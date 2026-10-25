package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhowden/tag"
	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- the lyrics index: every song's lyrics in one place ----
//
// The Lyrics page lists every song file in the music folder with the lyrics
// it has — a .lrc beside it, else lyrics in its own tags — searchable by
// their words. The index walks MUSIC_DIR (unchanged files are not read
// again), after every lyrics run and once a day. Admins edit, fetch, shift,
// tap-sync and delete lyrics there; the files are what changes, so Navidrome
// and every app see the result. Members see the songs their own library has.

type lyricsIndexJob struct {
	mu      sync.Mutex
	running bool
	done    int
	total   int
}

// lyricsEntry is one song file found by the walk.
type lyricsEntry struct {
	abs, rel  string
	size, mod int64
	lrc       string // the .lrc beside it ("" none), any case
	lrcMod    int64
}

// lyricsInfo is what the index keeps about a song file's lyrics.
type lyricsInfo struct {
	artist, title, album string
	duration             int
	kind, source         string // synced | plain | none · lrc | tag | ""
	lines                int
	words                string
}

// lyricsScan lists the song files under dir (with their .lrc, if any).
func (s *Server) lyricsScan(ctx context.Context, dir string) ([]lyricsEntry, error) {
	var out []lyricsEntry
	var walk func(dir string) error
	walk = func(dir string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			return nil // an unreadable folder is skipped, not fatal
		}
		lrcs := map[string]os.DirEntry{}
		for _, e := range ents {
			if strings.EqualFold(filepath.Ext(e.Name()), ".lrc") {
				lrcs[strings.ToLower(strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))] = e
			}
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
			if !lyricsAudio[strings.ToLower(filepath.Ext(name))] || !e.Type().IsRegular() {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			en := lyricsEntry{abs: p, rel: s.relToMusicDir(p), size: fi.Size(), mod: fi.ModTime().UnixNano()}
			if l, ok := lrcs[strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))]; ok {
				if li, err := l.Info(); err == nil {
					en.lrc, en.lrcMod = filepath.Join(dir, l.Name()), li.ModTime().UnixNano()
				}
			}
			out = append(out, en)
		}
		return nil
	}
	err := walk(dir)
	return out, err
}

// lyricsEntryFor is the walk's entry for one song file (ok false: it's gone).
func (s *Server) lyricsEntryFor(file string) (lyricsEntry, bool) {
	fi, err := os.Stat(file)
	if err != nil || !fi.Mode().IsRegular() {
		return lyricsEntry{}, false
	}
	en := lyricsEntry{abs: file, rel: s.relToMusicDir(file), size: fi.Size(), mod: fi.ModTime().UnixNano()}
	if l := existingLrc(file); l != "" {
		if li, err := os.Stat(l); err == nil {
			en.lrc, en.lrcMod = l, li.ModTime().UnixNano()
		}
	}
	return en, true
}

// existingLrc is the .lrc beside a song file, whatever the case of its name.
func existingLrc(file string) string {
	want := strings.ToLower(strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)) + ".lrc")
	if _, err := os.Stat(lrcPath(file)); err == nil {
		return lrcPath(file)
	}
	ents, _ := os.ReadDir(filepath.Dir(file))
	for _, e := range ents {
		if strings.ToLower(e.Name()) == want {
			return filepath.Join(filepath.Dir(file), e.Name())
		}
	}
	return ""
}

// fileTags reads a song file's artist, title, album, length and the lyrics in
// its own tags (raw text, "" none).
func fileTags(file string) (songTags, string) {
	if strings.EqualFold(filepath.Ext(file), ".flac") {
		m, dur, err := flacMeta(file)
		if err != nil && len(m) == 0 {
			return songTags{}, ""
		}
		t := songTags{artist: strings.TrimSpace(m["ARTIST"]), title: strings.TrimSpace(m["TITLE"]), album: strings.TrimSpace(m["ALBUM"]), duration: dur}
		if t.artist == "" {
			t.artist = strings.TrimSpace(m["ALBUMARTIST"])
		}
		return t, firstNonEmpty(m["LYRICS"], m["UNSYNCEDLYRICS"])
	}
	f, err := os.Open(file)
	if err != nil {
		return songTags{}, ""
	}
	defer f.Close()
	m, err := tag.ReadFrom(f)
	if err != nil {
		return songTags{}, ""
	}
	t := songTags{artist: strings.TrimSpace(m.Artist()), title: strings.TrimSpace(m.Title()), album: strings.TrimSpace(m.Album())}
	if t.artist == "" {
		t.artist = strings.TrimSpace(m.AlbumArtist())
	}
	lyr := m.Lyrics()
	if lyr == "" {
		if v, ok := m.Raw()["unsyncedlyrics"].(string); ok {
			lyr = v
		}
	}
	return t, lyr
}

var trackPrefix = regexp.MustCompile(`^\d{1,3}(?:[.-]\d{1,3})?\s*[.\-_)]?\s+`)

