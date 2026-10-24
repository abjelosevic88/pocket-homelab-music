package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhowden/tag"
	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- the lyrics library ----
//
// Box-wide and for admins: every song in the music folder without a .lrc file
// next to it is looked up at LRCLIB (artist, title, album and length from its
// tags, then once more without the album), and synced lyrics are written as
// <song>.lrc beside the file, so the music server and every player show them.
// Lyrics without timings go into the FLAC's UNSYNCEDLYRICS tag, an untimed
// .lrc, or nowhere — a setting. Songs LRCLIB doesn't know are asked again
// only after some days; a failing LRCLIB (503, the network) never counts as
// "not found".
//
// It walks MUSIC_DIR itself (the folder the music server reads, mounted
// read-write) rather than asking the server for paths: Subsonic servers
// report made-up paths unless told otherwise, and the walk works before
// anyone has connected an account.

var lyricsAudio = map[string]bool{".flac": true, ".mp3": true, ".m4a": true, ".opus": true, ".ogg": true, ".wav": true}

type lyricsJob struct {
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

type lyricsRunRow struct {
	ID         int64  `json:"id"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	State      string `json:"state"`
	Trigger    string `json:"trigger"`
	Total      int    `json:"total"`
	Checked    int    `json:"checked"`
	Have       int    `json:"have"`
	Fetched    int    `json:"fetched"`
	Plain      int    `json:"plain"`
	NotFound   int    `json:"not_found"`
	Backoff    int    `json:"backoff"`
	Missing    int    `json:"missing"` // files without artist or title tags
	Errors     int    `json:"errors"`
	Note       string `json:"note"`
}

func (s *Server) lyricsRuns(ctx context.Context, n int) []lyricsRunRow {
	rows, err := s.store.DB.QueryContext(ctx, `SELECT id, started_at, finished_at, state, trigger, total, checked, have, fetched, plain, not_found, backoff, missing, errors, note
		FROM lyrics_runs ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return []lyricsRunRow{}
	}
	defer rows.Close()
	out := []lyricsRunRow{}
	for rows.Next() {
		var r lyricsRunRow
		if rows.Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.State, &r.Trigger, &r.Total, &r.Checked, &r.Have, &r.Fetched, &r.Plain, &r.NotFound, &r.Backoff, &r.Missing, &r.Errors, &r.Note) == nil {
			out = append(out, r)
		}
	}
	return out
}

// lyricsWritable says whether the library can be written, and why not.
func (s *Server) lyricsWritable() (bool, string) {
	if s.cfg.MusicDir == "" {
		return false, "mount the music folder read-write and set MUSIC_DIR (the folder the music server reads)"
	}
	fi, err := os.Stat(s.cfg.MusicDir)
	if err != nil || !fi.IsDir() {
		return false, "MUSIC_DIR (" + s.cfg.MusicDir + ") is not a folder this app can see"
	}
	probe, err := os.CreateTemp(s.cfg.MusicDir, ".pocket-music-write-test-*")
	if err != nil {
		return false, "MUSIC_DIR is mounted read-only: lyrics can't be saved next to the songs"
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	if s.cfg.LyricsURL == "" {
		return false, "online lyrics are switched off on this server (LYRICS_URL=off)"
	}
	return true, ""
}

// inMusicDir maps a path relative to MUSIC_DIR to the file, never outside it.
func (s *Server) inMusicDir(rel string) (string, bool) {
	if s.cfg.MusicDir == "" {
		return "", false
	}
	root := filepath.Clean(s.cfg.MusicDir)
	p := filepath.Join(root, filepath.Clean("/"+rel))
	if p != root && !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", false
	}
	return p, true
}

func (s *Server) relToMusicDir(p string) string {
	rel, err := filepath.Rel(filepath.Clean(s.cfg.MusicDir), p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

func lrcPath(file string) string { return strings.TrimSuffix(file, filepath.Ext(file)) + ".lrc" }

// lyricsCandidate is a song file without a .lrc beside it.
type lyricsCandidate struct {
	path string
	mod  time.Time
}

// lyricsWalk lists the audio files under root, counting those that already
// have a .lrc and returning the others.
func lyricsWalk(ctx context.Context, root string) (todo []lyricsCandidate, have int, err error) {
	var walk func(dir string) error
	walk = func(dir string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil // an unreadable folder is skipped, not fatal
		}
		names := make(map[string]bool, len(ents))
		for _, e := range ents {
			names[strings.ToLower(e.Name())] = true
		}
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			p := filepath.Join(dir, name)
			if e.IsDir() {
				if err := walk(p); err != nil {
					return err
				}
				continue
			}
			ext := strings.ToLower(filepath.Ext(name))
			if !lyricsAudio[ext] || !e.Type().IsRegular() {
				continue
			}
			if names[strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))+".lrc")] {
				have++
				continue
			}
			c := lyricsCandidate{path: p}
			if fi, err := e.Info(); err == nil {
				c.mod = fi.ModTime()
			}
			todo = append(todo, c)
		}
		return nil
	}
	err = walk(root)
	return
}

