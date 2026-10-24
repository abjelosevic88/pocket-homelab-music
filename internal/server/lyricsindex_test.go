package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFtsQuery(t *testing.T) {
	for in, want := range map[string]string{
		"":                    "",
		"blue sk":             `"blue" "sk"*`,
		`"night drive" ana`:   `"night drive" "ana"*`,
		`say "hi`:             `"say" "hi"*`,
		`ana "night drive"`:   `"ana" "night drive"`,
		`  spaced    out  `:   `"spaced" "out"*`,
		`quote"in"the middle`: `"quoteinthe" "middle"*`,
	} {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFlacSetTag(t *testing.T) {
	p := filepath.Join(t.TempDir(), "song.flac")
	writeTestFlac(t, p, "Ana Band", "Blue Hour")
	if _, err := flacAddTag(p, "LYRICS", "old words"); err != nil {
		t.Fatal(err)
	}
	if changed, err := flacSetTag(p, "UNSYNCEDLYRICS", "new words", "LYRICS"); err != nil || !changed {
		t.Fatalf("set: %v %v", changed, err)
	}
	m, dur, err := flacMeta(p)
	if err != nil || dur != 10 || m["UNSYNCEDLYRICS"] != "new words" || m["LYRICS"] != "" || m["TITLE"] != "Blue Hour" {
		t.Fatalf("meta = %v %d %v", m, dur, err)
	}
	if changed, _ := flacSetTag(p, "UNSYNCEDLYRICS", "new words", "LYRICS"); changed {
		t.Fatal("setting the same value again rewrote the file")
	}
	if changed, err := flacSetTag(p, "UNSYNCEDLYRICS", "", "LYRICS"); err != nil || !changed {
		t.Fatalf("remove: %v %v", changed, err)
	}
	if flacHasTag(p, "UNSYNCEDLYRICS") {
		t.Fatal("tag still there after removing it")
	}
	raw, _ := os.ReadFile(p)
	if !strings.HasSuffix(string(raw), "\xff\xf8AUDIOFRAMES-that-must-survive") {
		t.Fatal("audio frames changed")
	}
}

func TestLyricsIndexPage(t *testing.T) {
	s, _, tok, h := testServer(t)
	var asked atomic.Int32
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/api/search":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "artistName": "Ana Band", "trackName": q.Get("track_name"), "albumName": "Live", "duration": 260, "syncedLyrics": "[00:02.00] live words"},
				{"id": 2, "artistName": "Ana Band", "trackName": q.Get("track_name"), "albumName": "Night Drive", "duration": 10, "plainLyrics": "plain words"},
				{"id": 3, "artistName": "Ana Band", "trackName": q.Get("track_name"), "albumName": "Night Drive", "duration": 11, "syncedLyrics": "[00:01.00] right words"},
			})
		case q.Get("track_name") == "Fresh":
			_ = json.NewEncoder(w).Encode(map[string]any{"syncedLyrics": "[00:03.00] fresh words"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer lr.Close()
	music := t.TempDir()
	s.cfg.MusicDir, s.cfg.LyricsURL = music, lr.URL
	ctx := context.Background()
	box, _ := s.store.GetSettings(ctx, 0)
	box.LyricsDelayMS, box.LyricsRescan = 0, false
	_ = s.store.SaveBoxSettings(ctx, box)

	album := filepath.Join(music, "Ana Band", "1986 - Night Drive")
	_ = os.MkdirAll(album, 0o755)
	blue := filepath.Join(album, "01. Blue Hour.flac")
	neon := filepath.Join(album, "02. Neon Rain.flac")
	fresh := filepath.Join(album, "03. Fresh.flac")
	writeTestFlac(t, blue, "Ana Band", "Blue Hour")
	writeTestFlac(t, neon, "Ana Band", "Neon Rain")
	writeTestFlac(t, fresh, "Ana Band", "Fresh")
	_ = os.WriteFile(filepath.Join(album, "01. Blue Hour.LRC"), []byte("[ar:Ana Band]\n[00:01.00] blue skies over Čačak\n[00:04.00] second line"), 0o644)
	if _, err := flacAddTag(neon, "UNSYNCEDLYRICS", "rain on the window"); err != nil {
		t.Fatal(err)
	}
	// ana's songs live at those paths; ben's song is nowhere in the folder
	_, _ = s.store.DB.Exec(`UPDATE songs SET path = 'Ana Band/1986 - Night Drive/01. Blue Hour.flac' WHERE id = 's1'`)
	_, _ = s.store.DB.Exec(`UPDATE songs SET path = 'Ana Band/1986 - Night Drive/02. Neon Rain.flac' WHERE id = 's2'`)

	if read, _, err := s.lyricsIndex(ctx, music); err != nil || read != 3 {
		t.Fatalf("index: %d %v", read, err)
	}
	// unchanged files are not read again
	if read, _, _ := s.lyricsIndex(ctx, music); read != 0 {
		t.Fatalf("second index read %d files", read)
	}

	list := func(tk, query string) map[string]any {
		t.Helper()
		code, out := call(t, h, tk, "GET", "/api/v1/lyrics-index?"+query, nil)
		if code != 200 {
			t.Fatalf("list %q: %d %v", query, code, out)
		}
		return out
	}
	items := func(out map[string]any) []map[string]any {
		var r []map[string]any
		for _, it := range out["items"].([]any) {
			r = append(r, it.(map[string]any))
		}
		return r
	}
	out := list(tok["ana"], "")
	c := out["counts"].(map[string]any)
	if out["total"].(float64) != 3 || c["synced"].(float64) != 1 || c["plain"].(float64) != 1 || c["missing"].(float64) != 1 {
		t.Fatalf("counts: %v", out)
	}
	byTitle := map[string]map[string]any{}
	for _, it := range items(out) {
		byTitle[it["title"].(string)] = it
	}
	if b := byTitle["Blue Hour"]; b["kind"] != "synced" || b["source"] != "lrc" || b["song_id"] != "s1" || b["lines"].(float64) != 2 {
		t.Fatalf("blue: %v", b)
	}
	if n := byTitle["Neon Rain"]; n["kind"] != "plain" || n["source"] != "tag" {
		t.Fatalf("neon: %v", n)
	}
	// words, without diacritics, last word as a prefix
	found := items(list(tok["ana"], "q=cacak"))
	if len(found) != 1 || found[0]["title"] != "Blue Hour" || !strings.Contains(found[0]["snippet"].(string), "\x02Čačak\x03") {
		t.Fatalf("search: %v", found)
	}
	if found := items(list(tok["ana"], "q=wind")); len(found) != 1 || found[0]["title"] != "Neon Rain" {
		t.Fatalf("prefix search: %v", found)
	}
	if found := items(list(tok["ana"], "filter=missing")); len(found) != 1 || found[0]["title"] != "Fresh" {
		t.Fatalf("missing: %v", found)
	}
	// odd input never breaks the query
	list(tok["ana"], "q="+`%22%22%20AND%20(`)

	// members see the songs their own library has — ben has none of these
	if out := list(tok["ben"], ""); out["total"].(float64) != 0 {
		t.Fatalf("ben sees %v", out)
	}
	blueID := fmt.Sprint(byTitle["Blue Hour"]["id"])
	freshID := fmt.Sprint(byTitle["Fresh"]["id"])
	neonID := fmt.Sprint(byTitle["Neon Rain"]["id"])
	if code, _ := call(t, h, tok["ben"], "GET", "/api/v1/lyrics-files/"+blueID, nil); code != 404 {
		t.Fatalf("ben reads ana's file: %d", code)
	}
	if code, _ := call(t, h, tok["ben"], "PUT", "/api/v1/lyrics-files/"+blueID, map[string]any{"text": "x"}); code != 403 {
		t.Fatalf("ben edits: %d", code)
	}
	// Now Playing → Edit finds the file for a song
	if code, out := call(t, h, tok["ana"], "GET", "/api/v1/lyrics-files/by-song/s1", nil); code != 200 || fmt.Sprint(out["id"]) != blueID {
		t.Fatalf("by song: %d %v", code, out)
	}
	code, d := call(t, h, tok["ana"], "GET", "/api/v1/lyrics-files/"+blueID, nil)
	if code != 200 || !strings.Contains(d["text"].(string), "[00:01.00] blue skies") || d["can_edit"] != true || d["song"].(map[string]any)["id"] != "s1" {
		t.Fatalf("detail: %d %v", code, d)
	}

	// an edit is saved where the lyrics were (the .LRC, keeping its name) and shows in Now Playing at once
	code, d = call(t, h, tok["ana"], "PUT", "/api/v1/lyrics-files/"+blueID, map[string]any{"text": "[00:01.50] blue skies, edited\r\n[00:04.00] second line"})
	if code != 200 || d["file"].(map[string]any)["attempt"] != "edited" {
		t.Fatalf("save: %d %v", code, d)
	}
	if b, _ := os.ReadFile(filepath.Join(album, "01. Blue Hour.LRC")); string(b) != "[00:01.50] blue skies, edited\n[00:04.00] second line\n" {
		t.Fatalf("lrc = %q", b)
	}
	_, ly := call(t, h, tok["ana"], "GET", "/api/v1/lyrics/s1", nil)
	if lines := ly["lines"].([]any); ly["synced"] != true || lines[0].(map[string]any)["text"] != "blue skies, edited" || lines[0].(map[string]any)["t"].(float64) != 1500 {
		t.Fatalf("now playing: %v", ly)
	}
	// plain words for a FLAC go into its tag (the box setting), and a .lrc that would hide them goes
	code, d = call(t, h, tok["ana"], "PUT", "/api/v1/lyrics-files/"+neonID, map[string]any{"text": "rain, edited"})
	if code != 200 || d["file"].(map[string]any)["source"] != "tag" || d["text"] != "rain, edited" {
		t.Fatalf("plain save: %d %v", code, d)
	}
	if m, _, _ := flacMeta(neon); m["UNSYNCEDLYRICS"] != "rain, edited" {
		t.Fatalf("tag = %v", m)
	}

	// LRCLIB's matches, synced and the right length first
	code, cands := call(t, h, tok["ana"], "GET", "/api/v1/lyrics-files/"+freshID+"/candidates", nil)
	ci := cands["items"].([]any)
	if code != 200 || len(ci) != 3 || ci[0].(map[string]any)["id"].(float64) != 3 || ci[1].(map[string]any)["id"].(float64) != 1 {
		t.Fatalf("candidates: %d %v", code, cands)
	}
	// fetch one song now
	code, d = call(t, h, tok["ana"], "POST", "/api/v1/lyrics-files/"+freshID+"/fetch", nil)
	if code != 200 || d["file"].(map[string]any)["kind"] != "synced" || !strings.Contains(d["text"].(string), "fresh words") {
		t.Fatalf("fetch: %d %v", code, d)
	}

	// delete and keep away: gone from the files, and no lookup brings them back
	code, d = call(t, h, tok["ana"], "DELETE", "/api/v1/lyrics-files/"+freshID+"?keep=1", nil)
	if code != 200 || d["file"].(map[string]any)["kind"] != "none" || d["file"].(map[string]any)["attempt"] != "skip" {
		t.Fatalf("delete: %d %v", code, d)
	}
	if existingLrc(fresh) != "" {
		t.Fatal(".lrc still there")
	}
	n := asked.Load()
	code, job := call(t, h, tok["ana"], "POST", "/api/v1/lyrics-index/fetch-missing", map[string]any{"again": true})
	if code != 202 || job["songs"].(float64) != 1 {
		t.Fatalf("fetch missing: %d %v", code, job)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.lyr.mu.Lock()
		running := s.lyr.running
		s.lyr.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run := s.lyricsRuns(ctx, 1)[0]; run.Trigger != "missing" || run.State != "done" || run.Backoff != 1 || asked.Load() != n {
		t.Fatalf("kept-away song was looked up: %+v (asked %d → %d)", run, n, asked.Load())
	}

	// files that leave the folder leave the index
	_ = os.Remove(neon)
	if _, dropped, _ := s.lyricsIndex(ctx, music); dropped != 1 {
		t.Fatalf("dropped = %d", dropped)
	}
	if out := list(tok["ana"], ""); out["total"].(float64) != 2 {
		t.Fatalf("after removal: %v", out["total"])
	}
}