// readLyricsInfo reads what the index keeps about one song file.
func readLyricsInfo(en lyricsEntry) lyricsInfo {
	t, embedded := fileTags(en.abs)
	in := lyricsInfo{artist: t.artist, title: t.title, album: t.album, duration: t.duration, kind: "none"}
	// files without tags still get a name: Artist/Album/NN. Title.ext
	parts := strings.Split(en.rel, "/")
	if in.title == "" {
		in.title = trackPrefix.ReplaceAllString(strings.TrimSuffix(parts[len(parts)-1], filepath.Ext(en.rel)), "")
	}
	if in.artist == "" && len(parts) > 1 {
		in.artist = parts[0]
	}
	if in.album == "" && len(parts) > 2 {
		in.album = parts[len(parts)-2]
	}
	text, source := "", ""
	if en.lrc != "" {
		if b, err := readCapped(en.lrc, 1<<20); err == nil && strings.TrimSpace(string(b)) != "" {
			text, source = string(b), "lrc"
		}
	}
	if text == "" && strings.TrimSpace(embedded) != "" {
		text, source = embedded, "tag"
	}
	if text != "" {
		lines, synced := parseLRC(text)
		words := make([]string, 0, len(lines))
		for _, l := range lines {
			if l.Text != "" {
				words = append(words, l.Text)
			}
		}
		if len(words) > 0 {
			in.source, in.lines, in.words, in.kind = source, len(words), strings.Join(words, "\n"), "plain"
			if synced {
				in.kind = "synced"
			}
		}
	}
	return in
}

// readLyricsInfos reads a batch a few files at a time: over a network share
// each file is a handful of round trips, so waiting on one at a time is slow.
func readLyricsInfos(batch []lyricsEntry) []lyricsInfo {
	infos := make([]lyricsInfo, len(batch))
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				infos[i] = readLyricsInfo(batch[i])
			}
		}()
	}
	for i := range batch {
		next <- i
	}
	close(next)
	wg.Wait()
	return infos
}

func readCapped(p string, n int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

// lyricsChangedAt is when a file's lyrics last changed, as far as the folder
// tells: the .lrc's time, or the song file's when they live in its tags.
func lyricsChangedAt(en lyricsEntry, in lyricsInfo) string {
	switch in.source {
	case "lrc":
		return time.Unix(0, en.lrcMod).UTC().Format(time.RFC3339Nano)
	case "tag":
		return time.Unix(0, en.mod).UTC().Format(time.RFC3339Nano)
	}
	return ""
}

func lyricsIndexPut(ctx context.Context, tx *sql.Tx, en lyricsEntry, in lyricsInfo) error {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO lyrics_files (path, size, mod, lrc_mod, artist, title, album, duration, kind, source, lines, changed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET size = excluded.size, mod = excluded.mod, lrc_mod = excluded.lrc_mod, artist = excluded.artist,
			title = excluded.title, album = excluded.album, duration = excluded.duration, kind = excluded.kind, source = excluded.source,
			lines = excluded.lines, changed_at = excluded.changed_at
		RETURNING id`, en.rel, en.size, en.mod, en.lrcMod, in.artist, in.title, in.album, in.duration, in.kind, in.source, in.lines, lyricsChangedAt(en, in)).Scan(&id)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM lyrics_fts WHERE rowid = ?`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO lyrics_fts (rowid, artist, title, album, words) VALUES (?, ?, ?, ?, ?)`, id, in.artist, in.title, in.album, in.words)
	return err
}

func lyricsIndexDrop(ctx context.Context, tx *sql.Tx, id int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM lyrics_fts WHERE rowid = ?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM lyrics_files WHERE id = ?`, id)
	return err
}

// lyricsIndex brings the index up to date for dir (the music folder or one
// folder in it): new and changed files are read, gone ones dropped.
func (s *Server) lyricsIndex(ctx context.Context, dir string) (read, dropped int, err error) {
	type known struct {
		id                int64
		size, mod, lrcMod int64
	}
	prefix := s.relToMusicDir(dir)
	whole := filepath.Clean(dir) == filepath.Clean(s.cfg.MusicDir)
	have := map[string]known{}
	q, args := `SELECT id, path, size, mod, lrc_mod FROM lyrics_files`, []any{}
	if !whole {
		q += ` WHERE substr(path, 1, ?) = ?`
		args = append(args, len(prefix)+1, prefix+"/")
	}
	rows, err := s.store.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var k known
		var p string
		if rows.Scan(&k.id, &p, &k.size, &k.mod, &k.lrcMod) == nil {
			have[p] = k
		}
	}
	rows.Close()

	found, err := s.lyricsScan(ctx, dir)
	if err != nil {
		return 0, 0, err
	}
	var todo []lyricsEntry
	for _, en := range found {
		k, ok := have[en.rel]
		delete(have, en.rel)
		if ok && k.size == en.size && k.mod == en.mod && k.lrcMod == en.lrcMod {
			continue
		}
		todo = append(todo, en)
	}
	if whole {
		s.lix.mu.Lock()
		s.lix.done, s.lix.total = 0, len(todo)
		s.lix.mu.Unlock()
	}
	// read a batch from the folder, then write it in one short transaction
	for start := 0; start < len(todo); start += 200 {
		if ctx.Err() != nil {
			return read, dropped, ctx.Err()
		}
		batch := todo[start:min(len(todo), start+200)]
		infos := readLyricsInfos(batch)
		tx, err := s.store.DB.BeginTx(ctx, nil)
		if err != nil {
			return read, dropped, err
		}
		for i, en := range batch {
			if err := lyricsIndexPut(ctx, tx, en, infos[i]); err != nil {
				_ = tx.Rollback()
				return read, dropped, err
			}
		}
		if err := tx.Commit(); err != nil {
			return read, dropped, err
		}
		read += len(batch)
		if whole {
			s.lix.mu.Lock()
			s.lix.done = read
			s.lix.mu.Unlock()
			s.liveLyricsJob("index", "running", read, len(todo))
		}
	}
	if len(have) > 0 {
		tx, err := s.store.DB.BeginTx(ctx, nil)
		if err != nil {
			return read, dropped, err
		}
		for _, k := range have {
			if err := lyricsIndexDrop(ctx, tx, k.id); err != nil {
				_ = tx.Rollback()
				return read, dropped, err
			}
			dropped++
		}
		if err := tx.Commit(); err != nil {
			return read, dropped, err
		}
	}
	if whole {
		_ = s.store.CachePut(ctx, "lyrics:indexed-at", store.Now())
	}
	return read, dropped, nil
}

