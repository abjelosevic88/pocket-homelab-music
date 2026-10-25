package server

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// fakeSubsonic is a tiny OpenSubsonic server with one library per account.
type fakeSubsonic struct {
	*httptest.Server
	mu         sync.Mutex
	passwords  map[string]string
	songs      map[string][]subsonic.Song
	scrobbles  map[string][]string
	playlists  map[string][]string // id -> song ids (owner in name prefix)
	owners     map[string]string
	nextPlayID int
}

func newFakeSubsonic(t *testing.T) *fakeSubsonic {
	f := &fakeSubsonic{passwords: map[string]string{}, songs: map[string][]subsonic.Song{}, scrobbles: map[string][]string{}, playlists: map[string][]string{}, owners: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSubsonic) add(user, pass string, songs ...subsonic.Song) {
	f.passwords[user] = pass
	f.songs[user] = append(f.songs[user], songs...)
}

func (f *fakeSubsonic) serve(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	q := r.Form
	reply := func(v map[string]any) {
		v["status"] = "ok"
		v["version"] = "1.16.1"
		v["type"] = "fake"
		_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": v})
	}
	fail := func(code int, msg string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{"status": "failed", "error": map[string]any{"code": code, "message": msg}}})
	}
	user := q.Get("u")
	pass, ok := f.passwords[user]
	if !ok || subsonicToken(pass, q.Get("s")) != q.Get("t") {
		fail(40, "wrong username or password")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	mine := f.songs[user]
	method := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/"), ".view")
	switch method {
	case "ping":
		reply(map[string]any{})
	case "getScanStatus":
		reply(map[string]any{"scanStatus": map[string]any{"scanning": false, "count": len(mine), "lastScan": "2026-10-01T00:00:00Z"}})
	case "search3":
		so, _ := strconv.Atoi(q.Get("songOffset"))
		sc, _ := strconv.Atoi(q.Get("songCount"))
		ao, _ := strconv.Atoi(q.Get("albumOffset"))
		ac, _ := strconv.Atoi(q.Get("albumCount"))
		ro, _ := strconv.Atoi(q.Get("artistOffset"))
		rc, _ := strconv.Atoi(q.Get("artistCount"))
		var albums []subsonic.Album
		var artists []subsonic.Artist
		seenA, seenR := map[string]bool{}, map[string]bool{}
		for _, s := range mine {
			if !seenA[s.AlbumID] {
				seenA[s.AlbumID] = true
				albums = append(albums, subsonic.Album{ID: s.AlbumID, Name: s.Album, Artist: s.Artist, ArtistID: s.ArtistID, Year: s.Year, Genre: s.Genre, SongCount: 1})
			}
			if !seenR[s.ArtistID] {
				seenR[s.ArtistID] = true
				artists = append(artists, subsonic.Artist{ID: s.ArtistID, Name: s.Artist, AlbumCount: 1})
			}
		}
		reply(map[string]any{"searchResult3": map[string]any{"song": page(mine, so, sc), "album": page(albums, ao, ac), "artist": page(artists, ro, rc)}})
	case "getSong", "getAlbum":
		for _, s := range mine {
			if s.ID == q.Get("id") {
				reply(map[string]any{"song": s})
				return
			}
			if s.AlbumID == q.Get("id") {
				reply(map[string]any{"album": map[string]any{"id": s.AlbumID, "name": s.Album, "song": []subsonic.Song{s}}})
				return
			}
		}
		fail(70, "not found")
	case "getStarred2":
		reply(map[string]any{"starred2": map[string]any{}})
	case "scrobble":
		if q.Get("submission") == "true" {
			f.scrobbles[user] = append(f.scrobbles[user], q.Get("id"))
		}
		reply(map[string]any{})
	case "createPlaylist":
		id := q.Get("playlistId")
		if id == "" {
			f.nextPlayID++
			id = strconv.Itoa(f.nextPlayID)
			f.owners[id] = user
		}
		f.playlists[id] = q["songId"]
		reply(map[string]any{"playlist": map[string]any{"id": id, "name": q.Get("name"), "owner": user}})
	case "updatePlaylist", "startScan", "star", "unstar", "setRating":
		reply(map[string]any{})
	case "getPlaylist":
		if ids, ok := f.playlists[q.Get("id")]; ok && f.owners[q.Get("id")] == user {
			reply(map[string]any{"playlist": map[string]any{"id": q.Get("id"), "songCount": len(ids)}})
			return
		}
		fail(70, "not found")
	case "deletePlaylist":
		if f.owners[q.Get("id")] != user {
			fail(70, "not found")
			return
		}
		delete(f.playlists, q.Get("id"))
		delete(f.owners, q.Get("id"))
		reply(map[string]any{})
	case "getUser":
		if q.Get("username") != user {
			fail(50, "not authorized")
			return
		}
		reply(map[string]any{"user": map[string]any{"username": user}})
	case "stream":
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = io.WriteString(w, "AUDIO:"+user+":"+q.Get("id"))
	default:
		fail(0, "unsupported "+method)
	}
}

