package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- importing listening history ----
//
// Plays from before Pocket Music come from Maloja (the self-hosted scrobble
// server; its read API is public) or ListenBrainz (a public profile, the
// token only for private ones). Every play is kept, matched to a song of the
// library by artist + title where possible; the rest still count in stats.
// Importing twice adds nothing twice.

func (s *Server) handleImports(w http.ResponseWriter, r *http.Request) {
	is, err := s.store.Imports(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "imports")
		return
	}
	writeJSON(w, http.StatusOK, is)
}

func (s *Server) handleStartImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Source string `json:"source"` // maloja | listenbrainz
		URL    string `json:"url"`
		User   string `json:"user"`
		Token  string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	var run func(ctx context.Context, id int64)
	account := ""
	switch in.Source {
	case "maloja":
		base := strings.TrimRight(firstNonEmpty(strings.TrimSpace(in.URL), st.MalojaURL), "/")
		if base == "" && s.core.Configured() {
			if cred, err := s.core.ServiceFor(ctx, u.Email, "maloja"); err == nil {
				base = strings.TrimRight(cred.URL, "/")
			}
		}
		if base == "" {
			writeErr(w, http.StatusBadRequest, "enter Maloja's address")
			return
		}
		if err := validServerURL(base); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		account = base
		if in.URL != "" && in.URL != st.MalojaURL {
			st.MalojaURL = base
			_ = s.store.SaveUserSettings(ctx, u.ID, st)
		}
		run = func(ctx context.Context, id int64) { s.importMaloja(ctx, u, id, base) }
	case "listenbrainz":
		user := firstNonEmpty(strings.TrimSpace(in.User), st.ListenBrainzUser)
		token := in.Token
		if token == "" || strings.Contains(token, "••") {
			token = st.ListenBrainzToken
		}
		if user == "" {
			writeErr(w, http.StatusBadRequest, "enter your ListenBrainz user name")
			return
		}
		base := strings.TrimRight(firstNonEmpty(st.ListenBrainzURL, "https://api.listenbrainz.org"), "/")
		account = user
		st.ListenBrainzUser, st.ListenBrainzToken = user, token
		_ = s.store.SaveUserSettings(ctx, u.ID, st)
		run = func(ctx context.Context, id int64) { s.importListenBrainz(ctx, u, id, base, user, token) }
	default:
		writeErr(w, http.StatusBadRequest, "source is maloja or listenbrainz")
		return
	}
	id, err := s.store.StartImport(ctx, u.ID, in.Source, account)
	if err != nil {
		s.fail(w, err, "start import")
		return
	}
	if !s.job(fmt.Sprintf("import:%d", u.ID), func(ctx context.Context) { run(ctx, id) }) {
		s.store.ImportProgress(ctx, id, "error", 0, 0, 0, "another import is running")
		writeErr(w, http.StatusConflict, "an import is already running")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": id})
}

// matcher finds library songs by folded artist + title.
type matcher map[string]store.Song

func (s *Server) newMatcher(ctx context.Context, uid int64) matcher {
	m := matcher{}
	songs, _ := s.store.QuerySongs(ctx, uid, "", "", 0)
	for _, so := range songs {
		k := store.MatchKey(so.Artist) + "|" + store.MatchKey(so.Title)
		if _, ok := m[k]; !ok {
			m[k] = so
		}
		if so.AlbumArtist != "" && so.AlbumArtist != so.Artist {
			k2 := store.MatchKey(so.AlbumArtist) + "|" + store.MatchKey(so.Title)
			if _, ok := m[k2]; !ok {
				m[k2] = so
			}
		}
	}
	return m
}

