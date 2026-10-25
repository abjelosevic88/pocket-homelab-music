package server

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// ---- mixes and radio ----
//
// Everything here is computed from two things Pocket Music owns: the
// person's library index and their plays. Mixes change once a day (seeded by
// the date, so the same mix plays the same all day on every device); radio
// starts from a song, album, artist or genre and blends the music server's
// "similar songs" (Last.fm, when the server has a key) with what the person
// played around that song before and with the same genre and era.

// Mix is one generated playlist.
type Mix struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"` // daily | repeat | rediscover | fresh | deep | loved | capsule | focus
	SongIDs     []string `json:"-"`
	SongCount   int      `json:"song_count"`
	Duration    int      `json:"duration"`
	Covers      []string `json:"covers"` // album ids for a 2×2 collage
	Color       string   `json:"color"`
}

type mixSet struct {
	Mixes []Mix `json:"mixes"`
}

// library is the person's index plus play stats, loaded once per computation.
type library struct {
	songs   []store.Song
	byID    map[string]*store.Song
	stats   map[string]store.SongStat
	hidden  map[string]bool // genres never used
	ambient map[string]bool // background genres (kept out unless asked for)
	rng     *rand.Rand
}

func seedFor(parts ...string) *rand.Rand {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	v := h.Sum64()
	return rand.New(rand.NewPCG(v, v^0x9e3779b97f4a7c15))
}

func genreSet(csv string) map[string]bool {
	out := map[string]bool{}
	for _, g := range strings.Split(csv, ",") {
		if g = strings.ToLower(strings.TrimSpace(g)); g != "" {
			out[g] = true
		}
	}
	return out
}

func (s *Server) loadLibrary(ctx context.Context, uid int64, st store.Settings, seed string) (*library, error) {
	songs, err := s.store.QuerySongs(ctx, uid, "", "", 0)
	if err != nil {
		return nil, err
	}
	stats, err := s.store.SongStats(ctx, uid, store.Stamp(time.Now().AddDate(0, 0, -30)))
	if err != nil {
		return nil, err
	}
	lib := &library{songs: songs, byID: make(map[string]*store.Song, len(songs)), stats: stats, hidden: genreSet(st.HideGenres), ambient: map[string]bool{}, rng: seedFor(seed)}
	if st.SkipAmbient {
		lib.ambient = genreSet(st.AmbientGenres)
	}
	for i := range songs {
		lib.byID[songs[i].ID] = &songs[i]
	}
	return lib, nil
}

func (l *library) genresOf(so *store.Song) []string { return store.SplitGenres(so.Genres) }

// usable says whether a song may go into a mix (hidden and, unless wanted, background genres stay out).
func (l *library) usable(so *store.Song, allowAmbient bool) bool {
	if so.Duration > 0 && so.Duration < 45 {
		return false // intros, skits, jingles
	}
	for _, g := range l.genresOf(so) {
		lg := strings.ToLower(g)
		if l.hidden[lg] || (!allowAmbient && l.ambient[lg]) {
			return false
		}
	}
	return true
}

// spread orders songs so the same artist doesn't come twice in a row and
// appears at most perArtist times, keeping the given order as the preference.
func spread(songs []*store.Song, perArtist, limit int) []*store.Song {
	// with only a few artists to choose from, each may come more often (a small library still gets full mixes)
	artists := map[string]bool{}
	for _, so := range songs {
		artists[artistKey(so)] = true
	}
	if n := len(artists); n > 0 && n*perArtist < limit {
		perArtist = (min(limit, len(songs)) + n - 1) / n
	}
	count := map[string]int{}
	var out, later []*store.Song
	last := ""
	for len(out) < limit && (len(songs) > 0 || len(later) > 0) {
		picked := -1
		for i, so := range later {
			a := artistKey(so)
			if a != last {
				picked = i
				break
			}
		}
		if picked >= 0 {
			so := later[picked]
			later = append(later[:picked], later[picked+1:]...)
			out = append(out, so)
			last = artistKey(so)
			continue
		}
		if len(songs) == 0 {
			break
		}
		so := songs[0]
		songs = songs[1:]
		a := artistKey(so)
		if count[a] >= perArtist {
			continue
		}
		count[a]++
		if a == last {
			later = append(later, so)
			continue
		}
		out = append(out, so)
		last = a
	}
	return out
}