// lyricsIndexFile updates the index for one song file (after a write).
func (s *Server) lyricsIndexFile(ctx context.Context, file string) {
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	var id int64
	rel := s.relToMusicDir(file)
	if en, ok := s.lyricsEntryFor(file); ok {
		in := readLyricsInfo(en)
		err = lyricsIndexPut(ctx, tx, en, in)
		if err == nil {
			_ = tx.QueryRowContext(ctx, `SELECT id FROM lyrics_files WHERE path = ?`, rel).Scan(&id)
		}
	} else if tx.QueryRowContext(ctx, `SELECT id FROM lyrics_files WHERE path = ?`, rel).Scan(&id) == nil {
		err = lyricsIndexDrop(ctx, tx, id)
	}
	if err != nil {
		_ = tx.Rollback()
		s.log.Warn("lyrics index", "file", file, "err", err)
		return
	}
	if tx.Commit() == nil {
		s.liveLyrics(id)
	}
}

// startLyricsIndex refreshes the whole index in the background.
func (s *Server) startLyricsIndex() bool {
	if s.cfg.MusicDir == "" {
		return false
	}
	if fi, err := os.Stat(s.cfg.MusicDir); err != nil || !fi.IsDir() {
		return false
	}
	return s.job("lyrics-index", func(ctx context.Context) {
		s.lix.mu.Lock()
		s.lix.running, s.lix.done, s.lix.total = true, 0, 0
		s.lix.mu.Unlock()
		state, read := "done", 0
		// told last, once the status no longer says running
		defer func() { s.liveLyricsJob("index", state, read, read) }()
		defer func() {
			s.lix.mu.Lock()
			s.lix.running = false
			s.lix.mu.Unlock()
		}()
		s.liveLyricsJob("index", "running", 0, 0)
		t0 := time.Now()
		read, dropped, err := s.lyricsIndex(ctx, s.cfg.MusicDir)
		if err != nil {
			state = "error"
			s.log.Warn("lyrics index", "err", err)
			return
		}
		s.log.Info("lyrics index", "read", read, "dropped", dropped, "took", time.Since(t0).Round(time.Second))
	})
}

// scheduledLyricsIndex keeps the index fresh: at start and once a day (janitor).
func (s *Server) scheduledLyricsIndex(ctx context.Context) {
	if v, _, ok := s.store.CacheGet(ctx, "lyrics:indexed-at"); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil && time.Since(t) < 20*time.Hour {
			return
		}
	}
	s.startLyricsIndex()
}

// ---- writing a song's lyrics ----

// writeLyricsFile saves text as the song's lyrics, replacing what it had:
// synced lyrics as a .lrc beside it; plain ones into the FLAC tag when the
// box keeps them there (a .lrc would hide the tag), else as a .lrc.
func writeLyricsFile(file, text, plainTo string) (string, error) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return "", errors.New("no lyrics to save")
	}
	if lrcTime.MatchString(text) || !strings.EqualFold(filepath.Ext(file), ".flac") || plainTo == "lrc" {
		dst := existingLrc(file)
		if dst == "" {
			dst = lrcPath(file)
		}
		return "lrc", writeFileAtomic(dst, []byte(text+"\n"))
	}
	if _, err := flacSetTag(file, "UNSYNCEDLYRICS", text, "LYRICS"); err != nil {
		return "", err
	}
	if l := existingLrc(file); l != "" {
		if err := os.Remove(l); err != nil {
			return "", err
		}
	}
	return "tag", nil
}

