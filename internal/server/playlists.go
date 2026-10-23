package server

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// ---- playlists (kept on the music server, so every player sees them) ----

type playlistView struct {
	subsonic.Playlist
	Mine    bool     `json:"mine"`
	SmartID int64    `json:"smart_id,omitempty"`
	Covers  []string `json:"covers"`
}

func (s *Server) handlePlaylists(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	pls, err := mc.c.Playlists(ctx)
	if err != nil {
		s.musicErr(w, err)
		return
	}
	smart, _ := s.store.ListSmart(ctx, uid)
	bySmart := map[string]int64{}
	for _, sp := range smart {
		if sp.PlaylistID != "" {
			bySmart[sp.PlaylistID] = sp.ID
		}
	}
	out := make([]playlistView, 0, len(pls))
	for _, p := range pls {
		out = append(out, playlistView{Playlist: p, Mine: strings.EqualFold(p.Owner, mc.c.User), SmartID: bySmart[p.ID], Covers: []string{}})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Changed > out[j].Changed })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	p, err := mc.c.Playlist(ctx, chi.URLParam(r, "id"))
	if err != nil {
		s.musicErr(w, err)
		return
	}
	ids := make([]string, 0, len(p.Entry))
	for _, e := range p.Entry {
		ids = append(ids, e.ID)
	}
	songs := make([]store.Song, 0, len(p.Entry))
	idx, _ := s.store.SongsByID(ctx, uid, ids)
	byID := map[string]store.Song{}
	for _, so := range idx {
		byID[so.ID] = so
	}
	for _, e := range p.Entry {
		if so, ok := byID[e.ID]; ok {
			songs = append(songs, so)
		} else {
			songs = append(songs, indexSong(e))
		}
	}
	s.store.FillSongPlays(ctx, uid, songs)
	view := playlistView{Playlist: p.Playlist, Mine: strings.EqualFold(p.Owner, mc.c.User), Covers: []string{}}
	seen := map[string]bool{}
	for _, so := range songs {
		if !seen[so.AlbumID] && len(view.Covers) < 4 {
			seen[so.AlbumID] = true
			view.Covers = append(view.Covers, so.AlbumID)
		}
	}
	out := map[string]any{"playlist": view, "songs": songs}
	if sp, err := s.store.SmartByPlaylist(ctx, uid, p.ID); err == nil {
		view.SmartID = sp.ID
		out["playlist"] = view
		out["smart"] = sp
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreatePlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		Name    string   `json:"name"`
		Comment string   `json:"comment"`
		SongIDs []string `json:"song_ids"`
		Public  bool     `json:"public"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "a name is required")
		return
	}
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	p, err := mc.c.CreatePlaylist(ctx, "", strings.TrimSpace(in.Name), in.SongIDs)
	if err != nil {
		s.musicErr(w, err)
		return
	}
	if p.ID != "" && (in.Comment != "" || in.Public) {
		c, pub := in.Comment, in.Public
		_ = mc.c.UpdatePlaylist(ctx, p.ID, subsonic.PlaylistUpdate{Comment: &c, Public: &pub})
	}
	s.publish(ctx, userFrom(ctx), "music.playlist_created", in.Name, map[string]any{"id": p.ID, "name": in.Name, "songs": len(in.SongIDs)})
	writeJSON(w, http.StatusCreated, p.Playlist)
}

// handleUpdatePlaylist renames, re-describes, adds or removes songs, or
// replaces the whole order (song_ids).
func (s *Server) handleUpdatePlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	var in struct {
		Name    *string   `json:"name"`
		Comment *string   `json:"comment"`
		Public  *bool     `json:"public"`
		Add     []string  `json:"add"`
		Remove  []int     `json:"remove"` // positions
		SongIDs *[]string `json:"song_ids"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	if sp, err := s.store.SmartByPlaylist(ctx, actorID(ctx), id); err == nil && (in.SongIDs != nil || len(in.Add) > 0 || len(in.Remove) > 0) {
		writeErr(w, http.StatusConflict, fmt.Sprintf("“%s” is a smart playlist: change its rules instead", sp.Name))
		return
	}
	if in.SongIDs != nil {
		if _, err := mc.c.CreatePlaylist(ctx, id, "", *in.SongIDs); err != nil {
			s.musicErr(w, err)
			return
		}
	}
	if in.Name != nil || in.Comment != nil || in.Public != nil || len(in.Add) > 0 || len(in.Remove) > 0 {
		if err := mc.c.UpdatePlaylist(ctx, id, subsonic.PlaylistUpdate{Name: in.Name, Comment: in.Comment, Public: in.Public, Add: in.Add, RemoveIndex: in.Remove}); err != nil {
			s.musicErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeletePlaylist(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	mc := s.music(w, r)
	if mc == nil {
		return
	}
	if err := mc.c.DeletePlaylist(ctx, id); err != nil {
		s.musicErr(w, err)
		return
	}
	if sp, err := s.store.SmartByPlaylist(ctx, actorID(ctx), id); err == nil {
		_ = s.store.DeleteSmart(ctx, actorID(ctx), sp.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- smart playlists ----
//
// Rules run over the person's index and plays here, and the result is
// written to a real playlist on the music server, refreshed every day: the
// car stereo and the phone app see "Not played in a year" like any other
// playlist. Fields: genre, artist, album, title, year, added (days ago),
// plays, last_played (days ago), starred, rating, duration (s), format,
// bitrate, path.

var smartFields = map[string]string{
	"genre": "text", "artist": "text", "album_artist": "text", "album": "text", "title": "text", "path": "text", "format": "text",
	"year": "number", "plays": "number", "rating": "number", "duration": "number", "bitrate": "number", "bpm": "number",
	"added": "days", "last_played": "days", "starred": "bool", "lossless": "bool", "never_played": "bool",
}

func validSmart(p *store.SmartPlaylist) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("a name is required")
	}
	if p.Rules.Match != "any" {
		p.Rules.Match = "all"
	}
	for _, c := range p.Rules.Conditions {
		kind, ok := smartFields[c.Field]
		if !ok {
			return fmt.Errorf("unknown field %q", c.Field)
		}
		ops := map[string][]string{
			"text":   {"is", "is_not", "contains", "not_contains", "starts_with"},
			"number": {"is", "is_not", "gt", "lt", "between"},
			"days":   {"within", "not_within", "between"},
			"bool":   {"is"},
		}[kind]
		found := false
		for _, o := range ops {
			found = found || o == c.Op
		}
		if !found {
			return fmt.Errorf("%s cannot use %q", c.Field, c.Op)
		}
	}
	switch p.Sort {
	case "random", "added", "added_asc", "plays", "least_played", "last_played", "year", "year_asc", "artist", "album", "rating", "title":
	default:
		p.Sort = "random"
	}
	if p.LimitCount < 0 || p.LimitCount > 5000 {
		p.LimitCount = 100
	}
	if p.LimitMinutes < 0 || p.LimitMinutes > 6000 {
		p.LimitMinutes = 0
	}
	if p.Refresh != "manual" {
		p.Refresh = "daily"
	}
	return nil
}

var lossless = map[string]bool{"flac": true, "alac": true, "wav": true, "aiff": true, "ape": true, "wv": true, "dsf": true, "dff": true}

func ruleMatch(c store.Rule, so *store.Song, stt store.SongStat, now time.Time) bool {
	textOf := func() []string {
		switch c.Field {
		case "genre":
			return store.SplitGenres(so.Genres)
		case "artist":
			return []string{so.Artist}
		case "album_artist":
			return []string{so.AlbumArtist}
		case "album":
			return []string{so.Album}
		case "title":
			return []string{so.Title}
		case "path":
			return []string{so.Path}
		case "format":
			return []string{so.Suffix}
		}
		return nil
	}
	num := func() float64 {
		switch c.Field {
		case "year":
			return float64(so.Year)
		case "plays":
			return float64(stt.Plays)
		case "rating":
			return float64(so.Rating)
		case "duration":
			return float64(so.Duration)
		case "bitrate":
			return float64(so.Bitrate)
		case "bpm":
			return float64(so.BPM)
		}
		return 0
	}
	daysAgo := func() (float64, bool) {
		var at string
		if c.Field == "added" {
			at = so.AddedAt
		} else {
			at = stt.Last
		}
		if at == "" {
			return 0, false
		}
		t, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return 0, false
		}
		return now.Sub(t).Hours() / 24, true
	}
	v := strings.ToLower(strings.TrimSpace(c.Value))
	switch smartFields[c.Field] {
	case "text":
		vals := textOf()
		any := func(f func(string) bool) bool {
			for _, x := range vals {
				if f(strings.ToLower(x)) {
					return true
				}
			}
			return false
		}
		switch c.Op {
		case "is":
			return any(func(x string) bool { return x == v })
		case "is_not":
			return !any(func(x string) bool { return x == v })
		case "contains":
			return any(func(x string) bool { return strings.Contains(x, v) })
		case "not_contains":
			return !any(func(x string) bool { return strings.Contains(x, v) })
		case "starts_with":
			return any(func(x string) bool { return strings.HasPrefix(x, v) })
		}
	case "number":
		n := num()
		a, _ := strconv.ParseFloat(v, 64)
		b, _ := strconv.ParseFloat(strings.TrimSpace(c.Value2), 64)
		switch c.Op {
		case "is":
			return n == a
		case "is_not":
			return n != a
		case "gt":
			return n > a
		case "lt":
			return n < a
		case "between":
			return n >= minF(a, b) && n <= maxF(a, b)
		}
	case "days":
		d, ok := daysAgo()
		a, _ := strconv.ParseFloat(v, 64)
		b, _ := strconv.ParseFloat(strings.TrimSpace(c.Value2), 64)
		switch c.Op {
		case "within":
			return ok && d <= a
		case "not_within":
			return !ok || d > a // never played counts as "not in the last N days"
		case "between":
			return ok && d >= minF(a, b) && d <= maxF(a, b)
		}
	case "bool":
		want := v != "false" && v != "0" && v != "no"
		var got bool
		switch c.Field {
		case "starred":
			got = so.StarredAt != ""
		case "lossless":
			got = lossless[so.Suffix]
		case "never_played":
			got = stt.Plays == 0
		}
		return got == want
	}
	return false
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// evalSmart returns the songs a smart playlist holds right now.
func (s *Server) evalSmart(ctx context.Context, uid int64, p *store.SmartPlaylist) ([]store.Song, error) {
	songs, err := s.store.QuerySongs(ctx, uid, "", "", 0)
	if err != nil {
		return nil, err
	}
	stats, err := s.store.SongStats(ctx, uid, store.Stamp(time.Now().AddDate(0, 0, -30)))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := []store.Song{}
	for i := range songs {
		so := &songs[i]
		stt := stats[so.ID]
		ok := p.Rules.Match != "any" || len(p.Rules.Conditions) == 0
		for _, c := range p.Rules.Conditions {
			m := ruleMatch(c, so, stt, now)
			if p.Rules.Match == "any" && m {
				ok = true
				break
			}
			if p.Rules.Match != "any" && !m {
				ok = false
				break
			}
		}
		if ok {
			so.Plays, so.LastPlayed = stt.Plays, stt.Last
			out = append(out, *so)
		}
	}
	rng := rand.New(rand.NewPCG(uint64(p.ID), uint64(time.Now().YearDay())))
	switch p.Sort {
	case "random":
		rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	case "added":
		sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt > out[j].AddedAt })
	case "added_asc":
		sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt < out[j].AddedAt })
	case "plays":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Plays > out[j].Plays })
	case "least_played":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Plays < out[j].Plays })
	case "last_played":
		sort.SliceStable(out, func(i, j int) bool { return out[i].LastPlayed > out[j].LastPlayed })
	case "year":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Year > out[j].Year })
	case "year_asc":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Year < out[j].Year })
	case "rating":
		sort.SliceStable(out, func(i, j int) bool { return out[i].Rating > out[j].Rating })
	case "title":
		sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	case "artist", "album":
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if p.Sort == "artist" && !strings.EqualFold(a.AlbumArtist, b.AlbumArtist) {
				return strings.ToLower(a.AlbumArtist) < strings.ToLower(b.AlbumArtist)
			}
			if a.AlbumID != b.AlbumID {
				if a.Year != b.Year {
					return a.Year < b.Year
				}
				return a.Album < b.Album
			}
			if a.Disc != b.Disc {
				return a.Disc < b.Disc
			}
			return a.Track < b.Track
		})
	}
	if p.LimitCount > 0 && len(out) > p.LimitCount {
		out = out[:p.LimitCount]
	}
	if p.LimitMinutes > 0 {
		total := 0
		for i, so := range out {
			total += so.Duration
			if total > p.LimitMinutes*60 {
				out = out[:i]
				break
			}
		}
	}
	return out, nil
}