func artistKey(so *store.Song) string {
	if so.AlbumArtistID != "" && !strings.EqualFold(so.AlbumArtist, "various artists") {
		return so.AlbumArtistID
	}
	return strings.ToLower(so.Artist)
}

func shuffled[T any](rng *rand.Rand, xs []T) []T {
	out := append([]T(nil), xs...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// weighted orders songs by score with some randomness (higher first).
func weighted(rng *rand.Rand, songs []*store.Song, score func(*store.Song) float64) []*store.Song {
	type sc struct {
		s *store.Song
		v float64
	}
	xs := make([]sc, 0, len(songs))
	for _, so := range songs {
		xs = append(xs, sc{so, score(so) * (0.6 + rng.Float64()*0.8)})
	}
	sort.SliceStable(xs, func(i, j int) bool { return xs[i].v > xs[j].v })
	out := make([]*store.Song, len(xs))
	for i, x := range xs {
		out[i] = x.s
	}
	return out
}

func finishMix(m Mix, songs []*store.Song) Mix {
	m.SongIDs = make([]string, 0, len(songs))
	m.Covers = []string{}
	seen := map[string]bool{}
	for _, so := range songs {
		m.SongIDs = append(m.SongIDs, so.ID)
		m.Duration += so.Duration
		if !seen[so.AlbumID] && len(m.Covers) < 4 && so.AlbumID != "" {
			seen[so.AlbumID] = true
			m.Covers = append(m.Covers, so.AlbumID)
		}
	}
	m.SongCount = len(m.SongIDs)
	return m
}

var mixColors = []string{"#dc2626", "#7c3aed", "#0891b2", "#16a34a", "#d97706", "#db2777", "#2563eb", "#0d9488"}

// buildMixes computes the person's mixes for a day.
func (s *Server) buildMixes(ctx context.Context, uid int64, day string) ([]Mix, error) {
	st, _ := s.store.GetSettings(ctx, uid)
	lib, err := s.loadLibrary(ctx, uid, st, fmt.Sprint(uid, day))
	if err != nil {
		return nil, err
	}
	if len(lib.songs) == 0 {
		return []Mix{}, nil
	}
	now := time.Now()
	var mixes []Mix
	add := func(m Mix, songs []*store.Song, minimum int) {
		if len(songs) >= minimum {
			m.Color = mixColors[len(mixes)%len(mixColors)]
			mixes = append(mixes, finishMix(m, songs))
		}
	}

	// genre weights from the last 180 days of listening (played songs only)
	half := store.Stamp(now.AddDate(0, 0, -180))
	genreW := map[string]float64{}
	artistW := map[string]float64{}
	played := 0
	for id, stt := range lib.stats {
		so := lib.byID[id]
		if so == nil {
			continue
		}
		played++
		w := float64(stt.Plays)
		if stt.Last >= half {
			w *= 2
		}
		for _, g := range lib.genresOf(so) {
			genreW[g] += w
		}
		artistW[artistKey(so)] += w
	}
	if played < 20 {
		// little or no history yet: the library's own shape stands in
		for i := range lib.songs {
			for _, g := range lib.genresOf(&lib.songs[i]) {
				genreW[g] += 0.05
			}
		}
	}

	// Daily Mix 1–4: one per favourite genre, a third known songs, the rest new to you
	type gw struct {
		g string
		w float64
	}
	var gs []gw
	for g, w := range genreW {
		lg := strings.ToLower(g)
		if lib.hidden[lg] || lib.ambient[lg] {
			continue
		}
		gs = append(gs, gw{g, w})
	}
	sort.Slice(gs, func(i, j int) bool { return gs[i].w > gs[j].w })
	usedGenres := map[string]bool{}
	related := func(a, b string) bool {
		a, b = strings.ToLower(a), strings.ToLower(b)
		return strings.Contains(a, b) || strings.Contains(b, a)
	}
	inGenres := func(so *store.Song, set []string) bool {
		for _, g := range set {
			if strings.Contains(so.Genres, "|"+g+"|") {
				return true
			}
		}
		return false
	}
	countIn := func(set []string) int {
		n := 0
		for i := range lib.songs {
			if inGenres(&lib.songs[i], set) && lib.usable(&lib.songs[i], false) {
				n++
			}
		}
		return n
	}
	daily := 0
	for gi, g := range gs {
		if daily == 4 || len(gs) == 0 {
			break
		}
		// skip a genre that is just a sub-label of one already used ("Rock" after "Hard Rock")
		dup := false
		for u := range usedGenres {
			if related(g.g, u) {
				dup = true
			}
		}
		if dup {
			continue
		}
		// a small genre takes its neighbours along (the next favourites) until there is enough music
		set := []string{g.g}
		for _, more := range gs[gi+1:] {
			if countIn(set) >= 25 || len(set) == 3 {
				break
			}
			if usedGenres[more.g] {
				continue
			}
			taken := false
			for u := range usedGenres {
				if related(more.g, u) {
					taken = true
				}
			}
			if !taken {
				set = append(set, more.g)
			}
		}
		var known, fresh []*store.Song
		for i := range lib.songs {
			so := &lib.songs[i]
			if !inGenres(so, set) || !lib.usable(so, false) {
				continue
			}
			if lib.stats[so.ID].Plays > 0 || so.StarredAt != "" {
				known = append(known, so)
			} else {
				fresh = append(fresh, so)
			}
		}
		if len(known)+len(fresh) < 12 {
			continue
		}
		known = weighted(lib.rng, known, func(so *store.Song) float64 {
			v := 1 + float64(lib.stats[so.ID].Plays)
			if so.StarredAt != "" {
				v += 3
			}
			return v
		})
		fresh = weighted(lib.rng, fresh, func(so *store.Song) float64 { return 1 + artistW[artistKey(so)]/10 + float64(so.Rating) })
		// about a third you know, the rest new to you (all known when there is nothing new)
		nKnown := max(17, 50-len(fresh))
		pick := append(append([]*store.Song{}, head(known, nKnown)...), head(fresh, 60)...)
		pick = spread(shuffled(lib.rng, pick), 3, 50)
		for _, x := range set {
			usedGenres[x] = true
		}
		daily++
		names := topArtistNames(pick, 3)
		label := strings.Join(set, " & ")
		if len(set) == 3 {
			label = set[0] + ", " + set[1] + " & " + set[2]
		}
		add(Mix{Key: fmt.Sprintf("daily-%d", daily), Name: fmt.Sprintf("Daily Mix %d", daily), Kind: "daily",
			Description: label + " · " + joinAnd(names)}, pick, 12)
	}

	// On Repeat: what you played most in the last 30 days
	var rep []*store.Song
	for id, stt := range lib.stats {
		if so := lib.byID[id]; so != nil && stt.Recent >= 2 {
			rep = append(rep, so)
		}
	}
	sort.Slice(rep, func(i, j int) bool { return lib.stats[rep[i].ID].Recent > lib.stats[rep[j].ID].Recent })
	add(Mix{Key: "repeat", Name: "On Repeat", Kind: "repeat", Description: "The songs you have played most this month"}, spread(rep, 4, 40), 8)

	// Rediscover: old favourites you haven't heard in four months
	cut := store.Stamp(now.AddDate(0, -4, 0))
	var old []*store.Song
	for id, stt := range lib.stats {
		if so := lib.byID[id]; so != nil && stt.Plays >= 3 && stt.Last < cut && lib.usable(so, false) {
			old = append(old, so)
		}
	}
	old = weighted(lib.rng, old, func(so *store.Song) float64 { return float64(lib.stats[so.ID].Plays) })
	add(Mix{Key: "rediscover", Name: "Rediscover", Kind: "rediscover", Description: "Old favourites you haven't played in a while"}, spread(old, 3, 50), 10)

	// Fresh in your library: albums added in the last 60 days, not played yet
	addedCut := now.AddDate(0, 0, -60).UTC().Format(time.RFC3339)
	byAlbum := map[string][]*store.Song{}
	var albumOrder []string
	for i := range lib.songs {
		so := &lib.songs[i]
		if so.AddedAt >= addedCut && lib.stats[so.ID].Plays == 0 && lib.usable(so, false) {
			if _, ok := byAlbum[so.AlbumID]; !ok {
				albumOrder = append(albumOrder, so.AlbumID)
			}
			byAlbum[so.AlbumID] = append(byAlbum[so.AlbumID], so)
		}
	}
	var freshPick []*store.Song
	for round := 0; round < 3; round++ {
		for _, a := range shuffled(lib.rng, albumOrder) {
			list := byAlbum[a]
			if round < len(list) {
				freshPick = append(freshPick, shuffled(lib.rng, list)[round])
			}
		}
	}
	add(Mix{Key: "fresh", Name: "Fresh in your library", Kind: "fresh", Description: "From albums added in the last two months you haven't played yet"}, spread(freshPick, 3, 50), 6)

	// Deep cuts: songs you never played by the artists you play most
	type aw struct {
		a string
		w float64
	}
	var top []aw
	for a, w := range artistW {
		top = append(top, aw{a, w})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].w > top[j].w })
	fav := map[string]bool{}
	for i := 0; i < len(top) && i < 25; i++ {
		fav[top[i].a] = true
	}
	var deep []*store.Song
	for i := range lib.songs {
		so := &lib.songs[i]
		if fav[artistKey(so)] && lib.stats[so.ID].Plays == 0 && lib.usable(so, false) {
			deep = append(deep, so)
		}
	}
	add(Mix{Key: "deep", Name: "Deep Cuts", Kind: "deep", Description: "Songs you have never played by the artists you play most"}, spread(shuffled(lib.rng, deep), 3, 50), 6)

	// Time capsule: what you played in this month in earlier years
	var capsule []*store.Song
	month := now.Format("-01-")
	if rows, err := s.store.DB.QueryContext(ctx, `SELECT song_id, COUNT(1) n FROM listens WHERE user_id = ? AND song_id <> '' AND substr(played_at, 5, 4) = ? AND played_at < ? GROUP BY song_id ORDER BY n DESC LIMIT 200`,
		uid, month, store.Stamp(now.AddDate(0, -11, 0))); err == nil {
		for rows.Next() {
			var id string
			var n int
			if rows.Scan(&id, &n) == nil {
				if so := lib.byID[id]; so != nil {
					capsule = append(capsule, so)
				}
			}
		}
		rows.Close()
	}
	add(Mix{Key: "capsule", Name: "Time Capsule", Kind: "capsule", Description: "What you listened to in " + now.Format("January") + " in years past"}, spread(capsule, 3, 40), 10)

	// Loved: your starred songs, shuffled
	var loved []*store.Song
	for i := range lib.songs {
		if lib.songs[i].StarredAt != "" {
			loved = append(loved, &lib.songs[i])
		}
	}
	add(Mix{Key: "loved", Name: "Loved", Kind: "loved", Description: "Your starred songs, shuffled"}, spread(shuffled(lib.rng, loved), 5, 100), 5)

	// Focus: background music (the genres Settings calls ambient)
	if len(lib.ambient) > 0 {
		var focus []*store.Song
		for i := range lib.songs {
			so := &lib.songs[i]
			if so.Duration < 90 {
				continue
			}
			for _, g := range lib.genresOf(so) {
				if lib.ambient[strings.ToLower(g)] && !lib.hidden[strings.ToLower(g)] {
					focus = append(focus, so)
					break
				}
			}
		}
		add(Mix{Key: "focus", Name: "Focus", Kind: "focus", Description: "Ambient and background music for working, reading and sleep"}, spread(shuffled(lib.rng, focus), 3, 40), 5)
	}
	return mixes, nil
}