// removeLyricsFile deletes the song's .lrc and the lyrics in its FLAC tags.
func removeLyricsFile(file string) error {
	if l := existingLrc(file); l != "" {
		if err := os.Remove(l); err != nil {
			return err
		}
	}
	if strings.EqualFold(filepath.Ext(file), ".flac") {
		if _, err := flacSetTag(file, "UNSYNCEDLYRICS", "", "LYRICS"); err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomic(dst string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".pocket-music-*.lrc.tmp")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *Server) noteLyricsAttempt(ctx context.Context, rel, result string) {
	_, _ = s.store.DB.ExecContext(ctx, `INSERT INTO lyrics_attempts (path, tried_at, result) VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET tried_at = excluded.tried_at, result = excluded.result`, rel, store.Now(), result)
}

// rescanAfterLyrics asks the music server to scan, if the box wants that.
func (s *Server) rescanAfterLyrics(ctx context.Context) {
	if box, _ := s.store.GetSettings(ctx, 0); box.LyricsRescan {
		if owner, err := s.store.GetUser(ctx, s.store.OwnerID(ctx)); err == nil {
			if mc, err := s.musicFor(ctx, owner); err == nil {
				_ = mc.c.StartScan(ctx)
			}
		}
	}
}

// lrclibTrouble says what went wrong at LRCLIB in words for a person:
// mostly it is busy (503, 429, a timeout) and asking later works.
func lrclibTrouble(err error) string {
	msg := err.Error()
	for _, busy := range []string{"503", "502", "504", "429", "deadline exceeded", "timeout"} {
		if strings.Contains(msg, busy) {
			return "LRCLIB is overloaded right now and turned the request away — nothing was changed. Try again in a few minutes."
		}
	}
	return "Couldn't reach LRCLIB (" + msg + ") — nothing was changed."
}

// ---- LRCLIB search: every match, to choose from ----

type lrclibMatch struct {
	ID           int64   `json:"id"`
	Artist       string  `json:"artist"`
	Title        string  `json:"title"`
	Album        string  `json:"album"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	Synced       bool    `json:"synced"`
	Text         string  `json:"text"`
}

// lrclibSearch asks LRCLIB's search, again after a pause when it is busy.
func (s *Server) lrclibSearch(ctx context.Context, artist, title string, duration int) (out []lrclibMatch, err error) {
	for try := 0; try < 3; try++ {
		if out, err = s.lrclibSearchOnce(ctx, artist, title, duration); err == nil {
			return out, nil
		}
		sleepCtx(ctx, time.Duration(try+1)*1500*time.Millisecond)
	}
	return nil, err
}

func (s *Server) lrclibSearchOnce(ctx context.Context, artist, title string, duration int) ([]lrclibMatch, error) {
	q := url.Values{"track_name": {title}}
	if artist != "" {
		q.Set("artist_name", artist)
	}
	var hits []struct {
		ID           int64   `json:"id"`
		ArtistName   string  `json:"artistName"`
		TrackName    string  `json:"trackName"`
		AlbumName    string  `json:"albumName"`
		Duration     float64 `json:"duration"`
		Instrumental bool    `json:"instrumental"`
		Plain        string  `json:"plainLyrics"`
		Synced       string  `json:"syncedLyrics"`
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, s.cfg.LyricsURL+"/api/search?"+q.Encode(), nil)
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	res, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LRCLIB answered %s", res.Status)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&hits); err != nil {
		return nil, err
	}
	out := make([]lrclibMatch, 0, len(hits))
	for _, h := range hits {
		c := lrclibMatch{ID: h.ID, Artist: h.ArtistName, Title: h.TrackName, Album: h.AlbumName, Duration: h.Duration, Instrumental: h.Instrumental, Synced: h.Synced != "", Text: firstNonEmpty(h.Synced, h.Plain)}
		if c.Text == "" && !c.Instrumental {
			continue
		}
		out = append(out, c)
	}
	off := func(c lrclibMatch) float64 {
		if duration <= 0 || c.Duration <= 0 {
			return 1e6
		}
		d := c.Duration - float64(duration)
		if d < 0 {
			d = -d
		}
		return d
	}
	// synced and the right length first, then synced, then the right length
	rank := func(c lrclibMatch) int {
		r := 3
		if c.Synced {
			r -= 2
		}
		if off(c) <= 3 {
			r--
		}
		return r
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := rank(out[i]), rank(out[j]); ri != rj {
			return ri < rj
		}
		return off(out[i]) < off(out[j])
	})
	if len(out) > 25 {
		out = out[:25]
	}
	return out, nil
}

// bestLyrics finds a song's lyrics at LRCLIB: the exact lookup, then the
// best search match (same title, length within 4 s, synced first).
func (s *Server) bestLyrics(ctx context.Context, t songTags) (synced, plain string, err error) {
	synced, plain, err = s.lrclibRaw(ctx, t)
	if err != nil || synced != "" {
		return
	}
	cands, err2 := s.lrclibSearch(ctx, t.artist, t.title, t.duration)
	if err2 != nil {
		return synced, plain, nil
	}
	for _, c := range cands {
		if store.MatchKey(c.Title) != store.MatchKey(t.title) || c.Instrumental || (t.duration > 0 && c.Duration > 0 && abs(int(c.Duration)-t.duration) > 4) {
			continue
		}
		if c.Synced {
			return c.Text, plain, nil
		}
		if plain == "" {
			plain = c.Text
		}
	}
	return "", plain, nil
}

// ---- API ----

type lyricsFileOut struct {
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	Artist    string `json:"artist"`
	Title     string `json:"title"`
	Album     string `json:"album"`
	Duration  int    `json:"duration"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Lines     int    `json:"lines"`
	ChangedAt string `json:"changed_at"`
	Attempt   string `json:"attempt"`  // what the last LRCLIB lookup found: synced | plain | none | skip | edited | ""
	TriedAt   string `json:"tried_at"` // when
	Snippet   string `json:"snippet"`  // with \x02 … \x03 around the matched words
	SongID    string `json:"song_id"`  // the person's song for this file ("" none)
	AlbumID   string `json:"album_id"`
}

var ftsToken = regexp.MustCompile(`"[^"]+"|\S+`)

// ftsQuery turns what was typed into an FTS5 query: every word must be
// there, the last one as a prefix; "quoted words" as a phrase.
func ftsQuery(q string) string {
	toks := ftsToken.FindAllString(q, -1)
	var out []string
	for i, t := range toks {
		phrase := strings.HasPrefix(t, `"`) && strings.HasSuffix(t, `"`) && len(t) > 1
		t = strings.TrimSpace(strings.ReplaceAll(t, `"`, ""))
		if t == "" {
			continue
		}
		q := `"` + t + `"`
		if !phrase && i == len(toks)-1 {
			q += "*"
		}
		out = append(out, q)
	}
	return strings.Join(out, " ")
}

func (s *Server) lyricsIndexStatus(ctx context.Context, admin bool) map[string]any {
	s.lix.mu.Lock()
	ix := map[string]any{"running": s.lix.running, "done": s.lix.done, "total": s.lix.total}
	s.lix.mu.Unlock()
	at, _, _ := s.store.CacheGet(ctx, "lyrics:indexed-at")
	ix["indexed_at"] = at
	out := map[string]any{"available": s.cfg.MusicDir != "", "index": ix, "online": s.cfg.LyricsURL != ""}
	if admin {
		box, _ := s.store.GetSettings(ctx, 0)
		ok, why := s.musicDirWritable()
		fetch, fwhy := s.lyricsWritable()
		s.lyr.mu.Lock()
		running := s.lyr.running
		s.lyr.mu.Unlock()
		var run any
		if running {
			if runs := s.lyricsRuns(ctx, 1); len(runs) > 0 {
				run = runs[0]
			}
		}
		out["writable"], out["detail"], out["can_fetch"], out["fetch_detail"] = ok, why, fetch, fwhy
		out["nightly"] = map[string]any{"enabled": box.LyricsWriter, "hour": box.LyricsHour, "retry_days": box.LyricsRetryDays}
		out["plain_to"] = box.LyricsPlain
		out["job"] = map[string]any{"running": running, "run": run}
	}
	return out
}

// handleLyricsIndex lists song files and their lyrics: filter (all | synced |
// plain | missing), q (words, artist, title, album), sort (path | recent |
// relevance), limit, offset.
func (s *Server) handleLyricsIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	admin := u.Role == "admin"
	qv := r.URL.Query()
	limit, _ := strconv.Atoi(qv.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	offset, _ := strconv.Atoi(qv.Get("offset"))
	offset = max(0, offset)
	q := ftsQuery(qv.Get("q"))

	from, where, args := "lyrics_files f", []string{}, []any{}
	if q != "" {
		from = "lyrics_fts JOIN lyrics_files f ON f.id = lyrics_fts.rowid"
		where = append(where, "lyrics_fts MATCH ?")
		args = append(args, q)
	}
	if !admin {
		// members: the songs their own library has
		// (two EXISTS, each on its index: one with OR scans every song for every file)
		where = append(where, `(EXISTS (SELECT 1 FROM songs sg INDEXED BY songs_path WHERE sg.user_id = ? AND sg.path = f.path)
			OR EXISTS (SELECT 1 FROM songs sg INDEXED BY songs_match WHERE sg.user_id = ? AND lower(sg.artist) = lower(f.artist)
				AND lower(sg.album) = lower(f.album) AND lower(sg.title) = lower(f.title)))`)
		args = append(args, u.ID, u.ID)
	}
	cond := func(extra ...string) string {
		all := append(append([]string{}, where...), extra...)
		if len(all) == 0 {
			return ""
		}
		return " WHERE " + strings.Join(all, " AND ")
	}
	counts := map[string]int{"all": 0, "synced": 0, "plain": 0, "missing": 0}
	rows, err := s.store.DB.QueryContext(ctx, `SELECT f.kind, COUNT(1) FROM `+from+cond()+` GROUP BY f.kind`, args...)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "can't search for that")
		return
	}
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			if k == "none" {
				k = "missing"
			}
			counts[k] += n
			counts["all"] += n
		}
	}
	rows.Close()

	filter := qv.Get("filter")
	extra := []string{}
	fargs := append([]any{}, args...)
	switch filter {
	case "synced", "plain":
		extra = append(extra, "f.kind = ?")
		fargs = append(fargs, filter)
	case "missing":
		extra = append(extra, "f.kind = 'none'")
	default:
		filter = "all"
	}
	order := " ORDER BY f.path COLLATE NOCASE"
	switch qv.Get("sort") {
	case "recent":
		order = " ORDER BY f.changed_at DESC, f.path COLLATE NOCASE"
	case "path":
	default:
		if q != "" {
			order = " ORDER BY lyrics_fts.rank"
		}
	}
	snippet := "''"
	if q != "" {
		snippet = "snippet(lyrics_fts, 3, char(2), char(3), '…', 12)"
	}
	rows, err = s.store.DB.QueryContext(ctx, `SELECT f.id, f.path, f.artist, f.title, f.album, f.duration, f.kind, f.source, f.lines, f.changed_at,
		COALESCE(a.result, ''), COALESCE(a.tried_at, ''), `+snippet+`
		FROM `+from+` LEFT JOIN lyrics_attempts a ON a.path = f.path`+cond(extra...)+order+` LIMIT ? OFFSET ?`, append(fargs, limit, offset)...)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "can't search for that")
		return
	}
	items := []lyricsFileOut{}
	for rows.Next() {
		var o lyricsFileOut
		if rows.Scan(&o.ID, &o.Path, &o.Artist, &o.Title, &o.Album, &o.Duration, &o.Kind, &o.Source, &o.Lines, &o.ChangedAt, &o.Attempt, &o.TriedAt, &o.Snippet) == nil {
			items = append(items, o)
		}
	}
	rows.Close()
	s.attachSongs(ctx, u.ID, items)
	total := counts[filter]
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "counts": counts, "status": s.lyricsIndexStatus(ctx, admin)})
}

