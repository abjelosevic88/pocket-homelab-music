package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

func gatewayCall(t *testing.T, h http.Handler, method string, q url.Values) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	q.Set("v", "1.16.1")
	q.Set("c", "TestApp")
	q.Set("f", "json")
	req := httptest.NewRequest("GET", "/rest/"+method+".view?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out struct {
		R map[string]any `json:"subsonic-response"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out.R
}

func tokenAuth(user, pass string) url.Values {
	return url.Values{"u": {user}, "t": {subsonicToken(pass, "abc123")}, "s": {"abc123"}}
}

func TestGatewaySignsInWithPocketPassword(t *testing.T) {
	_, fs, tok, h := testServer(t)
	code, out := call(t, h, tok["ana"], "POST", "/api/v1/subsonic/password", map[string]any{"username": "ana.p"})
	if code != 200 || out["password"] == nil || out["username"] != "ana.p" {
		t.Fatalf("make password: %d %v", code, out)
	}
	pw := out["password"].(string)

	if _, r := gatewayCall(t, h, "ping", tokenAuth("ana.p", pw)); r["status"] != "ok" {
		t.Fatalf("ping with the app password: %v", r)
	}
	if _, r := gatewayCall(t, h, "ping", tokenAuth("ana.p", "wrong")); r["status"] != "failed" {
		t.Fatalf("wrong password let in: %v", r)
	}
	// the music server's own password is not the Pocket one
	if _, r := gatewayCall(t, h, "ping", tokenAuth("ana", "ana-pw")); r["status"] != "failed" {
		t.Fatalf("server password works on the gateway: %v", r)
	}
	// plain p= and enc: forms
	if _, r := gatewayCall(t, h, "ping", url.Values{"u": {"ana.p"}, "p": {"enc:" + hexOf(pw)}}); r["status"] != "ok" {
		t.Fatalf("enc: password: %v", r)
	}

	// the app's user name becomes the server's for getUser
	if _, r := gatewayCall(t, h, "getUser", func() url.Values { v := tokenAuth("ana.p", pw); v.Set("username", "ana.p"); return v }()); r["status"] != "ok" {
		t.Fatalf("getUser with the Pocket user name: %v", r)
	}

	// streams pass through as the person
	rec, _ := gatewayCall(t, h, "stream", func() url.Values { v := tokenAuth("ana.p", pw); v.Set("id", "s1"); return v }())
	if rec.Body.String() != "AUDIO:ana:s1" || rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("stream = %q (%s)", rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	// a finished play lands in ana's history and goes on to her server
	sc := tokenAuth("ana.p", pw)
	sc.Set("id", "s2")
	sc.Set("submission", "true")
	sc.Set("time", "1790000000000")
	if _, r := gatewayCall(t, h, "scrobble", sc); r["status"] != "ok" {
		t.Fatalf("scrobble: %v", r)
	}
	_, hist := call(t, h, tok["ana"], "GET", "/api/v1/history", nil)
	if hist["total"].(float64) != 1 {
		t.Fatalf("ana's history after an app play: %v", hist)
	}
	_, hist = call(t, h, tok["ben"], "GET", "/api/v1/history", nil)
	if hist["total"].(float64) != 0 {
		t.Fatalf("ben sees ana's app play: %v", hist)
	}
	fs.mu.Lock()
	got := fs.scrobbles["ana"]
	fs.mu.Unlock()
	if len(got) != 1 || got[0] != "s2" {
		t.Fatalf("server scrobbles = %v", got)
	}

	// renewing replaces the password; disabling shuts the door
	_, out = call(t, h, tok["ana"], "POST", "/api/v1/subsonic/password", nil)
	if _, r := gatewayCall(t, h, "ping", tokenAuth("ana.p", pw)); r["status"] != "failed" {
		t.Fatalf("old password still works after renewing: %v", r)
	}
	if _, r := gatewayCall(t, h, "ping", tokenAuth("ana.p", out["password"].(string))); r["status"] != "ok" {
		t.Fatalf("renewed password: %v", r)
	}
	// ben can't take ana's user name
	if code, _ := call(t, h, tok["ben"], "POST", "/api/v1/subsonic/password", map[string]any{"username": "ANA.P"}); code != 409 {
		t.Fatalf("ben taking ana's user name = %d, want 409", code)
	}
	if code, _ := call(t, h, tok["ana"], "DELETE", "/api/v1/subsonic", nil); code != 204 {
		t.Fatalf("disable = %d", code)
	}
	if _, r := gatewayCall(t, h, "ping", tokenAuth("ana.p", out["password"].(string))); r["status"] != "failed" {
		t.Fatalf("disabled login still works: %v", r)
	}
}

func hexOf(s string) string {
	const d = "0123456789abcdef"
	var b strings.Builder
	for _, c := range []byte(s) {
		b.WriteByte(d[c>>4])
		b.WriteByte(d[c&15])
	}
	return b.String()
}

// a minimal FLAC: STREAMINFO (44.1 kHz, 10 s) + VORBIS_COMMENT + PADDING + fake frames
func writeTestFlac(t *testing.T, path, artist, title string) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("fLaC")
	si := make([]byte, 34)
	rate, total := uint64(44100), uint64(441000)
	si[10] = byte(rate >> 12)
	si[11] = byte(rate >> 4)
	si[12] = byte(rate<<4) | 0x02 // 2 channels-1 = 1 → bits, doesn't matter here
	si[13] = byte(total>>32) & 0x0f
	binary.BigEndian.PutUint32(si[14:18], uint32(total))
	b.Write([]byte{0, 0, 0, 34})
	b.Write(si)
	vc := vorbisComment{vendor: "test", comments: []string{"ARTIST=" + artist, "TITLE=" + title, "ALBUM=Night Drive"}}.bytes()
	b.Write([]byte{4, byte(len(vc) >> 16), byte(len(vc) >> 8), byte(len(vc))})
	b.Write(vc)
	b.Write([]byte{0x80 | 1, 0, 0, 16})
	b.Write(make([]byte, 16))
	b.WriteString("\xff\xf8AUDIOFRAMES-that-must-survive")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFlacAddTagKeepsAudio(t *testing.T) {
	p := filepath.Join(t.TempDir(), "song.flac")
	writeTestFlac(t, p, "Ana Band", "Blue Hour")
	if d := flacDuration(p); d != 10 {
		t.Fatalf("duration = %d, want 10", d)
	}
	added, err := flacAddTag(p, "UNSYNCEDLYRICS", "la la la\nsecond line")
	if err != nil || !added {
		t.Fatalf("add tag: %v %v", added, err)
	}
	if !flacHasTag(p, "unsyncedlyrics") {
		t.Fatal("tag not there after writing")
	}
	raw, _ := os.ReadFile(p)
	if !bytes.HasSuffix(raw, []byte("\xff\xf8AUDIOFRAMES-that-must-survive")) {
		t.Fatal("audio frames changed")
	}
	f, _ := os.Open(p)
	blocks, err := readFlacBlocks(bufio.NewReader(f))
	f.Close()
	if err != nil || len(blocks) != 3 || blocks[0].typ != 0 {
		t.Fatalf("blocks after write: %v %v", len(blocks), err)
	}
	vc, _ := parseVorbisComment(blocks[1].data)
	if len(vc.comments) != 4 || vc.vendor != "test" {
		t.Fatalf("comments = %v", vc.comments)
	}
	// a second time is a no-op
	if added, err := flacAddTag(p, "UNSYNCEDLYRICS", "other"); err != nil || added {
		t.Fatalf("second add: %v %v", added, err)
	}
	if tags, err := readSongTags(p); err != nil || tags.artist != "Ana Band" || tags.title != "Blue Hour" || tags.duration != 10 {
		t.Fatalf("tags = %+v %v", tags, err)
	}
}

func TestLyricsLibraryRun(t *testing.T) {
	s, _, tok, h := testServer(t)
	var asked atomic.Int32
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		switch r.URL.Query().Get("track_name") {
		case "Blue Hour":
			_ = json.NewEncoder(w).Encode(map[string]any{"syncedLyrics": "[00:01.00] blue", "plainLyrics": "blue"})
		case "Neon Rain":
			_ = json.NewEncoder(w).Encode(map[string]any{"plainLyrics": "rain, no timings"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer lr.Close()
	music := t.TempDir()
	s.cfg.MusicDir = music
	s.cfg.LyricsURL = lr.URL
	album := filepath.Join(music, "Ana Band", "1986 - Night Drive")
	_ = os.MkdirAll(album, 0o755)
	writeTestFlac(t, filepath.Join(album, "01. Blue Hour.flac"), "Ana Band", "Blue Hour")
	writeTestFlac(t, filepath.Join(album, "02. Neon Rain.flac"), "Ana Band", "Neon Rain")
	writeTestFlac(t, filepath.Join(album, "03. Unknown.flac"), "Ana Band", "Unknown")
	writeTestFlac(t, filepath.Join(album, "04. Had It.flac"), "Ana Band", "Had It")
	_ = os.WriteFile(filepath.Join(album, "04. Had It.lrc"), []byte("[00:00.00] mine"), 0o644)
	ctx := context.Background()
	box, _ := s.store.GetSettings(ctx, 0)
	box.LyricsDelayMS = 0
	_ = s.store.SaveBoxSettings(ctx, box)

	// members can't run it
	if code, _ := call(t, h, tok["ben"], "POST", "/api/v1/lyrics-library/run", nil); code != 403 {
		t.Fatalf("member run = %d, want 403", code)
	}
	// one folder, as the download hook does
	code, out := call(t, h, tok["ana"], "POST", "/api/v1/lyrics-library/run", map[string]any{"folder": "Ana Band/1986 - Night Drive"})
	if code != 200 || out["fetched"].(float64) != 1 || out["plain"].(float64) != 1 || out["not_found"].(float64) != 1 || out["have"].(float64) != 1 {
		t.Fatalf("folder run: %d %v", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(album, "01. Blue Hour.lrc")); string(b) != "[00:01.00] blue" {
		t.Fatalf("lrc = %q", b)
	}
	if !flacHasTag(filepath.Join(album, "02. Neon Rain.flac"), "UNSYNCEDLYRICS") {
		t.Fatal("plain lyrics not written to the FLAC tag")
	}
	if _, err := os.Stat(filepath.Join(album, "02. Neon Rain.lrc")); err == nil {
		t.Fatal("plain lyrics written as .lrc with the tag setting")
	}
	// outside the music folder: refused
	if code, _ := call(t, h, tok["ana"], "POST", "/api/v1/lyrics-library/run", map[string]any{"folder": "../../etc"}); code != 404 {
		t.Fatalf("escape = %d", code)
	}

	// "Save to library" from Now Playing: the song is found by its album folder and title
	_ = os.Remove(filepath.Join(album, "01. Blue Hour.lrc"))
	if code, _ := call(t, h, tok["ben"], "POST", "/api/v1/lyrics/s1/save", nil); code != 403 {
		t.Fatalf("member save = %d, want 403", code)
	}
	if code, out := call(t, h, tok["ana"], "POST", "/api/v1/lyrics/s1/save", nil); code != 200 || out["synced"] != true {
		t.Fatalf("save: %d %v", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(album, "01. Blue Hour.lrc")); string(b) != "[00:01.00] blue" {
		t.Fatalf("saved lrc = %q", b)
	}

	// a whole run asks for nothing again: songs LRCLIB didn't know wait for the retry days
	n := asked.Load()
	if code, _ := call(t, h, tok["ana"], "POST", "/api/v1/lyrics-library/run", nil); code != 202 {
		t.Fatalf("start run = %d", code)
	}
	deadline := time.Now().Add(3 * time.Second)
	var info map[string]any
	for time.Now().Before(deadline) {
		_, info = call(t, h, tok["ana"], "GET", "/api/v1/lyrics-library", nil)
		if info["running"] == false {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	runs := info["runs"].([]any)
	last := runs[0].(map[string]any)
	if last["state"] != "done" || last["backoff"].(float64) != 2 || last["have"].(float64) != 2 {
		t.Fatalf("full run: %v", last)
	}
	if asked.Load() != n {
		t.Fatalf("LRCLIB asked again for songs in backoff (%d → %d)", n, asked.Load())
	}
}

func TestImportLyricsState(t *testing.T) {
	s, _, _, _ := testServer(t)
	p := filepath.Join(t.TempDir(), "state.json")
	_ = os.WriteFile(p, []byte(`{"/home/x/media/music/A/B/01.flac": 1790000000.5, "/elsewhere/c.flac": 1}`), 0o644)
	n, err := ImportLyricsState(context.Background(), s.store, p, "/home/x/media/music/")
	if err != nil || n != 1 {
		t.Fatalf("import: %d %v", n, err)
	}
	var path string
	_ = s.store.DB.QueryRow(`SELECT path FROM lyrics_attempts`).Scan(&path)
	if path != "A/B/01.flac" {
		t.Fatalf("path = %q", path)
	}
	_ = io.Discard
}

func TestSamePlayDifferentSpellingCountsOnce(t *testing.T) {
	s, _, _, _ := testServer(t)
	ctx := context.Background()
	u, _ := s.store.GetUserByEmail(ctx, "ana@example.com")
	at := "2026-10-21T08:10:10Z"
	n, err := s.store.AddListens(ctx, u.ID, []store.Listen{{Title: "Weeping Dawn (City Version)", Artist: "Borislav Slavov", PlayedAt: at, Source: "maloja"}})
	if err != nil || n != 1 {
		t.Fatalf("first: %d %v", n, err)
	}
	n, _ = s.store.AddListens(ctx, u.ID, []store.Listen{{Title: "Weeping Dawn", Artist: "Borislav Slavov ", PlayedAt: "2026-10-21T08:10:10.000Z", Source: "maloja"}, {Title: "Next", Artist: "X", PlayedAt: "2026-10-21T08:13:10Z", Source: "maloja"}})
	if n != 1 {
		t.Fatalf("re-import added %d, want 1 (only the new play)", n)
	}
	// another person at the same second is their own play
	b, _ := s.store.GetUserByEmail(ctx, "ben@example.com")
	if n, _ := s.store.AddListens(ctx, b.ID, []store.Listen{{Title: "Weeping Dawn", Artist: "Borislav Slavov", PlayedAt: at}}); n != 1 {
		t.Fatalf("ben's play at the same second = %d", n)
	}
}

func TestRelinkSplitArtists(t *testing.T) {
	s, _, _, _ := testServer(t)
	ctx := context.Background()
	u, _ := s.store.GetUserByEmail(ctx, "ana@example.com")
	for _, x := range [][3]string{{"t1", "AC/DC", "Demon Fire"}, {"t2", "Soundscapes & Ambience", "Silver Dragon Inn"}, {"t3", "Other Band", "Silver Dragon Inn"}} {
		if _, err := s.store.DB.Exec(`INSERT INTO songs (user_id, id, title, artist) VALUES (?, ?, ?, ?)`, u.ID, x[0], x[2], x[1]); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = s.store.AddListens(ctx, u.ID, []store.Listen{
		{Title: "Demon Fire", Artist: "AC", PlayedAt: "2026-10-01T10:00:00Z"},
		{Title: "Silver Dragon Inn", Artist: "Ambience & Soundscapes", PlayedAt: "2026-10-01T11:00:00Z"},
		{Title: "Silver Dragon Inn", Artist: "Nobody", PlayedAt: "2026-10-01T12:00:00Z"},
	})
	n, err := s.store.RelinkListens(ctx, u.ID)
	if err != nil || n != 2 {
		t.Fatalf("relinked %d %v, want 2", n, err)
	}
	ls, _ := s.store.Listens(ctx, u.ID, "2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z", -1, "")
	got := map[string]string{}
	for _, l := range ls {
		got[l.PlayedAt[11:13]] = l.SongID + "|" + l.Artist
	}
	if got["10"] != "t1|AC/DC" || got["11"] != "t2|Soundscapes & Ambience" || got["12"] != "|Nobody" {
		t.Fatalf("relinked = %v", got)
	}
}