// refreshSmart writes the playlist to the music server.
func (s *Server) refreshSmart(ctx context.Context, u *store.User, p *store.SmartPlaylist) error {
	songs, err := s.evalSmart(ctx, u.ID, p)
	if err != nil {
		return err
	}
	mc, err := s.musicFor(ctx, u)
	if err != nil {
		_ = s.store.SmartRefreshed(ctx, u.ID, p.ID, "", len(songs), 0, err.Error())
		return err
	}
	ids := make([]string, 0, len(songs))
	secs := 0
	for _, so := range songs {
		ids = append(ids, so.ID)
		secs += so.Duration
	}
	plID := p.PlaylistID
	if plID != "" {
		if _, err := mc.c.Playlist(ctx, plID); subsonic.IsNotFound(err) {
			plID = "" // deleted on the server: make it again
		}
	}
	comment := "Smart playlist from Pocket Music, refreshed " + time.Now().Format("2 Jan 2006")
	if p.Description != "" {
		comment = p.Description + " · " + comment
	}
	name := p.Name
	if plID == "" {
		pl, err := mc.c.CreatePlaylist(ctx, "", name, ids)
		if err != nil {
			_ = s.store.SmartRefreshed(ctx, u.ID, p.ID, "", len(songs), secs, err.Error())
			return err
		}
		plID = pl.ID
	} else if _, err := mc.c.CreatePlaylist(ctx, plID, "", ids); err != nil {
		_ = s.store.SmartRefreshed(ctx, u.ID, p.ID, "", len(songs), secs, err.Error())
		return err
	}
	_ = mc.c.UpdatePlaylist(ctx, plID, subsonic.PlaylistUpdate{Name: &name, Comment: &comment})
	return s.store.SmartRefreshed(ctx, u.ID, p.ID, plID, len(songs), secs, "")
}