// attachSongs finds the person's song for each file: by its path when the
// server reports real ones, else by artist, album and title (the same file
// type and the closest length first, for copies of a song). The indexes are
// named: left alone, SQLite walks the person's whole library by user_id.
func (s *Server) attachSongs(ctx context.Context, uid int64, items []lyricsFileOut) {
	for i := range items {
		it := &items[i]
		err := s.store.DB.QueryRowContext(ctx, `SELECT id, album_id FROM songs INDEXED BY songs_path WHERE user_id = ? AND path = ? LIMIT 1`, uid, it.Path).Scan(&it.SongID, &it.AlbumID)
		if err == nil {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(it.Path)), ".")
		_ = s.store.DB.QueryRowContext(ctx, `SELECT id, album_id FROM songs INDEXED BY songs_match
			WHERE user_id = ? AND lower(artist) = lower(?) AND lower(album) = lower(?) AND lower(title) = lower(?)
			ORDER BY lower(suffix) = ? DESC, abs(duration - ?) LIMIT 1`, uid, it.Artist, it.Album, it.Title, ext, it.Duration).Scan(&it.SongID, &it.AlbumID)
	}
}

// indexedFileFor finds a song's file through the lyrics index: by the path
// the server reports, else by artist, album and title.
func (s *Server) indexedFileFor(ctx context.Context, so store.Song) (string, bool) {
	if p, ok := s.songFileByPath(so); ok {
		return p, true
	}
	if s.cfg.MusicDir == "" {
		return "", false
	}
	var rel string
	err := s.store.DB.QueryRowContext(ctx, `SELECT path FROM lyrics_files INDEXED BY lyrics_files_match
		WHERE lower(artist) = lower(?) AND lower(album) = lower(?) AND lower(title) = lower(?)
		ORDER BY lower(path) LIKE ? DESC, abs(duration - ?) LIMIT 1`, so.Artist, so.Album, so.Title, "%."+strings.ToLower(so.Suffix), so.Duration).Scan(&rel)
	if err != nil {
		return "", false
	}
	p, ok := s.inMusicDir(rel)
	return p, ok
}