func (m matcher) listen(artist, title, album string, at time.Time, durSec int, source string) (store.Listen, bool) {
	l := store.Listen{Title: title, Artist: artist, Album: album, PlayedAt: store.Stamp(at), Source: source, Duration: durSec}
	so, ok := m[store.MatchKey(artist)+"|"+store.MatchKey(title)]
	if ok {
		l.SongID, l.AlbumID, l.ArtistID, l.Genre = so.ID, so.AlbumID, firstNonEmpty(so.ArtistID, so.AlbumArtistID), so.Genre
		l.Title, l.Artist = so.Title, so.Artist
		if l.Album == "" {
			l.Album = so.Album
		}
		if l.Duration == 0 {
			l.Duration = so.Duration
		}
	}
	return l, ok
}

func (s *Server) getJSON(ctx context.Context, u string, header http.Header, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")
	for k, vs := range header {
		for _, x := range vs {
			req.Header.Add(k, x)
		}
	}
	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return errRateLimited
	}
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("%s: %s", res.Status, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(v)
}

var errRateLimited = fmt.Errorf("rate limited")

func (s *Server) importMaloja(ctx context.Context, u *store.User, id int64, base string) {
	m := s.newMatcher(ctx, u.ID)
	seen, imported, matched := 0, 0, 0
	for page := 0; ; page++ {
		var res struct {
			List []struct {
				Time  int64 `json:"time"`
				Track struct {
					Artists []string `json:"artists"`
					Title   string   `json:"title"`
					Length  *int     `json:"length"`
					Album   *struct {
						Title string `json:"albumtitle"`
					} `json:"album"`
				} `json:"track"`
				Duration *int `json:"duration"`
			} `json:"list"`
		}
		err := s.getJSON(ctx, fmt.Sprintf("%s/apis/mlj_1/scrobbles?perpage=500&page=%d", base, page), nil, &res)
		if err != nil {
			s.store.ImportProgress(ctx, id, "error", seen, imported, matched, "Maloja: "+err.Error())
			return
		}
		var batch []store.Listen
		for _, x := range res.List {
			seen++
			// every name Maloja split the credit into ("AC/DC" comes back as AC, DC);
			// matching to the library puts them back together
			artist := strings.Join(x.Track.Artists, " & ")
			album := ""
			if x.Track.Album != nil {
				album = x.Track.Album.Title
			}
			dur := 0
			if x.Track.Length != nil {
				dur = *x.Track.Length
			}
			l, ok := m.listen(artist, x.Track.Title, album, time.Unix(x.Time, 0), dur, "maloja")
			if x.Duration != nil {
				l.MSPlayed = int64(*x.Duration) * 1000
			}
			if ok {
				matched++
			}
			batch = append(batch, l)
		}
		n, err := s.store.AddListens(ctx, u.ID, batch)
		if err != nil {
			s.store.ImportProgress(ctx, id, "error", seen, imported, matched, err.Error())
			return
		}
		imported += n
		s.store.ImportProgress(ctx, id, "running", seen, imported, matched, "")
		s.hub.send(u.ID, "import", map[string]any{"id": id, "seen": seen, "imported": imported})
		if len(res.List) < 500 || ctx.Err() != nil {
			break
		}
	}
	// plays the quick match above missed (split artists, other spellings): the full matcher
	if n, err := s.store.RelinkListens(ctx, u.ID); err == nil {
		matched += n
	}
	s.store.ImportProgress(ctx, id, "done", seen, imported, matched, "")
	s.hub.send(u.ID, "import", map[string]any{"id": id, "state": "done", "seen": seen, "imported": imported})
	s.log.Info("history imported", "source", "maloja", "user", u.Email, "seen", seen, "new", imported, "matched", matched)
}

