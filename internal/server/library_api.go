package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// ---- browsing: albums, artists, songs, genres, search ----

func (s *Server) handleAlbums(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AlbumFilter{Sort: q.Get("sort"), Genre: q.Get("genre"), Year: queryInt(r, "year"), Decade: queryInt(r, "decade"), ArtistID: q.Get("artist"),
		Starred: queryBool(r, "starred"), Unplayed: queryBool(r, "unplayed"), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")}
	f.Seed, _ = strconv.ParseInt(q.Get("seed"), 10, 64)
	albums, total, err := s.store.ListAlbums(r.Context(), actorID(r.Context()), f)
	if err != nil {
		s.fail(w, err, "albums")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"albums": albums, "total": total})
}

// songsFor returns songs by id from the index, asking the server for ids the
// index does not have yet (a fresh install, a song added since the last scan).
func (s *Server) songsFor(ctx context.Context, uid int64, ids []string) ([]store.Song, error) {
	songs, err := s.store.SongsByID(ctx, uid, ids)
	if err != nil {
		return nil, err
	}
	if len(songs) < len(ids) && len(ids) <= 200 {
		have := map[string]bool{}
		for _, so := range songs {
			have[so.ID] = true
		}
		u, _ := s.store.GetUser(ctx, uid)
		if mc, err := s.musicFor(ctx, u); err == nil {
			byID := map[string]store.Song{}
			for _, so := range songs {
				byID[so.ID] = so
			}
			for _, id := range ids {
				if have[id] {
					continue
				}
				if so, err := mc.c.Song(ctx, id); err == nil {
					byID[id] = indexSong(*so)
				}
			}
			songs = songs[:0]
			for _, id := range ids {
				if so, ok := byID[id]; ok {
					songs = append(songs, so)
				}
			}
		}
	}
	s.store.FillSongPlays(ctx, uid, songs)
	return songs, nil
}