// lyricsFileFor loads one index row the person may see, and its file.
func (s *Server) lyricsFileFor(ctx context.Context, u *store.User, idStr string) (lyricsFileOut, string, error) {
	id, _ := strconv.ParseInt(idStr, 10, 64)
	var o lyricsFileOut
	err := s.store.DB.QueryRowContext(ctx, `SELECT f.id, f.path, f.artist, f.title, f.album, f.duration, f.kind, f.source, f.lines, f.changed_at,
		COALESCE(a.result, ''), COALESCE(a.tried_at, '')
		FROM lyrics_files f LEFT JOIN lyrics_attempts a ON a.path = f.path WHERE f.id = ?`, id).
		Scan(&o.ID, &o.Path, &o.Artist, &o.Title, &o.Album, &o.Duration, &o.Kind, &o.Source, &o.Lines, &o.ChangedAt, &o.Attempt, &o.TriedAt)
	if err != nil {
		return o, "", store.ErrNotFound
	}
	items := []lyricsFileOut{o}
	s.attachSongs(ctx, u.ID, items)
	o = items[0]
	if u.Role != "admin" && o.SongID == "" {
		return o, "", store.ErrNotFound
	}
	file, ok := s.inMusicDir(o.Path)
	if !ok {
		return o, "", store.ErrNotFound
	}
	return o, file, nil
}

func (s *Server) lyricsFileDetail(ctx context.Context, u *store.User, o lyricsFileOut, file string) map[string]any {
	text := ""
	switch o.Source {
	case "lrc":
		if l := existingLrc(file); l != "" {
			if b, err := readCapped(l, 1<<20); err == nil {
				text = string(b)
			}
		}
	case "tag":
		_, text = fileTags(file)
	}
	var song any
	if o.SongID != "" {
		if songs, err := s.store.SongsByID(ctx, u.ID, []string{o.SongID}); err == nil && len(songs) == 1 {
			song = songs[0]
		}
	}
	admin := u.Role == "admin"
	ok, why := s.musicDirWritable()
	box, _ := s.store.GetSettings(ctx, 0)
	return map[string]any{"file": o, "text": strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")), "song": song,
		"flac": strings.EqualFold(filepath.Ext(file), ".flac"), "plain_to": box.LyricsPlain,
		"can_edit": admin && ok, "edit_detail": why, "can_fetch": admin && ok && s.cfg.LyricsURL != ""}
}

func (s *Server) handleLyricsFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	o, file, err := s.lyricsFileFor(ctx, u, chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, s.lyricsFileDetail(ctx, u, o, file))
}