func page[T any](xs []T, off, n int) []T {
	if n <= 0 || off >= len(xs) {
		return []T{}
	}
	return xs[off:min(len(xs), off+n)]
}

func subsonicToken(pass, salt string) string {
	h := md5.Sum([]byte(pass + salt))
	return hex.EncodeToString(h[:])
}

// testServer is a Music server with two people, each with their own account and library.
func testServer(t *testing.T) (*Server, *fakeSubsonic, map[string]string, http.Handler) {
	t.Helper()
	fs := newFakeSubsonic(t)
	fs.add("ana", "ana-pw",
		subsonic.Song{ID: "s1", Title: "Blue Hour", Album: "Night Drive", AlbumID: "al1", Artist: "Ana Band", ArtistID: "ar1", Genre: "Synthwave", Year: 1986, Duration: 200, Created: "2026-10-01T00:00:00Z"},
		subsonic.Song{ID: "s2", Title: "Neon Rain", Album: "Night Drive", AlbumID: "al1", Artist: "Ana Band", ArtistID: "ar1", Genre: "Synthwave", Year: 1986, Duration: 220, Created: "2026-10-01T00:00:00Z"},
	)
	fs.add("ben", "ben-pw",
		subsonic.Song{ID: "s9", Title: "Iron Will", Album: "Forge", AlbumID: "al9", Artist: "Ben Metal", ArtistID: "ar9", Genre: "Heavy Metal", Year: 1984, Duration: 300, Created: "2026-10-01T00:00:00Z"},
	)
	dir := t.TempDir()
	cfg := testConfig(dir)
	cfg.NavidromeURL = fs.URL
	cfg.SyncEvery = time.Hour
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err := s.loadSecretKey(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tokens := map[string]string{}
	for _, name := range []string{"ana", "ben"} {
		u := &store.User{Email: name + "@example.com", Name: name, Role: "member"}
		if name == "ana" {
			u.Role = "admin"
		}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		sealed, _ := s.seal(name + "-pw")
		if err := st.SaveConnection(ctx, &store.Connection{UserID: u.ID, Kind: "direct", Username: name, Secret: sealed}); err != nil {
			t.Fatal(err)
		}
		if err := s.syncUser(ctx, u.ID, true); err != nil {
			t.Fatal(err)
		}
		tok, _, _ := st.CreateAPIToken(ctx, u.ID, "test")
		tokens[name] = tok
	}
	return s, fs, tokens, s.Router()
}

func call(t *testing.T, h http.Handler, tok, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out) == 0 && strings.HasPrefix(rec.Body.String(), "[") {
		var arr []any
		_ = json.Unmarshal(rec.Body.Bytes(), &arr)
		out["_list"] = arr
	}
	return rec.Code, out
}

func TestLibraryIsPerPerson(t *testing.T) {
	_, _, tok, h := testServer(t)
	code, out := call(t, h, tok["ana"], "GET", "/api/v1/albums", nil)
	if code != 200 || out["total"].(float64) != 1 {
		t.Fatalf("ana's albums: %d %v", code, out)
	}
	code, out = call(t, h, tok["ben"], "GET", "/api/v1/search?q=night", nil)
	if code != 200 || len(out["albums"].([]any)) != 0 || len(out["songs"].([]any)) != 0 {
		t.Fatalf("ben must not find ana's music: %v", out)
	}
	// another person's album id: not in ben's index, and his server account can't see it either
	if code, _ := call(t, h, tok["ben"], "GET", "/api/v1/albums/al1", nil); code != 404 {
		t.Fatalf("ben opening ana's album = %d, want 404", code)
	}
	if code, _ := call(t, h, tok["ben"], "GET", "/api/v1/songs/s1", nil); code != 404 {
		t.Fatalf("ben opening ana's song = %d, want 404", code)
	}
}

func TestListensArePersonalAndScrobbled(t *testing.T) {
	s, fs, tok, h := testServer(t)
	code, out := call(t, h, tok["ana"], "POST", "/api/v1/listens", map[string]any{"song_id": "s1", "played_at": time.Now().Add(-3 * time.Minute).UnixMilli(), "ms_played": 150000})
	if code != 200 || out["added"] != true {
		t.Fatalf("add listen: %d %v", code, out)
	}
	// the same play again (a retried request) is not counted twice
	_, out2 := call(t, h, tok["ana"], "POST", "/api/v1/listens", map[string]any{"song_id": "s1", "played_at": out["listen"].(map[string]any)["played_at"], "ms_played": 150000})
	if out2["added"] != false {
		t.Fatalf("duplicate play counted: %v", out2)
	}
	id := int64(out["listen"].(map[string]any)["id"].(float64))
	if code, _ := call(t, h, tok["ben"], "DELETE", "/api/v1/listens/"+strconv.FormatInt(id, 10), nil); code != 404 {
		t.Fatalf("ben deleting ana's play = %d, want 404", code)
	}
	_, hist := call(t, h, tok["ben"], "GET", "/api/v1/history", nil)
	if hist["total"].(float64) != 0 {
		t.Fatalf("ben sees ana's plays: %v", hist)
	}
	_, stats := call(t, h, tok["ana"], "GET", "/api/v1/stats?period=week", nil)
	if stats["totals"].(map[string]any)["plays"].(float64) != 1 {
		t.Fatalf("ana's stats: %v", stats["totals"])
	}
	// scrobbling to the music server happens in the background
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fs.mu.Lock()
		n := len(fs.scrobbles["ana"])
		fs.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.scrobbles["ana"]) != 1 || len(fs.scrobbles["ben"]) != 0 {
		t.Fatalf("scrobbles = %v", fs.scrobbles)
	}
	_ = s
}