type songTags struct {
	artist, title, album string
	duration             int
}

func readSongTags(p string) (songTags, error) {
	f, err := os.Open(p)
	if err != nil {
		return songTags{}, err
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return songTags{}, err
	}
	t := songTags{artist: strings.TrimSpace(m.Artist()), title: strings.TrimSpace(m.Title()), album: strings.TrimSpace(m.Album())}
	if t.artist == "" {
		t.artist = strings.TrimSpace(m.AlbumArtist())
	}
	if strings.EqualFold(filepath.Ext(p), ".flac") {
		t.duration = flacDuration(p)
	}
	return t, nil
}

// lrclibRaw asks LRCLIB for a song: synced and plain text, found or not.
// An error means LRCLIB itself failed (try again later).
func (s *Server) lrclibRaw(ctx context.Context, t songTags) (synced, plain string, err error) {
	once := func(withAlbum bool) (string, string, error) {
		q := url.Values{"artist_name": {t.artist}, "track_name": {t.title}}
		if withAlbum && t.album != "" {
			q.Set("album_name", t.album)
		}
		if t.duration > 0 {
			q.Set("duration", strconv.Itoa(t.duration))
		}
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, s.cfg.LyricsURL+"/api/get?"+q.Encode(), nil)
		req.Header.Set("User-Agent", s.cfg.UserAgent)
		res, err := s.http.Do(req)
		if err != nil {
			return "", "", err
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusNotFound {
			return "", "", nil
		}
		if res.StatusCode != http.StatusOK {
			return "", "", fmt.Errorf("LRCLIB answered %s", res.Status)
		}
		var hit struct {
			Synced string `json:"syncedLyrics"`
			Plain  string `json:"plainLyrics"`
		}
		if err := json.NewDecoder(res.Body).Decode(&hit); err != nil {
			return "", "", err
		}
		return hit.Synced, hit.Plain, nil
	}
	// LRCLIB answers 503 now and then under load: try again shortly before giving up
	ask := func(withAlbum bool) (string, string, error) {
		var err error
		for try := 0; try < 3; try++ {
			var a, b string
			if a, b, err = once(withAlbum); err == nil {
				return a, b, nil
			}
			sleepCtx(ctx, time.Duration(try+1)*2*time.Second)
		}
		return "", "", err
	}
	if synced, plain, err = ask(true); err != nil || synced != "" || t.album == "" {
		return
	}
	// the album name is the usual mismatch
	s2, p2, err2 := ask(false)
	if err2 != nil {
		return synced, plain, nil
	}
	return s2, firstNonEmpty(plain, p2), nil
}

// lyricsTally is what one pass over a set of files did.
type lyricsTally struct {
	lyricsRunRow
	errStreak int
}

// lyricsOne handles one song file without a .lrc: asks LRCLIB and writes.
// It returns an error only when LRCLIB failed (nothing was recorded).
func (s *Server) lyricsOne(ctx context.Context, box store.Settings, file string, t *lyricsTally) error {
	tags, err := readSongTags(file)
	if err != nil || tags.artist == "" || tags.title == "" {
		t.Missing++
		return nil
	}
	synced, plain, err := s.lrclibRaw(ctx, tags)
	if err != nil {
		t.Errors++
		return err
	}
	result := "none"
	switch {
	case synced != "":
		if err := os.WriteFile(lrcPath(file), []byte(synced), 0o644); err != nil {
			t.Errors++
			t.Note = "can't write next to the songs: " + err.Error()
			return nil
		}
		t.Fetched++
		result = "synced"
	case plain != "":
		result = "plain"
		switch box.LyricsPlain {
		case "lrc":
			if err := os.WriteFile(lrcPath(file), []byte(plain), 0o644); err != nil {
				t.Errors++
				return nil
			}
			t.Plain++
		case "tag":
			if strings.EqualFold(filepath.Ext(file), ".flac") {
				if added, err := flacAddTag(file, "UNSYNCEDLYRICS", plain); err != nil {
					t.Errors++
					t.Note = filepath.Base(file) + ": " + err.Error()
				} else if added {
					t.Plain++
				}
			}
		}
	default:
		t.NotFound++
	}
	_, _ = s.store.DB.Exec(`INSERT INTO lyrics_attempts (path, tried_at, result) VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET tried_at = excluded.tried_at, result = excluded.result`, s.relToMusicDir(file), store.Now(), result)
	return nil
}