// handleLyricsFileBySong finds the lyrics index row for one of the person's
// songs (Now Playing → Edit).
func (s *Server) handleLyricsFileBySong(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	songs, err := s.songsFor(ctx, userFrom(ctx).ID, []string{chi.URLParam(r, "songId")})
	if err != nil || len(songs) == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	file, ok := s.indexedFileFor(ctx, songs[0])
	if !ok {
		file, ok = s.songFileFor(songs[0])
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "can't find this song's file in the music folder")
		return
	}
	var id int64
	q := `SELECT id FROM lyrics_files WHERE path = ?`
	if s.store.DB.QueryRowContext(ctx, q, s.relToMusicDir(file)).Scan(&id) != nil {
		s.lyricsIndexFile(ctx, file) // not read yet (a brand-new song)
		if err := s.store.DB.QueryRowContext(ctx, q, s.relToMusicDir(file)).Scan(&id); err != nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// lyricsEditable loads a row for an admin about to change the file.
func (s *Server) lyricsEditable(w http.ResponseWriter, r *http.Request) (lyricsFileOut, string, bool) {
	if !s.requireAdmin(w, r) {
		return lyricsFileOut{}, "", false
	}
	if ok, why := s.musicDirWritable(); !ok {
		writeErr(w, http.StatusConflict, why)
		return lyricsFileOut{}, "", false
	}
	o, file, err := s.lyricsFileFor(r.Context(), userFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return o, "", false
	}
	if _, err := os.Stat(file); err != nil {
		s.lyricsIndexFile(r.Context(), file)
		writeErr(w, http.StatusNotFound, "this song's file is gone from the music folder")
		return o, "", false
	}
	return o, file, true
}

// afterLyricsEdit re-reads the file into the index and answers with it.
func (s *Server) afterLyricsEdit(w http.ResponseWriter, r *http.Request, file string) {
	ctx := r.Context()
	u := userFrom(ctx)
	s.lyricsIndexFile(ctx, file)
	s.rescanAfterLyrics(ctx)
	var id int64
	_ = s.store.DB.QueryRowContext(ctx, `SELECT id FROM lyrics_files WHERE path = ?`, s.relToMusicDir(file)).Scan(&id)
	o, file, err := s.lyricsFileFor(ctx, u, strconv.FormatInt(id, 10))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, s.lyricsFileDetail(ctx, u, o, file))
}

// handleLyricsFileSave writes edited lyrics: {"text": "…"}.
func (s *Server) handleLyricsFileSave(w http.ResponseWriter, r *http.Request) {
	o, file, ok := s.lyricsEditable(w, r)
	if !ok {
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeErr(w, http.StatusBadRequest, "no lyrics to save — delete them instead")
		return
	}
	if len(in.Text) > 512<<10 {
		writeErr(w, http.StatusBadRequest, "lyrics too long")
		return
	}
	box, _ := s.store.GetSettings(r.Context(), 0)
	if _, err := writeLyricsFile(file, in.Text, box.LyricsPlain); err != nil {
		writeErr(w, http.StatusInternalServerError, "can't write next to the song: "+err.Error())
		return
	}
	// edited by hand: the nightly run leaves this song alone from now on
	s.noteLyricsAttempt(r.Context(), o.Path, "edited")
	s.afterLyricsEdit(w, r, file)
}

// handleLyricsFileDelete removes a song's lyrics; ?keep=1 also keeps the
// nightly run from fetching them again.
func (s *Server) handleLyricsFileDelete(w http.ResponseWriter, r *http.Request) {
	o, file, ok := s.lyricsEditable(w, r)
	if !ok {
		return
	}
	if err := removeLyricsFile(file); err != nil {
		writeErr(w, http.StatusInternalServerError, "can't change the song's files: "+err.Error())
		return
	}
	if r.URL.Query().Get("keep") == "1" {
		s.noteLyricsAttempt(r.Context(), o.Path, "skip")
	} else {
		_, _ = s.store.DB.ExecContext(r.Context(), `DELETE FROM lyrics_attempts WHERE path = ?`, o.Path)
	}
	s.afterLyricsEdit(w, r, file)
}

// handleLyricsFileFetch looks the song up at LRCLIB now and saves the best match.
func (s *Server) handleLyricsFileFetch(w http.ResponseWriter, r *http.Request) {
	o, file, ok := s.lyricsEditable(w, r)
	if !ok {
		return
	}
	if s.cfg.LyricsURL == "" {
		writeErr(w, http.StatusConflict, "online lyrics are switched off on this server (LYRICS_URL=off)")
		return
	}
	ctx := r.Context()
	t := songTags{artist: o.Artist, title: o.Title, album: o.Album, duration: o.Duration}
	if t.duration == 0 && o.SongID != "" {
		if songs, err := s.store.SongsByID(ctx, userFrom(ctx).ID, []string{o.SongID}); err == nil && len(songs) == 1 {
			t.duration = songs[0].Duration
		}
	}
	synced, plain, err := s.bestLyrics(ctx, t)
	if err != nil {
		writeErr(w, http.StatusBadGateway, lrclibTrouble(err))
		return
	}
	text := firstNonEmpty(synced, plain)
	if text == "" {
		s.noteLyricsAttempt(ctx, o.Path, "none")
		writeErr(w, http.StatusNotFound, "LRCLIB has no lyrics for this song yet — try Find online with other words")
		return
	}
	box, _ := s.store.GetSettings(ctx, 0)
	if _, err := writeLyricsFile(file, text, box.LyricsPlain); err != nil {
		writeErr(w, http.StatusInternalServerError, "can't write next to the song: "+err.Error())
		return
	}
	result := "plain"
	if synced != "" {
		result = "synced"
	}
	s.noteLyricsAttempt(ctx, o.Path, result)
	s.afterLyricsEdit(w, r, file)
}