func TestSmartPlaylistRules(t *testing.T) {
	_, fs, tok, h := testServer(t)
	call(t, h, tok["ana"], "POST", "/api/v1/listens", map[string]any{"song_id": "s1", "played_at": time.Now().Add(-time.Hour).UnixMilli(), "ms_played": 200000})
	rules := map[string]any{"name": "Never played synthwave", "sort": "title", "limit_count": 10,
		"rules": map[string]any{"match": "all", "conditions": []map[string]any{{"field": "genre", "op": "is", "value": "synthwave"}, {"field": "never_played", "op": "is", "value": "true"}}}}
	code, prev := call(t, h, tok["ana"], "POST", "/api/v1/smart/preview", rules)
	if code != 200 || prev["count"].(float64) != 1 {
		t.Fatalf("preview: %d %v", code, prev)
	}
	code, saved := call(t, h, tok["ana"], "POST", "/api/v1/smart", rules)
	if code != 200 || saved["playlist_id"] == "" || saved["song_count"].(float64) != 1 {
		t.Fatalf("save: %d %v", code, saved)
	}
	fs.mu.Lock()
	songs := fs.playlists[saved["playlist_id"].(string)]
	owner := fs.owners[saved["playlist_id"].(string)]
	fs.mu.Unlock()
	if len(songs) != 1 || songs[0] != "s2" || owner != "ana" {
		t.Fatalf("server playlist = %v by %s", songs, owner)
	}
	id := strconv.Itoa(int(saved["id"].(float64)))
	if code, _ := call(t, h, tok["ben"], "GET", "/api/v1/smart/"+id, nil); code != 404 {
		t.Fatalf("ben reading ana's smart playlist = %d", code)
	}
	if code, _ := call(t, h, tok["ben"], "DELETE", "/api/v1/smart/"+id, nil); code != 404 {
		t.Fatalf("ben deleting ana's smart playlist = %d", code)
	}
}

func TestParseLRC(t *testing.T) {
	lines, synced := parseLRC("[ar:Someone]\n[00:12.30]First line\n[00:05.5]Earlier\n[01:02.123][01:10.00]Twice\n")
	if !synced || len(lines) != 4 {
		t.Fatalf("lines = %v synced=%v", lines, synced)
	}
	if lines[0].T != 5500 || lines[0].Text != "Earlier" || lines[1].T != 12300 || lines[2].T != 62123 || lines[3].T != 70000 {
		t.Fatalf("times = %v", lines)
	}
	plain, synced := parseLRC("Just words\nMore words\n")
	if synced || len(plain) != 2 || plain[0].T != -1 {
		t.Fatalf("plain = %v", plain)
	}
}

func TestMatchKey(t *testing.T) {
	cases := [][2]string{{"Beyoncé feat. Jay-Z", "beyonce"}, {"Run to the Hills (2015 Remaster)", "runtothehills"}, {"AC/DC", "acdc"}, {"Sigur Rós", "sigurros"}}
	for _, c := range cases {
		if got := store.MatchKey(c[0]); got != c[1] {
			t.Errorf("MatchKey(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestGuessPlan(t *testing.T) {
	genres := []store.GenreCount{{Name: "Synthwave"}, {Name: "Ambient"}, {Name: "Heavy Metal"}, {Name: "Jazz Fusion"}}
	p := guessPlan("synth from the 80s for a rainy night", genres, []string{"Ana Band"})
	if p.YearFrom != 1980 || p.YearTo != 1989 {
		t.Errorf("decade = %d–%d", p.YearFrom, p.YearTo)
	}
	has := func(g string) bool {
		for _, x := range p.Genres {
			if x == g {
				return true
			}
		}
		return false
	}
	if !has("Synthwave") || !has("Ambient") || has("Heavy Metal") {
		t.Errorf("genres = %v", p.Genres)
	}
	if q := guessPlan("heavy workout", genres, nil); len(q.Genres) == 0 || q.Genres[0] != "Heavy Metal" {
		t.Errorf("workout genres = %v", q.Genres)
	}
}