// refreshDueSmart refreshes daily smart playlists last built before today (janitor).
func (s *Server) refreshDueSmart(ctx context.Context) {
	owners, err := s.store.SmartOwners(ctx)
	if err != nil {
		return
	}
	today := time.Now().Format("2006-01-02")
	for _, uid := range owners {
		u, err := s.store.GetUser(ctx, uid)
		if err != nil {
			continue
		}
		ps, _ := s.store.ListSmart(ctx, uid)
		for i := range ps {
			p := &ps[i]
			if p.Refresh != "daily" || (p.RefreshedAt != "" && toLocalDay(p.RefreshedAt) == today) {
				continue
			}
			if err := s.refreshSmart(ctx, u, p); err != nil && !isNoMusic(err) {
				s.log.Warn("smart playlist", "user", u.Email, "name", p.Name, "err", err)
			}
		}
	}
}

func toLocalDay(stamp string) string {
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return ""
	}
	return t.Local().Format("2006-01-02")
}

func (s *Server) handleSmartList(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.ListSmart(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "smart playlists")
		return
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) handleSmartGet(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.GetSmart(r.Context(), actorID(r.Context()), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "smart playlist")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleSmartPreview evaluates rules without saving (the editor's live count).
func (s *Server) handleSmartPreview(w http.ResponseWriter, r *http.Request) {
	var p store.SmartPlaylist
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if p.Name == "" {
		p.Name = "preview"
	}
	if err := validSmart(&p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	songs, err := s.evalSmart(r.Context(), actorID(r.Context()), &p)
	if err != nil {
		s.fail(w, err, "preview")
		return
	}
	secs := 0
	for _, so := range songs {
		secs += so.Duration
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(songs), "duration": secs, "songs": head(songs, 50)})
}

func (s *Server) handleSmartSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var p store.SmartPlaylist
	if err := decode(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if id := idParam(r, "id"); id > 0 {
		old, err := s.store.GetSmart(ctx, u.ID, id)
		if err != nil {
			s.fail(w, err, "smart playlist")
			return
		}
		p.ID, p.PlaylistID = old.ID, old.PlaylistID
	} else {
		p.ID = 0
	}
	if err := validSmart(&p); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	created := p.ID == 0
	if err := s.store.SaveSmart(ctx, u.ID, &p); err != nil {
		s.fail(w, err, "save smart playlist")
		return
	}
	if err := s.refreshSmart(ctx, u, &p); err != nil {
		s.log.Warn("smart playlist", "err", err)
	}
	saved, _ := s.store.GetSmart(ctx, u.ID, p.ID)
	if created && saved != nil {
		s.publish(ctx, u, "music.playlist_created", saved.Name, map[string]any{"id": saved.PlaylistID, "name": saved.Name, "smart": true, "songs": saved.SongCount})
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleSmartRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	p, err := s.store.GetSmart(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "smart playlist")
		return
	}
	if err := s.refreshSmart(ctx, u, p); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	p, _ = s.store.GetSmart(ctx, u.ID, p.ID)
	writeJSON(w, http.StatusOK, p)
}

// handleSmartDelete removes the rules and (unless keep=1) the playlist on the server.
func (s *Server) handleSmartDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	p, err := s.store.GetSmart(ctx, u.ID, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "smart playlist")
		return
	}
	if p.PlaylistID != "" && !queryBool(r, "keep") {
		if mc, err := s.musicFor(ctx, u); err == nil {
			_ = mc.c.DeletePlaylist(ctx, p.PlaylistID)
		}
	}
	if err := s.store.DeleteSmart(ctx, u.ID, p.ID); err != nil {
		s.fail(w, err, "delete smart playlist")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