// handleLyricsCandidates lists LRCLIB's matches for a song (?artist=&title=
// to search with other words).
func (s *Server) handleLyricsCandidates(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	if s.cfg.LyricsURL == "" {
		writeErr(w, http.StatusConflict, "online lyrics are switched off on this server (LYRICS_URL=off)")
		return
	}
	o, _, err := s.lyricsFileFor(ctx, userFrom(ctx), chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	artist, title := o.Artist, o.Title
	if r.URL.Query().Has("title") {
		artist, title = strings.TrimSpace(r.URL.Query().Get("artist")), strings.TrimSpace(r.URL.Query().Get("title"))
	}
	if title == "" {
		writeErr(w, http.StatusBadRequest, "a title is needed")
		return
	}
	cands, err := s.lrclibSearch(ctx, artist, title, o.Duration)
	if err != nil {
		writeErr(w, http.StatusBadGateway, lrclibTrouble(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": cands, "duration": o.Duration})
}

// handleLyricsIndexRefresh re-reads the music folder (admins).
func (s *Server) handleLyricsIndexRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.cfg.MusicDir == "" {
		writeErr(w, http.StatusConflict, "mount the music folder and set MUSIC_DIR")
		return
	}
	s.startLyricsIndex()
	writeJSON(w, http.StatusAccepted, s.lyricsIndexStatus(r.Context(), true))
}

// handleLyricsFetchMissing looks up every song without lyrics that matches
// {"q": "…"} (all of them without q) in the background. Songs LRCLIB didn't
// know recently are only asked again with {"again": true}; songs whose
// lyrics were deleted on purpose or edited by hand never.
func (s *Server) handleLyricsFetchMissing(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in struct {
		Q     string `json:"q"`
		Again bool   `json:"again"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &in); err != nil {
			writeErr(w, http.StatusBadRequest, "bad request")
			return
		}
	}
	q, args := `SELECT f.path FROM lyrics_files f WHERE f.kind = 'none' ORDER BY f.path`, []any{}
	if fq := ftsQuery(in.Q); fq != "" {
		q = `SELECT f.path FROM lyrics_fts JOIN lyrics_files f ON f.id = lyrics_fts.rowid WHERE lyrics_fts MATCH ? AND f.kind = 'none' ORDER BY f.path`
		args = append(args, fq)
	}
	rows, err := s.store.DB.QueryContext(ctx, q, args...)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "can't search for that")
		return
	}
	var files []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			if abs, ok := s.inMusicDir(p); ok {
				files = append(files, abs)
			}
		}
	}
	rows.Close()
	if len(files) == 0 {
		writeErr(w, http.StatusNotFound, "no songs without lyrics here")
		return
	}
	again := in.Again
	err = s.startLyricsJob("missing", func(ctx context.Context, t *lyricsTally, save func()) error {
		return s.lyricsFetchFiles(ctx, files, again, t, save)
	})
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"songs": len(files), "status": s.lyricsIndexStatus(ctx, true)})
}

// lyricsFetchFiles asks LRCLIB for each of files that still has no lyrics.
func (s *Server) lyricsFetchFiles(ctx context.Context, files []string, again bool, t *lyricsTally, save func()) error {
	box, _ := s.store.GetSettings(ctx, 0)
	tried := s.lyricsTried(ctx)
	retry := time.Duration(max(1, box.LyricsRetryDays)) * 24 * time.Hour
	delay := time.Duration(max(0, box.LyricsDelayMS)) * time.Millisecond
	t.Total = len(files)
	last := time.Now()
	for _, file := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t.Checked++
		if save != nil && time.Since(last) > 3*time.Second {
			save()
			last = time.Now()
		}
		if existingLrc(file) != "" {
			t.Have++
			continue
		}
		if a, ok := tried[s.relToMusicDir(file)]; ok && (a.never() || (!again && time.Since(a.at) < retry)) {
			t.Backoff++
			continue
		}
		if err := s.lyricsOne(ctx, box, file, t); err != nil {
			t.Note = "LRCLIB: " + err.Error()
			t.errStreak++
			if t.errStreak >= 8 {
				return fmt.Errorf("LRCLIB keeps failing (%v); try again later", err)
			}
			sleepCtx(ctx, 5*time.Second)
			continue
		}
		t.errStreak = 0
		sleepCtx(ctx, delay)
	}
	return nil
}

type lyricsTry struct {
	at     time.Time
	result string
}

// never: lyrics deleted on purpose, or edited by hand — no automatic lookups.
func (a lyricsTry) never() bool { return a.result == "skip" || a.result == "edited" }

func (s *Server) lyricsTried(ctx context.Context) map[string]lyricsTry {
	tried := map[string]lyricsTry{}
	rows, err := s.store.DB.QueryContext(ctx, `SELECT path, tried_at, result FROM lyrics_attempts`)
	if err != nil {
		return tried
	}
	defer rows.Close()
	for rows.Next() {
		var p, at, res string
		if rows.Scan(&p, &at, &res) == nil {
			if ts, err := time.Parse(time.RFC3339Nano, at); err == nil {
				tried[p] = lyricsTry{at: ts, result: res}
			}
		}
	}
	return tried
}