func (s *Server) handleAlbum(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	id := chi.URLParam(r, "id")
	a, err := s.store.GetAlbum(ctx, uid, id)
	var songs []store.Song
	if err == nil {
		songs, err = s.store.AlbumSongs(ctx, uid, id)
	}
	if errors.Is(err, store.ErrNotFound) || (err == nil && len(songs) == 0) {
		// not indexed yet: ask the server
		mc := s.music(w, r)
		if mc == nil {
			return
		}
		live, lerr := mc.c.Album(ctx, id)
		if lerr != nil {
			s.musicErr(w, lerr)
			return
		}
		al := indexAlbum(live.Album)
		a, err = &al, nil
		songs = make([]store.Song, 0, len(live.Song))
		for _, so := range live.Song {
			songs = append(songs, indexSong(so))
		}
	}
	if err != nil {
		s.fail(w, err, "album")
		return
	}
	s.store.FillSongPlays(ctx, uid, songs)
	plays, last := 0, ""
	for _, so := range songs {
		plays += so.Plays
		if so.LastPlayed > last {
			last = so.LastPlayed
		}
	}
	a.Plays, a.LastPlayed = plays, last
	more := []store.Album{}
	if a.ArtistID != "" {
		own, _, _ := s.store.ArtistAlbums(ctx, uid, a.ArtistID)
		for _, o := range own {
			if o.ID != a.ID {
				more = append(more, o)
			}
		}
	}
	out := map[string]any{"album": a, "songs": songs, "more": more, "genres": store.SplitGenres(a.Genres), "quality": albumQuality(songs)}
	if queryBool(r, "info") {
		if mc, err := s.musicFor(ctx, userFrom(ctx)); err == nil {
			if info, err := mc.c.AlbumInfo(ctx, id); err == nil {
				out["notes"] = cleanNotes(info.Notes)
				out["lastfm_url"] = info.LastFmURL
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// albumQuality summarises the files: "FLAC 24/96", "MP3 320".
func albumQuality(songs []store.Song) string {
	if len(songs) == 0 {
		return ""
	}
	so := songs[0]
	same := true
	for _, x := range songs[1:] {
		if x.Suffix != so.Suffix || x.BitDepth != so.BitDepth || x.SampleRate != so.SampleRate {
			same = false
			break
		}
	}
	f := strings.ToUpper(so.Suffix)
	if !same {
		return "Mixed"
	}
	switch so.Suffix {
	case "flac", "alac", "wav", "aiff", "ape", "wv", "dsf", "dff":
		if so.BitDepth > 0 && so.SampleRate > 0 {
			return f + " " + strconv.Itoa(so.BitDepth) + "/" + strings.TrimSuffix(strconv.FormatFloat(float64(so.SampleRate)/1000, 'f', 1, 64), ".0")
		}
		return f
	}
	if so.Bitrate > 0 {
		return f + " " + strconv.Itoa(so.Bitrate)
	}
	return f
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// cleanNotes strips the Last.fm "Read more" link and tags from notes / biographies.
func cleanNotes(s string) string {
	if i := strings.Index(s, "<a href=\"https://www.last.fm"); i >= 0 {
		s = s[:i]
	}
	s = tagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(strings.ReplaceAll(s, "&amp;", "&"))
}

func (s *Server) handleArtists(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.ArtistFilter{Sort: q.Get("sort"), Starred: queryBool(r, "starred"), AlbumArtist: !queryBool(r, "all"), Genre: q.Get("genre"), Limit: queryInt(r, "limit"), Offset: queryInt(r, "offset")}
	artists, total, err := s.store.ListArtists(r.Context(), actorID(r.Context()), f)
	if err != nil {
		s.fail(w, err, "artists")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artists": artists, "total": total})
}

func (s *Server) handleArtist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	id := chi.URLParam(r, "id")
	a, err := s.store.GetArtist(ctx, uid, id)
	if errors.Is(err, store.ErrNotFound) {
		mc := s.music(w, r)
		if mc == nil {
			return
		}
		live, lerr := mc.c.Artist(ctx, id)
		if lerr != nil {
			s.musicErr(w, lerr)
			return
		}
		a, err = &store.Artist{ID: live.ID, Name: live.Name, AlbumCount: live.AlbumCount, MBID: live.MusicBrainzID, StarredAt: live.Starred}, nil
	}
	if err != nil {
		s.fail(w, err, "artist")
		return
	}
	own, appears, err := s.store.ArtistAlbums(ctx, uid, id)
	if err != nil {
		s.fail(w, err, "artist albums")
		return
	}
	// the person's own most played songs by this artist, topped up with the most popular ones
	songs, _ := s.store.QuerySongs(ctx, uid, `artist_id = ? OR album_artist_id = ?`, ``, 0, id, id)
	s.store.FillSongPlays(ctx, uid, songs)
	sort.SliceStable(songs, func(i, j int) bool {
		if songs[i].Plays != songs[j].Plays {
			return songs[i].Plays > songs[j].Plays
		}
		if (songs[i].StarredAt != "") != (songs[j].StarredAt != "") {
			return songs[i].StarredAt != ""
		}
		return songs[i].ServerPlays > songs[j].ServerPlays
	})
	totalSongs := len(songs)
	plays := 0
	for _, so := range songs {
		plays += so.Plays
	}
	top := songs
	if len(top) > 10 {
		top = top[:10]
	}
	if plays == 0 {
		// nothing played yet: ask the server for the artist's well-known songs
		if mc, err := s.musicFor(ctx, userFrom(ctx)); err == nil {
			if ts, err := mc.c.TopSongs(ctx, a.Name, 10); err == nil && len(ts) > 0 {
				ids := make([]string, 0, len(ts))
				for _, t := range ts {
					ids = append(ids, t.ID)
				}
				if got, err := s.store.SongsByID(ctx, uid, ids); err == nil && len(got) > 0 {
					top = got
				}
			}
		}
	}
	a.Plays = plays
	out := map[string]any{"artist": a, "albums": own, "appears_on": appears, "top_songs": top, "song_count": totalSongs}
	if queryBool(r, "info") {
		if mc, err := s.musicFor(ctx, userFrom(ctx)); err == nil {
			if info, err := mc.c.ArtistInfo(ctx, id, 20); err == nil {
				out["biography"] = cleanNotes(info.Biography)
				out["lastfm_url"] = info.LastFmURL
				ids := []string{}
				for _, sa := range info.SimilarArtist {
					ids = append(ids, sa.ID)
				}
				similar, _ := s.store.ArtistsByID(ctx, uid, ids)
				out["similar"] = similar
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSongs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	q := r.URL.Query()
	var where []string
	var args []any
	if g := q.Get("genre"); g != "" {
		where = append(where, "genres LIKE ?")
		args = append(args, "%|"+g+"|%")
	}
	if queryBool(r, "starred") {
		where = append(where, "starred_at <> ''")
	}
	if a := q.Get("artist"); a != "" {
		where = append(where, "(artist_id = ? OR album_artist_id = ?)")
		args = append(args, a, a)
	}
	if y := queryInt(r, "decade"); y > 0 {
		where = append(where, "year BETWEEN ? AND ?")
		args = append(args, y, y+9)
	}
	if lossless := queryBool(r, "lossless"); lossless {
		where = append(where, "suffix IN ('flac','alac','wav','aiff','ape','wv','dsf','dff')")
	}
	order := "added_at DESC, album_id, disc, track"
	switch q.Get("sort") {
	case "title":
		order = "title COLLATE NOCASE"
	case "artist":
		order = "artist COLLATE NOCASE, year, album_id, disc, track"
	case "album":
		order = "album COLLATE NOCASE, disc, track"
	case "year":
		order = "year DESC, album_id, disc, track"
	case "duration":
		order = "duration DESC"
	case "starred":
		order = "starred_at DESC"
	case "random":
		order = "random()"
	}
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	offset := queryInt(r, "offset")
	cond := strings.Join(where, " AND ")
	var total int
	cq := `SELECT COUNT(1) FROM songs WHERE user_id = ?`
	if cond != "" {
		cq += ` AND (` + cond + `)`
	}
	_ = s.store.DB.QueryRowContext(ctx, cq, append([]any{uid}, args...)...).Scan(&total)
	songs, err := s.store.QuerySongs(ctx, uid, cond, order+" LIMIT "+strconv.Itoa(limit)+" OFFSET "+strconv.Itoa(offset), 0, args...)
	if err != nil {
		s.fail(w, err, "songs")
		return
	}
	s.store.FillSongPlays(ctx, uid, songs)
	writeJSON(w, http.StatusOK, map[string]any{"songs": songs, "total": total})
}

func (s *Server) handleSong(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	songs, err := s.songsFor(ctx, actorID(ctx), []string{chi.URLParam(r, "id")})
	if err != nil {
		s.fail(w, err, "song")
		return
	}
	if len(songs) == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, songs[0])
}

// handleSongsByID resolves many ids at once (restoring a queue).
func (s *Server) handleSongsByID(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(in.IDs) > 5000 {
		in.IDs = in.IDs[:5000]
	}
	songs, err := s.songsFor(r.Context(), actorID(r.Context()), in.IDs)
	if err != nil {
		s.fail(w, err, "songs")
		return
	}
	writeJSON(w, http.StatusOK, songs)
}

func (s *Server) handleGenres(w http.ResponseWriter, r *http.Request) {
	gs, err := s.store.Genres(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "genres")
		return
	}
	sort.Slice(gs, func(i, j int) bool {
		if gs[i].Songs != gs[j].Songs {
			return gs[i].Songs > gs[j].Songs
		}
		return gs[i].Name < gs[j].Name
	})
	writeJSON(w, http.StatusOK, gs)
}

// handleSearch finds artists, albums and songs in the index (the server's own
// search when the index is still empty).
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := map[string]any{"artists": []store.Artist{}, "albums": []store.Album{}, "songs": []store.Song{}}
	if q == "" {
		writeJSON(w, http.StatusOK, out)
		return
	}
	n := max(queryInt(r, "limit"), 1)
	if queryInt(r, "limit") == 0 {
		n = 8
	}
	if s.store.Sync(ctx, uid).SyncedAt == "" {
		mc := s.music(w, r)
		if mc == nil {
			return
		}
		ar, al, so, err := mc.c.Search(ctx, q, n, n, n*2)
		if err != nil {
			s.musicErr(w, err)
			return
		}
		artists := []store.Artist{}
		for _, a := range ar {
			artists = append(artists, store.Artist{ID: a.ID, Name: a.Name, AlbumCount: a.AlbumCount, StarredAt: a.Starred})
		}
		albums := []store.Album{}
		for _, a := range al {
			albums = append(albums, indexAlbum(a))
		}
		songs := []store.Song{}
		for _, x := range so {
			songs = append(songs, indexSong(x))
		}
		writeJSON(w, http.StatusOK, map[string]any{"artists": artists, "albums": albums, "songs": songs})
		return
	}
	get := func(kind string, limit int) []string {
		hits, err := s.store.Search(ctx, uid, q, kind, limit)
		if err != nil {
			return nil
		}
		ids := make([]string, 0, len(hits))
		for _, h := range hits {
			ids = append(ids, h.ID)
		}
		return ids
	}
	artists, _ := s.store.ArtistsByID(ctx, uid, get("artist", n*3))
	// people mean the artists with albums first
	sort.SliceStable(artists, func(i, j int) bool { return artists[i].AlbumCount > 0 && artists[j].AlbumCount == 0 })
	if len(artists) > n {
		artists = artists[:n]
	}
	albums, _ := s.store.AlbumsByID(ctx, uid, get("album", n))
	songs, _ := s.store.SongsByID(ctx, uid, get("song", n*2))
	s.store.FillSongPlays(ctx, uid, songs)
	writeJSON(w, http.StatusOK, map[string]any{"artists": artists, "albums": albums, "songs": songs})
}

// ---- stars and ratings ----

func (s *Server) handleStar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
		On   bool   `json:"on"`
	}
	if err := decode(r, &in); err != nil || in.ID == "" {
		writeErr(w, http.StatusBadRequest, "kind, id and on are required")
		return
	}
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	if err := mc.c.Star(ctx, in.On, in.Kind, in.ID); err != nil {
		s.musicErr(w, err)
		return
	}
	_ = s.store.SetStarred(ctx, u.ID, in.Kind, in.ID, in.On)
	if in.On {
		subject := ""
		switch in.Kind {
		case "song":
			if so, err := s.store.GetSong(ctx, u.ID, in.ID); err == nil {
				subject = so.Artist + " — " + so.Title
			}
		case "album":
			if a, err := s.store.GetAlbum(ctx, u.ID, in.ID); err == nil {
				subject = a.Artist + " — " + a.Name
			}
		case "artist":
			if a, err := s.store.GetArtist(ctx, u.ID, in.ID); err == nil {
				subject = a.Name
			}
		}
		s.publish(ctx, u, "music.loved", subject, map[string]any{"kind": in.Kind, "id": in.ID, "title": subject})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "starred": in.On})
}

func (s *Server) handleRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Rating int    `json:"rating"`
	}
	if err := decode(r, &in); err != nil || in.ID == "" || in.Rating < 0 || in.Rating > 5 {
		writeErr(w, http.StatusBadRequest, "kind, id and a rating 0–5 are required")
		return
	}
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	if err := mc.c.Rate(ctx, in.ID, in.Rating); err != nil {
		s.musicErr(w, err)
		return
	}
	_ = s.store.SetRating(ctx, actorID(ctx), in.Kind, in.ID, in.Rating)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- covers and audio (the browser never sees the server's credentials) ----

var coverID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !coverID.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	p := url.Values{"id": {id}}
	if size := queryInt(r, "size"); size > 0 {
		p.Set("size", strconv.Itoa(min(size, 1500)))
	}
	res, err := mc.c.Open(r.Context(), "getCoverArt", p, r.Header)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "music server unreachable")
		return
	}
	defer res.Body.Close()
	ct := res.Header.Get("Content-Type")
	if res.StatusCode == http.StatusNotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "image/") {
		// Subsonic errors come back as JSON with 200: no picture
		w.Header().Set("Cache-Control", "private, max-age=600")
		writeErr(w, http.StatusNotFound, "no cover")
		return
	}
	for _, k := range []string{"Content-Type", "Content-Length", "ETag", "Last-Modified"} {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = io.Copy(w, res.Body)
}