func (s *Server) importListenBrainz(ctx context.Context, u *store.User, id int64, base, user, token string) {
	m := s.newMatcher(ctx, u.ID)
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Token "+token)
	}
	seen, imported, matched := 0, 0, 0
	maxTS := time.Now().Unix() + 60
	for {
		var res struct {
			Payload struct {
				Count   int `json:"count"`
				Listens []struct {
					ListenedAt int64 `json:"listened_at"`
					Meta       struct {
						Artist  string `json:"artist_name"`
						Track   string `json:"track_name"`
						Release string `json:"release_name"`
						Info    struct {
							DurationMS int64 `json:"duration_ms"`
							Duration   int   `json:"duration"`
						} `json:"additional_info"`
					} `json:"track_metadata"`
				} `json:"listens"`
			} `json:"payload"`
		}
		err := s.getJSON(ctx, fmt.Sprintf("%s/1/user/%s/listens?count=1000&max_ts=%d", base, url.PathEscape(user), maxTS), h, &res)
		if err == errRateLimited {
			time.Sleep(10 * time.Second)
			continue
		}
		if err != nil {
			s.store.ImportProgress(ctx, id, "error", seen, imported, matched, "ListenBrainz: "+err.Error())
			return
		}
		ls := res.Payload.Listens
		if len(ls) == 0 {
			break
		}
		var batch []store.Listen
		for _, x := range ls {
			seen++
			dur := x.Meta.Info.Duration
			if dur == 0 && x.Meta.Info.DurationMS > 0 {
				dur = int(x.Meta.Info.DurationMS / 1000)
			}
			l, ok := m.listen(x.Meta.Artist, x.Meta.Track, x.Meta.Release, time.Unix(x.ListenedAt, 0), dur, "listenbrainz")
			if ok {
				matched++
			}
			batch = append(batch, l)
			if x.ListenedAt < maxTS {
				maxTS = x.ListenedAt
			}
		}
		n, err := s.store.AddListens(ctx, u.ID, batch)
		if err != nil {
			s.store.ImportProgress(ctx, id, "error", seen, imported, matched, err.Error())
			return
		}
		imported += n
		s.store.ImportProgress(ctx, id, "running", seen, imported, matched, "")
		s.hub.send(u.ID, "import", map[string]any{"id": id, "seen": seen, "imported": imported})
		if ctx.Err() != nil {
			break
		}
		time.Sleep(300 * time.Millisecond) // be polite to the public API
	}
	// plays the quick match above missed (split artists, other spellings): the full matcher
	if n, err := s.store.RelinkListens(ctx, u.ID); err == nil {
		matched += n
	}
	s.store.ImportProgress(ctx, id, "done", seen, imported, matched, "")
	s.hub.send(u.ID, "import", map[string]any{"id": id, "state": "done", "seen": seen, "imported": imported})
	s.log.Info("history imported", "source", "listenbrainz", "user", u.Email, "seen", seen, "new", imported, "matched", matched)
}

// handleExportListens downloads the person's plays as CSV or JSON.
func (s *Server) handleExportListens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	rows, err := s.store.DB.QueryContext(ctx, `SELECT played_at, artist, title, album, ms_played, duration, source, song_id FROM listens WHERE user_id = ? ORDER BY played_at`, uid)
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	defer rows.Close()
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="pocket-music-listens.json"`)
		enc := json.NewEncoder(w)
		_, _ = w.Write([]byte("["))
		first := true
		for rows.Next() {
			var at, artist, title, album, source, song string
			var ms int64
			var dur int
			if rows.Scan(&at, &artist, &title, &album, &ms, &dur, &source, &song) != nil {
				continue
			}
			if !first {
				_, _ = w.Write([]byte(","))
			}
			first = false
			_ = enc.Encode(map[string]any{"played_at": at, "artist": artist, "title": title, "album": album, "ms_played": ms, "duration": dur, "source": source, "song_id": song})
		}
		_, _ = w.Write([]byte("]"))
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-music-listens.csv"`)
	_, _ = w.Write([]byte("played_at,artist,title,album,seconds_played,duration,source\n"))
	q := func(v string) string { return `"` + strings.ReplaceAll(v, `"`, `""`) + `"` }
	for rows.Next() {
		var at, artist, title, album, source, song string
		var ms int64
		var dur int
		if rows.Scan(&at, &artist, &title, &album, &ms, &dur, &source, &song) != nil {
			continue
		}
		_, _ = w.Write([]byte(strings.Join([]string{at, q(artist), q(title), q(album), strconv.FormatInt(ms/1000, 10), strconv.Itoa(dur), source}, ",") + "\n"))
	}
}