// lyricsPass goes through root's song files without lyrics. save is called
// now and then with the tally so far.
func (s *Server) lyricsPass(ctx context.Context, root string, t *lyricsTally, save func()) error {
	box, _ := s.store.GetSettings(ctx, 0)
	todo, have, err := lyricsWalk(ctx, root)
	if err != nil {
		return err
	}
	t.Have += have
	t.Total += have + len(todo)
	if box.LyricsOrder == "path" {
		sort.Slice(todo, func(i, j int) bool { return todo[i].path < todo[j].path })
	} else {
		sort.SliceStable(todo, func(i, j int) bool { return todo[i].mod.After(todo[j].mod) })
	}
	tried := map[string]time.Time{}
	if rows, err := s.store.DB.QueryContext(ctx, `SELECT path, tried_at FROM lyrics_attempts`); err == nil {
		for rows.Next() {
			var p, at string
			if rows.Scan(&p, &at) == nil {
				if ts, err := time.Parse(time.RFC3339Nano, at); err == nil {
					tried[p] = ts
				}
			}
		}
		rows.Close()
	}
	retry := time.Duration(max(1, box.LyricsRetryDays)) * 24 * time.Hour
	delay := time.Duration(max(0, box.LyricsDelayMS)) * time.Millisecond
	t.Checked += have
	last := time.Now()
	for _, c := range todo {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t.Checked++
		if save != nil && time.Since(last) > 3*time.Second {
			save()
			last = time.Now()
		}
		if at, ok := tried[s.relToMusicDir(c.path)]; ok && time.Since(at) < retry {
			t.Backoff++
			continue
		}
		if err := s.lyricsOne(ctx, box, c.path, t); err != nil {
			t.Note = "LRCLIB: " + err.Error()
			t.errStreak++
			if t.errStreak >= 8 {
				return fmt.Errorf("LRCLIB keeps failing (%v); the next run carries on", err)
			}
			sleepCtx(ctx, 5*time.Second)
			continue
		}
		t.errStreak = 0
		sleepCtx(ctx, delay)
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// startLyricsRun begins a whole-library run in the background unless one is going.
func (s *Server) startLyricsRun(trigger string) error {
	s.lyr.mu.Lock()
	defer s.lyr.mu.Unlock()
	if s.lyr.running {
		return errors.New("a run is already going")
	}
	if ok, why := s.lyricsWritable(); !ok {
		return errors.New(why)
	}
	res, err := s.store.DB.Exec(`INSERT INTO lyrics_runs (started_at, trigger) VALUES (?, ?)`, store.Now(), trigger)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
	s.lyr.running, s.lyr.cancel = true, cancel
	go func() {
		defer func() {
			cancel()
			s.lyr.mu.Lock()
			s.lyr.running, s.lyr.cancel = false, nil
			s.lyr.mu.Unlock()
		}()
		s.runLyrics(ctx, id, trigger)
	}()
	return nil
}

func (s *Server) saveLyricsRun(id int64, t *lyricsTally) {
	finished := ""
	if t.State != "running" {
		finished = store.Now()
	}
	_, _ = s.store.DB.Exec(`UPDATE lyrics_runs SET state = ?, total = ?, checked = ?, have = ?, fetched = ?, plain = ?, not_found = ?, backoff = ?, missing = ?, errors = ?, note = ?, finished_at = ? WHERE id = ?`,
		t.State, t.Total, t.Checked, t.Have, t.Fetched, t.Plain, t.NotFound, t.Backoff, t.Missing, t.Errors, t.Note, finished, id)
}

func (s *Server) runLyrics(ctx context.Context, id int64, trigger string) {
	t := &lyricsTally{}
	t.State = "running"
	s.saveLyricsRun(id, t)
	err := s.lyricsPass(ctx, s.cfg.MusicDir, t, func() { s.saveLyricsRun(id, t) })
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		t.State, t.Note = "stopped", "stopped before the end; the next run carries on"
	case err != nil:
		t.State, t.Note = "error", err.Error()
	default:
		t.State = "done"
	}
	s.saveLyricsRun(id, t)
	s.log.Info("lyrics library run", "trigger", trigger, "state", t.State, "fetched", t.Fetched, "plain", t.Plain, "not_found", t.NotFound, "backoff", t.Backoff, "have", t.Have, "errors", t.Errors)
	s.afterLyrics(context.Background(), t, trigger)
}

// afterLyrics asks the music server to scan and tells the owner, as set.
func (s *Server) afterLyrics(ctx context.Context, t *lyricsTally, trigger string) {
	box, _ := s.store.GetSettings(ctx, 0)
	if t.Fetched+t.Plain == 0 && t.State != "error" {
		return
	}
	owner, err := s.store.GetUser(ctx, s.store.OwnerID(ctx))
	if err != nil {
		return
	}
	if box.LyricsRescan && t.Fetched+t.Plain > 0 {
		if mc, err := s.musicFor(ctx, owner); err == nil {
			_ = mc.c.StartScan(ctx)
		}
	}
	if !box.LyricsNotify || trigger == "folder" {
		return
	}
	st, _ := s.store.GetSettings(ctx, owner.ID)
	if t.State == "error" {
		_ = s.notifyUser(ctx, st, "Lyrics run failed", t.Note, "warning")
		return
	}
	body := fmt.Sprintf("%d songs got synced lyrics", t.Fetched)
	if t.Plain > 0 {
		body += fmt.Sprintf(", %d got plain ones", t.Plain)
	}
	body += fmt.Sprintf(". %d not found yet, %d wait to be asked again.", t.NotFound, t.Backoff)
	_ = s.notifyUser(ctx, st, "Lyrics added", body, "microphone")
}

// scheduledLyrics starts the nightly run at the chosen hour (janitor, every 15 minutes).
func (s *Server) scheduledLyrics(ctx context.Context) {
	box, _ := s.store.GetSettings(ctx, 0)
	if !box.LyricsWriter || time.Now().Hour() != box.LyricsHour {
		return
	}
	day := time.Now().Format("2006-01-02")
	if v, _, ok := s.store.CacheGet(ctx, "lyrics:last-scheduled"); ok && v == day {
		return
	}
	_ = s.store.CachePut(ctx, "lyrics:last-scheduled", day)
	if err := s.startLyricsRun("schedule"); err != nil {
		s.log.Warn("lyrics library: nightly run not started", "err", err)
	}
}

// ---- API (admins) ----

func (s *Server) lyricsLibraryInfo(ctx context.Context) map[string]any {
	ok, why := s.lyricsWritable()
	box, _ := s.store.GetSettings(ctx, 0)
	s.lyr.mu.Lock()
	running := s.lyr.running
	s.lyr.mu.Unlock()
	var attempts, synced int
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(CASE WHEN result = 'synced' THEN 1 ELSE 0 END), 0) FROM lyrics_attempts`).Scan(&attempts, &synced)
	return map[string]any{"writable": ok, "detail": why, "music_dir": s.cfg.MusicDir, "running": running,
		"enabled": box.LyricsWriter, "attempts": attempts, "synced_total": synced, "runs": s.lyricsRuns(ctx, 10)}
}

func (s *Server) handleLyricsLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.lyricsLibraryInfo(r.Context()))
}

// handleLyricsLibraryRun starts a whole-library run, or with {"folder": "…"}
// (relative to the music folder) does just that folder now and answers with
// what it did — for download hooks.
func (s *Server) handleLyricsLibraryRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Folder string `json:"folder"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "bad request")
			return
		}
	}
	if in.Folder == "" {
		if err := s.startLyricsRun("manual"); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, s.lyricsLibraryInfo(r.Context()))
		return
	}
	if ok, why := s.lyricsWritable(); !ok {
		writeErr(w, http.StatusConflict, why)
		return
	}
	dir, ok := s.inMusicDir(in.Folder)
	if fi, err := os.Stat(dir); !ok || err != nil || !fi.IsDir() {
		writeErr(w, http.StatusNotFound, "no such folder in the music folder")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	t := &lyricsTally{}
	err := s.lyricsPass(ctx, dir, t, nil)
	t.State, t.Trigger = "done", "folder"
	if err != nil {
		t.State, t.Note = "error", err.Error()
	}
	if t.Fetched+t.Plain+t.NotFound+t.Errors > 0 {
		if res, err := s.store.DB.Exec(`INSERT INTO lyrics_runs (started_at, trigger) VALUES (?, 'folder')`, store.Now()); err == nil {
			id, _ := res.LastInsertId()
			t.Note = strings.TrimSuffix(in.Folder+" · "+t.Note, " · ")
			s.saveLyricsRun(id, t)
		}
	}
	s.afterLyrics(context.Background(), t, "folder")
	writeJSON(w, http.StatusOK, t.lyricsRunRow)
}