func head[T any](xs []T, n int) []T {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

func topArtistNames(songs []*store.Song, n int) []string {
	count := map[string]int{}
	name := map[string]string{}
	for _, so := range songs {
		k := artistKey(so)
		count[k]++
		name[k] = firstNonEmpty(so.AlbumArtist, so.Artist)
		if strings.EqualFold(name[k], "various artists") {
			name[k] = so.Artist
		}
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return count[keys[i]] > count[keys[j]] })
	out := []string{}
	for _, k := range head(keys, n) {
		out = append(out, name[k])
	}
	return out
}

// mixVersion changes the cache key when the way mixes are made changes.
const mixVersion = 2

// mixesFor returns today's mixes, cached until the day or the index changes.
func (s *Server) mixesFor(ctx context.Context, uid int64, off int) ([]Mix, error) {
	day := time.Now().In(zone(off)).Format("2006-01-02")
	sync := s.store.Sync(ctx, uid)
	n, _ := s.store.CountListens(ctx, uid)
	key := fmt.Sprintf("mixes:%d:%s:%s:%d:v%d", uid, day, sync.SyncedAt, n/25, mixVersion) // every 25 new plays refresh too
	if raw, _, ok := s.store.CacheGet(ctx, key); ok {
		var cached []struct {
			Mix
			IDs []string `json:"ids"`
		}
		if json.Unmarshal([]byte(raw), &cached) == nil {
			out := make([]Mix, len(cached))
			for i, c := range cached {
				c.Mix.SongIDs = c.IDs
				out[i] = c.Mix
			}
			return out, nil
		}
	}
	mixes, err := s.buildMixes(ctx, uid, day)
	if err != nil {
		return nil, err
	}
	type withIDs struct {
		Mix
		IDs []string `json:"ids"`
	}
	ws := make([]withIDs, len(mixes))
	for i, m := range mixes {
		ws[i] = withIDs{m, m.SongIDs}
	}
	b, _ := json.Marshal(ws)
	_ = s.store.CacheDelete(ctx, fmt.Sprintf("mixes:%d:", uid))
	_ = s.store.CachePut(ctx, key, string(b))
	return mixes, nil
}