// handleStream plays a song: the original file with byte ranges (seekable),
// or transcoded by the server at the chosen bitrate (a seek reloads with
// timeOffset, see the player).
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !coverID.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	q := r.URL.Query()
	p := url.Values{"id": {id}}
	quality := q.Get("q")
	if br, err := strconv.Atoi(quality); err == nil && br >= 32 && br <= 320 {
		format := q.Get("format")
		if format != "mp3" && format != "aac" && format != "opus" {
			format = "mp3"
		}
		p.Set("format", format)
		p.Set("maxBitRate", strconv.Itoa(br))
		if off := queryInt(r, "offset"); off > 0 {
			p.Set("timeOffset", strconv.Itoa(off))
		}
	} else {
		p.Set("format", "raw")
	}
	res, err := mc.c.Open(r.Context(), "stream", p, r.Header)
	if err != nil {
		if r.Context().Err() == nil {
			writeErr(w, http.StatusBadGateway, "music server unreachable")
		}
		return
	}
	defer res.Body.Close()
	ct := res.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/xml") {
		writeErr(w, http.StatusNotFound, "the music server cannot play this song")
		return
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag", "X-Content-Duration"} {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(res.StatusCode)
	buf := make([]byte, 64<<10)
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok && p.Get("format") != "raw" {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// handleDownload gives the original file as an attachment.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !coverID.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	res, err := mc.c.Open(r.Context(), "download", url.Values{"id": {id}}, r.Header)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "music server unreachable")
		return
	}
	defer res.Body.Close()
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		writeErr(w, http.StatusForbidden, "the music server does not allow downloads for this account")
		return
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Last-Modified"} {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

// ---- the server's own lists (things the index cannot know) ----

// handleNowPlayingServer lists what other Subsonic players of the person play (car, phone app).
func (s *Server) handleServerNowPlaying(w http.ResponseWriter, r *http.Request) {
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	es, err := mc.c.NowPlaying(r.Context())
	if err != nil {
		s.musicErr(w, err)
		return
	}
	type entry struct {
		Song       store.Song `json:"song"`
		Player     string     `json:"player"`
		MinutesAgo int        `json:"minutes_ago"`
	}
	out := []entry{}
	for _, e := range es {
		if !strings.EqualFold(e.Username, mc.c.User) || e.PlayerName == subsonic.ClientName {
			continue
		}
		out = append(out, entry{Song: indexSong(e.Song), Player: e.PlayerName, MinutesAgo: e.MinutesAgo})
	}
	writeJSON(w, http.StatusOK, out)
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