func (s *Server) handleLyricsLibraryStop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.lyr.mu.Lock()
	if s.lyr.cancel != nil {
		s.lyr.cancel()
	}
	s.lyr.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// songFileFor finds a song's file under MUSIC_DIR: its reported path when the
// server reports real ones, else by its album folder and title.
func (s *Server) songFileFor(so store.Song) (string, bool) {
	if s.cfg.MusicDir == "" {
		return "", false
	}
	if so.Path != "" {
		parts := strings.Split(strings.TrimLeft(filepath.ToSlash(so.Path), "/"), "/")
		for i := 0; i < len(parts) && i < 4; i++ { // absolute or library-relative
			if p, ok := s.inMusicDir(strings.Join(parts[i:], "/")); ok {
				if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
					return p, true
				}
			}
		}
	}
	// Artist/<…Album…>/<…Title…>.<suffix> (one folder deeper for discs)
	title := strings.ToLower(so.Title)
	album := strings.ToLower(so.Album)
	if title == "" || album == "" {
		return "", false
	}
	var found []string
	for _, artist := range []string{so.AlbumArtist, so.Artist} {
		if artist == "" {
			continue
		}
		base, ok := s.inMusicDir(artist)
		if !ok {
			continue
		}
		dirs, _ := os.ReadDir(base)
		for _, d := range dirs {
			if !d.IsDir() || !strings.Contains(strings.ToLower(d.Name()), album) {
				continue
			}
			_ = filepath.WalkDir(filepath.Join(base, d.Name()), func(p string, e os.DirEntry, err error) error {
				if err != nil || e.IsDir() {
					return nil
				}
				n := strings.ToLower(e.Name())
				if strings.Contains(strings.TrimSuffix(n, filepath.Ext(n)), title) && (so.Suffix == "" || strings.EqualFold(strings.TrimPrefix(filepath.Ext(n), "."), so.Suffix)) {
					found = append(found, p)
				}
				return nil
			})
		}
		if len(found) > 0 {
			break
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// handleLyricsSave writes one song's lyrics (from LRCLIB) next to it now (admins).
func (s *Server) handleLyricsSave(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	if ok, why := s.lyricsWritable(); !ok {
		writeErr(w, http.StatusConflict, why)
		return
	}
	songs, err := s.songsFor(ctx, actorID(ctx), []string{chi.URLParam(r, "id")})
	if err != nil || len(songs) == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	so := songs[0]
	file, ok := s.songFileFor(so)
	if !ok {
		writeErr(w, http.StatusNotFound, "can't find this song's file in the music folder")
		return
	}
	tags, err := readSongTags(file)
	if err != nil || tags.artist == "" || tags.title == "" {
		tags = songTags{artist: so.Artist, title: so.Title, album: so.Album, duration: so.Duration}
	}
	synced, plain, err := s.lrclibRaw(ctx, tags)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	text := firstNonEmpty(synced, plain)
	if text == "" {
		writeErr(w, http.StatusNotFound, "LRCLIB has no lyrics for this song")
		return
	}
	if err := os.WriteFile(lrcPath(file), []byte(text), 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, "can't write next to the song: "+err.Error())
		return
	}
	result := "plain"
	if synced != "" {
		result = "synced"
	}
	_, _ = s.store.DB.ExecContext(ctx, `INSERT INTO lyrics_attempts (path, tried_at, result) VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET tried_at = excluded.tried_at, result = excluded.result`, s.relToMusicDir(file), store.Now(), result)
	if box, _ := s.store.GetSettings(ctx, 0); box.LyricsRescan {
		if mc, err := s.musicFor(ctx, userFrom(ctx)); err == nil {
			_ = mc.c.StartScan(ctx)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "synced": synced != "", "file": s.relToMusicDir(lrcPath(file))})
}

// ImportLyricsState brings in the attempts of the old fetch-lyrics.py
// (state.json: absolute path → unix time), with prefix the music folder it
// used, so songs it already asked about are not asked again at once.
func ImportLyricsState(ctx context.Context, st *store.Store, stateFile, prefix string) (int, error) {
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		return 0, err
	}
	var m map[string]float64
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, err
	}
	prefix = strings.TrimRight(prefix, "/") + "/"
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	n := 0
	for p, ts := range m {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		at := time.Unix(int64(ts), 0).UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT INTO lyrics_attempts (path, tried_at, result) VALUES (?, ?, 'none') ON CONFLICT(path) DO NOTHING`, strings.TrimPrefix(p, prefix), at); err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		n++
	}
	return n, tx.Commit()
}