func (s *Server) handleMixes(w http.ResponseWriter, r *http.Request) {
	mixes, err := s.mixesFor(r.Context(), actorID(r.Context()), tzOffset(r))
	if err != nil {
		s.fail(w, err, "mixes")
		return
	}
	writeJSON(w, http.StatusOK, mixes)
}

func (s *Server) handleMix(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	mixes, err := s.mixesFor(ctx, uid, tzOffset(r))
	if err != nil {
		s.fail(w, err, "mixes")
		return
	}
	key := chi.URLParam(r, "key")
	for _, m := range mixes {
		if m.Key == key {
			songs, err := s.store.SongsByID(ctx, uid, m.SongIDs)
			if err != nil {
				s.fail(w, err, "mix")
				return
			}
			s.store.FillSongPlays(ctx, uid, songs)
			writeJSON(w, http.StatusOK, map[string]any{"mix": m, "songs": songs})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "no such mix today")
}

// ---- radio ----

func (s *Server) handleRadio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	q := r.URL.Query()
	st, _ := s.store.GetSettings(ctx, u.ID)
	lib, err := s.loadLibrary(ctx, u.ID, st, fmt.Sprint(u.ID, time.Now().UnixNano()))
	if err != nil {
		s.fail(w, err, "radio")
		return
	}
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	score := map[string]float64{}
	bump := func(id string, v float64) {
		if so := lib.byID[id]; so != nil {
			score[id] += v
		}
	}
	var seed *store.Song
	var seedGenres []string
	seedYear := 0
	title := "Radio"
	mc, _ := s.musicFor(ctx, u)
	similar := func(id string, weight float64) {
		if mc == nil {
			return
		}
		// the server asks Last.fm: remembered for a day, and a slow answer
		// doesn't hold the music up
		key := fmt.Sprintf("similar:%d:%s", u.ID, id)
		var ids []string
		if raw, at, ok := s.store.CacheGet(ctx, key); ok {
			if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < 24*time.Hour {
				_ = json.Unmarshal([]byte(raw), &ids)
			}
		}
		if ids == nil {
			sctx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			if ss, err := mc.c.SimilarSongs(sctx, id, 60); err == nil {
				ids = make([]string, 0, len(ss))
				for _, so := range ss {
					ids = append(ids, so.ID)
				}
				b, _ := json.Marshal(ids)
				_ = s.store.CachePut(ctx, key, string(b))
			}
		}
		for i, sid := range ids {
			bump(sid, weight*(1-float64(i)/float64(len(ids)+10)))
		}
	}
	switch {
	case q.Get("song") != "":
		seed = lib.byID[q.Get("song")]
		if seed == nil {
			writeErr(w, http.StatusNotFound, "song not found")
			return
		}
		title = seed.Title + " radio"
		seedGenres, seedYear = lib.genresOf(seed), seed.Year
		similar(seed.ID, 4)
		if nb, err := s.store.SessionNeighbours(ctx, u.ID, seed.ID, 45*time.Minute, limit); err == nil {
			for id, n := range nb {
				bump(id, 1.5*float64(min(n, 4)))
			}
		}
		for i := range lib.songs {
			if artistKey(&lib.songs[i]) == artistKey(seed) && lib.songs[i].ID != seed.ID {
				bump(lib.songs[i].ID, 1.2)
			}
		}
	case q.Get("artist") != "":
		id := q.Get("artist")
		a, err := s.store.GetArtist(ctx, u.ID, id)
		if err != nil {
			s.fail(w, err, "artist")
			return
		}
		title = a.Name + " radio"
		// the server's two Last.fm lookups run side by side
		infoCh := make(chan map[string]bool, 1)
		go func() {
			sim := map[string]bool{}
			if mc != nil {
				ictx, cancel := context.WithTimeout(ctx, 4*time.Second)
				defer cancel()
				if info, err := mc.c.ArtistInfo(ictx, id, 15); err == nil {
					for _, sa := range info.SimilarArtist {
						sim[sa.ID] = true
					}
				}
			}
			infoCh <- sim
		}()
		similar(id, 3)
		gcount := map[string]int{}
		for i := range lib.songs {
			so := &lib.songs[i]
			if so.ArtistID == id || so.AlbumArtistID == id {
				bump(so.ID, 2+float64(lib.stats[so.ID].Plays)/5)
				for _, g := range lib.genresOf(so) {
					gcount[g]++
				}
				if seedYear == 0 {
					seedYear = so.Year
				}
			}
		}
		seedGenres = topKeys(gcount, 3)
		if sim := <-infoCh; len(sim) > 0 {
			for i := range lib.songs {
				so := &lib.songs[i]
				if sim[so.ArtistID] || sim[so.AlbumArtistID] {
					bump(so.ID, 1.6)
				}
			}
		}
	case q.Get("album") != "":
		al, err := s.store.GetAlbum(ctx, u.ID, q.Get("album"))
		if err != nil {
			s.fail(w, err, "album")
			return
		}
		title = al.Name + " radio"
		seedGenres, seedYear = store.SplitGenres(al.Genres), al.Year
		for i := range lib.songs {
			so := &lib.songs[i]
			if so.AlbumID == al.ID {
				bump(so.ID, 1.5)
				if seed == nil || lib.stats[so.ID].Plays > lib.stats[seed.ID].Plays {
					seed = so
				}
			}
		}
		if seed != nil {
			similar(seed.ID, 3)
			seed = nil
		}
	case q.Get("genre") != "":
		g := q.Get("genre")
		title = g + " radio"
		seedGenres = []string{g}
	default:
		writeErr(w, http.StatusBadRequest, "start radio from a song, album, artist or genre")
		return
	}
	// the same genre (and era) fills the rest, leaning to what the person likes
	gset := map[string]bool{}
	for _, g := range seedGenres {
		gset[g] = true
	}
	allowAmbient := false
	for g := range gset {
		if lib.ambient[strings.ToLower(g)] {
			allowAmbient = true
		}
	}
	for i := range lib.songs {
		so := &lib.songs[i]
		match := false
		for _, g := range lib.genresOf(so) {
			if gset[g] {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		v := 0.6
		if seedYear > 0 && so.Year > 0 && abs(so.Year-seedYear) <= 6 {
			v += 0.4
		}
		if so.StarredAt != "" {
			v += 0.5
		}
		v += minF(float64(lib.stats[so.ID].Plays)/10, 0.6)
		score[so.ID] += v
	}
	var cands []*store.Song
	for id := range score {
		so := lib.byID[id]
		if so == nil || (seed != nil && id == seed.ID) || !lib.usable(so, allowAmbient) {
			continue
		}
		cands = append(cands, so)
	}
	ordered := weighted(lib.rng, cands, func(so *store.Song) float64 { return score[so.ID] })
	out := spread(ordered, 4, limit)
	if seed != nil {
		out = append([]*store.Song{seed}, head(out, limit-1)...)
	}
	songs := make([]store.Song, 0, len(out))
	for _, so := range out {
		songs = append(songs, *so)
	}
	s.store.FillSongPlays(ctx, u.ID, songs)
	writeJSON(w, http.StatusOK, map[string]any{"name": title, "songs": songs})
}

func topKeys(m map[string]int, n int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
	return head(keys, n)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// handleShuffle plays the whole library (or a genre / decade / starred) shuffled, artist-spread.
func (s *Server) handleShuffle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	lib, err := s.loadLibrary(ctx, uid, st, fmt.Sprint(uid, time.Now().UnixNano()))
	if err != nil {
		s.fail(w, err, "shuffle")
		return
	}
	q := r.URL.Query()
	g, decade, starred := q.Get("genre"), queryInt(r, "decade"), queryBool(r, "starred")
	var pool []*store.Song
	for i := range lib.songs {
		so := &lib.songs[i]
		if g != "" && !strings.Contains(so.Genres, "|"+g+"|") {
			continue
		}
		if decade > 0 && (so.Year < decade || so.Year > decade+9) {
			continue
		}
		if starred && so.StarredAt == "" {
			continue
		}
		if !lib.usable(so, g != "") {
			continue
		}
		pool = append(pool, so)
	}
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	out := spread(shuffled(lib.rng, pool), 6, limit)
	songs := make([]store.Song, 0, len(out))
	for _, so := range out {
		songs = append(songs, *so)
	}
	writeJSON(w, http.StatusOK, map[string]any{"songs": songs})
}

// ---- mixes as playlists on the music server ----
//
// With "Publish mixes as playlists" on, each morning the person's mixes are
// written to their music server account as playlists ("Pocket · Daily Mix 1"
// …), so phone and car apps that only speak Subsonic get them too. The same
// playlists are reused day after day (their ids live in the cache under
// mixpl:<uid>); a mix that is gone today loses its playlist, and switching
// the option off deletes them all.

const mixPlaylistPrefix = "Pocket · "

func (s *Server) mixPlaylists(ctx context.Context, uid int64) map[string]string {
	out := map[string]string{}
	if raw, _, ok := s.store.CacheGet(ctx, fmt.Sprintf("mixpl:%d", uid)); ok {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) saveMixPlaylists(ctx context.Context, uid int64, m map[string]string) {
	b, _ := json.Marshal(m)
	_ = s.store.CachePut(ctx, fmt.Sprintf("mixpl:%d", uid), string(b))
}

// publishMixes writes the person's mixes of today to their server (or, with
// the option off, removes the ones written before).
func (s *Server) publishMixes(ctx context.Context, uid int64) error {
	u, err := s.store.GetUser(ctx, uid)
	if err != nil {
		return err
	}
	st, _ := s.store.GetSettings(ctx, uid)
	have := s.mixPlaylists(ctx, uid)
	if !st.MixesToServer && len(have) == 0 {
		return nil
	}
	mc, err := s.musicFor(ctx, u)
	if err != nil {
		return err
	}
	if !st.MixesToServer {
		for key, id := range have {
			if err := mc.c.DeletePlaylist(ctx, id); err == nil || subsonic.IsNotFound(err) {
				delete(have, key)
			}
		}
		s.saveMixPlaylists(ctx, uid, have)
		s.livePlaylists(uid, "deleted", nil, 0)
		return nil
	}
	_, off := time.Now().Zone()
	mixes, err := s.mixesFor(ctx, uid, off/60)
	if err != nil {
		return err
	}
	today := map[string]bool{}
	comment := "Made by Pocket Music for " + time.Now().Format("Monday 2 January") + " — it changes every day."
	for _, m := range mixes {
		if len(m.SongIDs) == 0 {
			continue
		}
		today[m.Key] = true
		name := mixPlaylistPrefix + m.Name
		id := have[m.Key]
		if id != "" {
			if _, err := mc.c.Playlist(ctx, id); subsonic.IsNotFound(err) {
				id = "" // deleted on the server: make it again
			}
		}
		if id == "" {
			pl, err := mc.c.CreatePlaylist(ctx, "", name, m.SongIDs)
			if err != nil {
				return err
			}
			id = pl.ID
			have[m.Key] = id
			s.saveMixPlaylists(ctx, uid, have)
		} else if _, err := mc.c.CreatePlaylist(ctx, id, "", m.SongIDs); err != nil {
			return err
		}
		desc := firstNonEmpty(m.Description, m.Name) + " · " + comment
		_ = mc.c.UpdatePlaylist(ctx, id, subsonic.PlaylistUpdate{Name: &name, Comment: &desc})
	}
	for key, id := range have {
		if !today[key] {
			if err := mc.c.DeletePlaylist(ctx, id); err == nil || subsonic.IsNotFound(err) {
				delete(have, key)
			}
		}
	}
	s.saveMixPlaylists(ctx, uid, have)
	ids := make([]string, 0, len(have))
	for _, id := range have {
		ids = append(ids, id)
	}
	s.livePlaylists(uid, "updated", ids, 0)
	return nil
}

// publishMixesDaily runs publishMixes once a day per person, from 05:00 (janitor).
func (s *Server) publishMixesDaily(ctx context.Context) {
	if time.Now().Hour() < 5 {
		return
	}
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	day := time.Now().Format("2006-01-02")
	for _, uid := range ids {
		if ctx.Err() != nil {
			return
		}
		key := fmt.Sprintf("mixpub:%d", uid)
		if v, _, ok := s.store.CacheGet(ctx, key); ok && v == day {
			continue
		}
		if err := s.publishMixes(ctx, uid); err != nil {
			if !isNoMusic(err) {
				s.log.Warn("mixes to server", "user", uid, "err", err)
			}
			continue
		}
		_ = s.store.CachePut(ctx, key, day)
	}
}
