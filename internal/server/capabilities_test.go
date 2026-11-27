package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-music/internal/config"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// capScopes are the link tokens of the fake Core: pcx_<who>_<what> acts for
// <who>@example.com with one scope.
var capScopes = map[string]string{
	"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
	"read": "listening:read", "playlists": "playlists:write", "play": "music:play",
}

// capServer is testServer behind a fake Core that knows the capScopes tokens
// and keeps the manifest the app registers with.
func capServer(t *testing.T) (*Server, map[string]string, http.Handler, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var manifest map[string]any
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			mu.Lock()
			manifest = map[string]any{}
			_ = json.NewDecoder(r.Body).Decode(&manifest)
			mu.Unlock()
			io.WriteString(w, `{"token":"pca_test"}`)
		case "/api/v1/auth/introspect":
			var in struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
			if len(parts) != 2 || capScopes[parts[1]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			email := parts[0] + "@example.com"
			fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"music","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"search","name":"Pocket Search"}}`,
				capScopes[parts[1]], email, email, parts[0])
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(coreSrv.Close)
	s, _, tok, h := testServer(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
	return s, tok, h, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return manifest
	}
}

func pocketGet(t *testing.T, h http.Handler, tok, path string, out any) int {
	t.Helper()
	code, body := rawCall(t, h, tok, "GET", "/api/v1/pocket/"+path, "", nil)
	if code == 200 {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("%s: %v (%s)", path, err, body)
		}
	}
	return code
}

func pocketFind(t *testing.T, h http.Handler, tok, q string) []core.SearchItem {
	t.Helper()
	var res core.SearchResults
	if code := pocketGet(t, h, tok, "search?"+q, &res); code != 200 || res.Items == nil {
		t.Fatalf("search %s = %d %+v", q, code, res)
	}
	return res.Items
}

func found(items []core.SearchItem) string {
	var out []string
	for _, it := range items {
		out = append(out, it.Kind+" "+it.Title)
	}
	return strings.Join(out, " | ")
}

func userID(t *testing.T, s *Server, name string) int64 {
	t.Helper()
	u, err := s.store.GetUserByEmail(context.Background(), name+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// person adds somebody with a token and, when account is not empty, that
// account on the music server: connected, but nothing indexed yet.
func person(t *testing.T, s *Server, name, account, password string) string {
	t.Helper()
	ctx := context.Background()
	u := &store.User{Email: name + "@example.com", Name: name, Role: "member"}
	if err := s.store.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if account != "" {
		sealed, _ := s.seal(password)
		if err := s.store.SaveConnection(ctx, &store.Connection{UserID: u.ID, Kind: "direct", Username: account, Secret: sealed}); err != nil {
			t.Fatal(err)
		}
	}
	tok, _, _ := s.store.CreateAPIToken(ctx, u.ID, "test")
	return tok
}

// play records a play of one of the person's songs through POST /listens.
func play(t *testing.T, h http.Handler, tok, song string, at time.Time, ms int64) {
	t.Helper()
	if code, out := call(t, h, tok, "POST", "/api/v1/listens", map[string]any{"song_id": song, "played_at": at.UnixMilli(), "ms_played": ms}); code != 200 || out["added"] != true {
		t.Fatalf("play %s: %d %v", song, code, out)
	}
}

// Pocket Search finds a person's albums, artists and songs in one list, the
// name that answers best first, and nothing of anybody else's.
func TestPocketSearch(t *testing.T) {
	s, _, tok, h := testServer(t)
	ana, ben := tok["ana"], tok["ben"]

	// the album by its name, then the songs found through it
	got := pocketFind(t, h, ana, "q=night")
	if found(got) != "album Night Drive | song Blue Hour | song Neon Rain" {
		t.Fatalf("night = %s", found(got))
	}
	if it := got[0]; it.ID != "album:al1" || it.URL != "/albums/al1" || it.Snippet != "Ana Band · 1986" {
		t.Errorf("album = %+v", it)
	}
	if it := got[1]; it.ID != "song:s1" || it.URL != "/albums/al1" || it.Snippet != "Ana Band · Night Drive · 3:20" {
		t.Errorf("song = %+v", it)
	}
	// an artist's name: the artist, the album, the songs
	got = pocketFind(t, h, ana, "q=ana+band")
	if found(got) != "artist Ana Band | album Night Drive | song Blue Hour | song Neon Rain" {
		t.Fatalf("ana band = %s", found(got))
	}
	if it := got[0]; it.ID != "artist:ar1" || it.URL != "/artists/ar1" || it.Snippet != "1 album · 2 songs" {
		t.Errorf("artist = %+v", it)
	}
	// a song by its title comes before the rest; accents and capitals do not matter
	for _, q := range []string{"q=neon", "q=N%C3%89ON+ra"} {
		if got := pocketFind(t, h, ana, q); len(got) != 1 || got[0].ID != "song:s2" || got[0].Snippet != "Ana Band · Night Drive · 3:40" {
			t.Errorf("%s = %+v", q, got)
		}
	}
	// the limit, nothing asked, and words without a letter or a digit
	if got := pocketFind(t, h, ana, "q=ana&limit=2"); found(got) != "artist Ana Band | album Night Drive" {
		t.Errorf("limit 2 = %s", found(got))
	}
	for _, q := range []string{"q=", "q=+", "q=%3F%3F", "q=*", "q=%22"} {
		if got := pocketFind(t, h, ana, q); len(got) != 0 {
			t.Errorf("%s = %s", q, found(got))
		}
	}
	// each person finds only their own music
	for _, q := range []string{"q=night", "q=ana", "q=blue", "q=synthwave", "q=n"} {
		if got := pocketFind(t, h, ben, q); len(got) != 0 {
			t.Errorf("Ben found Ana's music with %s: %s", q, found(got))
		}
	}
	if got := pocketFind(t, h, ben, "q=forge"); found(got) != "album Forge | song Iron Will" || got[1].Snippet != "Ben Metal · Forge · 5:00" {
		t.Errorf("Ben's own = %+v", got)
	}
	if got := pocketFind(t, h, ana, "q=iron"); len(got) != 0 {
		t.Errorf("Ana found Ben's music: %s", found(got))
	}
	// GET /search is as it was
	if code, out := call(t, h, ana, "GET", "/api/v1/search?q=night", nil); code != 200 || len(out["albums"].([]any)) != 1 || len(out["songs"].([]any)) != 2 || len(out["artists"].([]any)) != 0 {
		t.Errorf("/search = %d %v", code, out)
	}
	_ = s
}

// While a person's index is empty the search asks the music server with
// their own account, as GET /search does; no account means nothing found.
func TestPocketSearchBeforeTheFirstSync(t *testing.T) {
	s, fs, _, h := testServer(t)
	fs.add("cy", "cy-pw", subsonic.Song{ID: "s20", Title: "Low Tide", Album: "Shore", AlbumID: "al20", Artist: "Cy Trio", ArtistID: "ar20", Year: 2001, Duration: 65})
	cy := person(t, s, "cy", "cy", "cy-pw")
	if got := pocketFind(t, h, cy, "q=shore"); found(got) != "album Shore | artist Cy Trio | song Low Tide" || got[0].ID != "album:al20" || got[2].Snippet != "Cy Trio · Shore · 1:05" {
		t.Errorf("from the server = %+v", got)
	}
	// nobody's account: an empty list, where GET /search says 409
	dee := person(t, s, "dee", "", "")
	if got := pocketFind(t, h, dee, "q=shore"); len(got) != 0 {
		t.Errorf("no account = %s", found(got))
	}
	if code, _ := call(t, h, dee, "GET", "/api/v1/search?q=shore", nil); code != 409 {
		t.Errorf("/search without an account = %d, want 409", code)
	}
	// an account that no longer signs in: the same
	eve := person(t, s, "eve", "cy", "wrong")
	if got := pocketFind(t, h, eve, "q=shore"); len(got) != 0 {
		t.Errorf("wrong password = %s", found(got))
	}
	// a music server that does not answer is an error, without its text
	fs.Close()
	code, body := rawCall(t, h, cy, "GET", "/api/v1/pocket/search?q=shore", "", nil)
	if code != 500 || !strings.Contains(string(body), "the app could not answer") || strings.Contains(string(body), "127.0.0.1") {
		t.Errorf("server down = %d %s", code, body)
	}
}

func pocketDayOf(t *testing.T, h http.Handler, tok, date, tz string) core.Day {
	t.Helper()
	var d core.Day
	if code := pocketGet(t, h, tok, "day/"+date+"?tz="+tz, &d); code != 200 || d.Date != date || d.Items == nil {
		t.Fatalf("day %s = %d %+v", date, code, d)
	}
	return d
}

func metrics(ms []core.Metric) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Label+"="+m.Value)
	}
	return strings.Join(out, ", ")
}

// Pocket Day gets each song of the day once, with its minutes and plays, in
// the caller's time zone; the minutes add up to the day's listening.
func TestPocketDay(t *testing.T) {
	s, _, tok, h := testServer(t)
	ana, ben := tok["ana"], tok["ben"]
	at := func(clock string) time.Time {
		v, err := time.Parse("2006-01-02 15:04", "2026-11-20 "+clock)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	play(t, h, ana, "s1", at("09:30"), 150000)
	play(t, h, ana, "s1", at("10:00"), 200000)
	play(t, h, ana, "s2", at("23:30"), 0) // no time played: the song's length counts

	d := pocketDayOf(t, h, ana, "2026-11-20", "UTC")
	if len(d.Items) != 2 || d.Summary != "Listened to Ana Band for 9 min" || metrics(d.Metrics) != "Time=9 min, Plays=3, Top album=Night Drive, New to you=2 songs" {
		t.Fatalf("day = %+v", d)
	}
	if it := d.Items[0]; it.ID != "s1" || it.Title != "Blue Hour" || it.Sub != "Ana Band" || it.Time != "09:30" || it.Minutes != 5.83 || it.Qty != 2 ||
		it.URL != "/albums/al1" || it.Image != "/api/v1/cover/al-al1?size=96" || it.Kind != "" {
		t.Errorf("Blue Hour = %+v", it)
	}
	if it := d.Items[1]; it.ID != "s2" || it.Time != "23:30" || it.Minutes != 3.67 || it.Qty != 1 {
		t.Errorf("Neon Rain = %+v", it)
	}
	// an hour east the late song belongs to the next day
	d = pocketDayOf(t, h, ana, "2026-11-20", "Europe/Sarajevo")
	if len(d.Items) != 1 || d.Items[0].Time != "10:30" || d.Summary != "Listened to Ana Band for 5 min" || metrics(d.Metrics) != "Time=5 min, Plays=2, Top album=Night Drive, New to you=1 song" {
		t.Errorf("Sarajevo, the 20th = %+v", d)
	}
	d = pocketDayOf(t, h, ana, "2026-11-21", "Europe/Sarajevo")
	if len(d.Items) != 1 || d.Items[0].Title != "Neon Rain" || d.Items[0].Time != "00:30" || d.Summary != "Listened to Ana Band for 3 min" {
		t.Errorf("Sarajevo, the 21st = %+v", d)
	}

	// plays brought in from elsewhere: no song of the library behind them
	ctx, uid := context.Background(), userID(t, s, "ana")
	for i, l := range []store.Listen{
		{Title: "Far Away", Artist: "The Others", Album: "Elsewhere", Duration: 3000, PlayedAt: "2026-11-22T08:00:00Z", Source: "maloja"},
		{Title: "Far Away", Artist: "The Others", Album: "Elsewhere", Duration: 3000, PlayedAt: "2026-11-22T09:00:00Z", Source: "maloja"},
		{Title: "Blue Hour", Artist: "Ana Band", Album: "Night Drive", AlbumID: "al1", SongID: "s1", Duration: 200, PlayedAt: "2026-11-22T10:00:00Z", Source: "maloja"},
	} {
		if added, err := s.store.AddListen(ctx, uid, &l); err != nil || !added {
			t.Fatalf("listen %d: %v %v", i, added, err)
		}
	}
	d = pocketDayOf(t, h, ana, "2026-11-22", "UTC")
	if len(d.Items) != 2 || d.Summary != "Listened for 1 h 43 min, mostly The Others" || metrics(d.Metrics) != "Time=1 h 43 min, Plays=3, Top album=Elsewhere, New to you=1 song" {
		t.Fatalf("the 22nd = %+v", d)
	}
	if it := d.Items[0]; it.ID != "" || it.Title != "Far Away" || it.Sub != "The Others" || it.Minutes != 100 || it.Qty != 2 || it.URL != "/songs" || it.Image != "" {
		t.Errorf("Far Away = %+v", it)
	}
	// a song of no known length still counts as a song
	if _, err := s.store.AddListen(ctx, uid, &store.Listen{Title: "Short", Artist: "Ana Band", PlayedAt: "2026-11-23T08:00:00Z", Source: "maloja"}); err != nil {
		t.Fatal(err)
	}
	if d = pocketDayOf(t, h, ana, "2026-11-23", "UTC"); d.Summary != "Listened to 1 song by Ana Band" || metrics(d.Metrics) != "Plays=1, New to you=1 song" {
		t.Errorf("the 23rd = %+v", d)
	}

	// a day with nothing, another person's day, a day that is not one
	if d = pocketDayOf(t, h, ana, "2026-11-19", "UTC"); len(d.Items) != 0 || d.Summary != "" || len(d.Metrics) != 0 {
		t.Errorf("an empty day = %+v", d)
	}
	for _, date := range []string{"2026-11-20", "2026-11-22"} {
		if d = pocketDayOf(t, h, ben, date, "UTC"); len(d.Items) != 0 || d.Summary != "" {
			t.Errorf("Ben sees Ana's %s: %+v", date, d)
		}
	}
	for _, date := range []string{"2026-13-40", "today", "2026-11-2"} {
		if code, _ := rawCall(t, h, ana, "GET", "/api/v1/pocket/day/"+date, "", nil); code != 400 {
			t.Errorf("day %s = %d, want 400", date, code)
		}
	}
	// GET /day/{date} is as it was
	if code, out := call(t, h, ana, "GET", "/api/v1/day/2026-11-20?tz=0", nil); code != 200 || len(out["listens"].([]any)) != 3 ||
		out["summary"] != "Listened to 9 min of music (3 songs), mostly Ana Band." {
		t.Errorf("/day = %d %v", code, out["summary"])
	}
}

// A day with more songs than a day's list holds keeps the minutes right: the
// songs listened to longest are listed, the rest share the last line.
func TestPocketDayWithManySongs(t *testing.T) {
	s, _, tok, h := testServer(t)
	ctx, uid := context.Background(), userID(t, s, "ana")
	start := time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)
	ls := make([]store.Listen, 0, 205)
	for i := 0; i < 205; i++ {
		l := store.Listen{Title: fmt.Sprintf("Song %03d", i), Artist: fmt.Sprintf("Artist %d", i%4), Duration: 120, PlayedAt: store.Stamp(start.Add(time.Duration(i) * 2 * time.Minute)), Source: "maloja"}
		if i < 6 {
			l.Duration = 60 // the six shortest are the ones left out
		}
		ls = append(ls, l)
	}
	if n, err := s.store.AddListens(ctx, uid, ls); err != nil || n != 205 {
		t.Fatalf("listens: %d %v", n, err)
	}
	d := pocketDayOf(t, h, tok["ana"], "2026-11-20", "UTC")
	if len(d.Items) != 200 || d.Summary != "Listened for 6 h 44 min, mostly Artist 0" {
		t.Fatalf("day = %d items, %q", len(d.Items), d.Summary)
	}
	total := 0.0
	for _, it := range d.Items {
		total += it.Minutes
	}
	if last := d.Items[199]; total != 404 || last.Title != "6 more songs" || last.Minutes != 6 || last.Qty != 6 || last.Time != "" || last.URL != "/history" {
		t.Errorf("total %v, last = %+v", total, last)
	}
}

func pocketWidgetOf(t *testing.T, h http.Handler, tok string) core.WidgetData {
	t.Helper()
	var w core.WidgetData
	if code := pocketGet(t, h, tok, "widget", &w); code != 200 || w.Items == nil {
		t.Fatalf("widget = %d %+v", code, w)
	}
	return w
}

// Pocket Hub gets the songs played last, each once, and what plays now when
// a device said so.
func TestPocketWidget(t *testing.T) {
	_, fs, tok, h := testServer(t)
	ana, ben := tok["ana"], tok["ben"]
	if w := pocketWidgetOf(t, h, ana); w.Line != "" || len(w.Items) != 0 || len(w.Metrics) != 0 {
		t.Fatalf("nothing played = %+v", w)
	}
	now := time.Now()
	play(t, h, ana, "s1", now.Add(-50*time.Hour), 180000)
	play(t, h, ana, "s2", now.Add(-49*time.Hour), 120000)
	play(t, h, ana, "s1", now.Add(-48*time.Hour), 180000)
	w := pocketWidgetOf(t, h, ana)
	if w.Line != "Last played: Blue Hour by Ana Band" || w.URL != "/history" || len(w.Items) != 2 || metrics(w.Metrics) != "Last 7 days=8 min" {
		t.Fatalf("widget = %+v", w)
	}
	if it := w.Items[0]; it.ID != "s1" || it.Title != "Blue Hour" || it.Detail != "Ana Band" || it.URL != "/albums/al1" {
		t.Errorf("first row = %+v", it)
	}
	if it := w.Items[1]; it.ID != "s2" || it.Title != "Neon Rain" {
		t.Errorf("second row = %+v", it)
	}
	// a play just now counts for today too (around midnight it may be yesterday's)
	at := now.Add(-2 * time.Minute)
	play(t, h, ana, "s2", at, 60000)
	want := "Last 7 days=9 min"
	if at.Format("2006-01-02") == time.Now().Format("2006-01-02") {
		want = "Today=1 min, " + want
	}
	if w = pocketWidgetOf(t, h, ana); w.Line != "Last played: Neon Rain by Ana Band" || metrics(w.Metrics) != want || w.Items[0].ID != "s2" || len(w.Items) != 2 {
		t.Errorf("after a play = %+v", w)
	}
	// a device reports a song: it leads, and is not listed twice
	if code, _ := call(t, h, ana, "POST", "/api/v1/now", map[string]any{"device_id": "web-1", "device_name": "Laptop", "song_id": "s1", "playing": true}); code != 204 {
		t.Fatalf("now = %d", code)
	}
	if w = pocketWidgetOf(t, h, ana); w.Line != "Playing now: Blue Hour by Ana Band" || len(w.Items) != 2 || w.Items[0].ID != "s1" || w.Items[1].ID != "s2" {
		t.Errorf("playing = %+v", w)
	}
	call(t, h, ana, "POST", "/api/v1/now", map[string]any{"device_id": "web-1", "device_name": "Laptop", "song_id": "s1", "playing": false})
	if w = pocketWidgetOf(t, h, ana); w.Line != "Last played: Neon Rain by Ana Band" {
		t.Errorf("paused = %+v", w)
	}
	// the other person sees none of it, and the music server is not needed
	if w = pocketWidgetOf(t, h, ben); w.Line != "" || len(w.Items) != 0 || len(w.Metrics) != 0 {
		t.Errorf("Ben's widget = %+v", w)
	}
	fs.Close()
	if w = pocketWidgetOf(t, h, ana); len(w.Items) != 2 {
		t.Errorf("without the music server = %+v", w)
	}
	var d core.Day
	if code := pocketGet(t, h, ana, "day/"+at.UTC().Format("2006-01-02")+"?tz=UTC", &d); code != 200 || len(d.Items) == 0 {
		t.Errorf("day without the music server = %d %+v", code, d)
	}
}

// A link token with one of the pocket:* scopes reaches its one route (and
// who it acts for) and nothing else; the older scopes do not open the new routes.
func TestPocketLinkScopes(t *testing.T) {
	_, tok, h, _ := capServer(t)
	play(t, h, tok["ana"], "s1", time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC), 150000)
	const day = "/api/v1/pocket/day/2026-11-20"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{"pcx_ana_search", "GET", "/api/v1/pocket/search?q=night", 200},
		{"pcx_ana_search", "GET", "/api/v1/auth/me", 200},
		{"pcx_ana_search", "POST", "/api/v1/pocket/search?q=night", 405}, // reading only: the router has no such route
		{"pcx_ana_search", "GET", day, 403},
		{"pcx_ana_search", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_ana_search", "GET", "/api/v1/search?q=night", 403},
		{"pcx_ana_search", "GET", "/api/v1/albums/al1", 403},
		{"pcx_ana_search", "GET", "/api/v1/songs/s1", 403},
		{"pcx_ana_search", "GET", "/api/v1/stream/s1", 403},
		{"pcx_ana_search", "GET", "/api/v1/cover/al-al1", 403},
		{"pcx_ana_search", "GET", "/api/v1/playlists", 403},
		{"pcx_ana_search", "POST", "/api/v1/playlists", 403},
		{"pcx_ana_search", "POST", "/api/v1/listens", 403},
		{"pcx_ana_search", "GET", "/api/v1/history", 403},
		{"pcx_ana_day", "GET", day, 200},
		{"pcx_ana_day", "GET", day + "?tz=Europe/Sarajevo", 200},
		{"pcx_ana_day", "GET", "/api/v1/auth/me", 200},
		{"pcx_ana_day", "GET", "/api/v1/day/2026-11-20", 403},
		{"pcx_ana_day", "GET", "/api/v1/pocket/search?q=night", 403},
		{"pcx_ana_day", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_ana_day", "GET", "/api/v1/history", 403},
		{"pcx_ana_day", "GET", "/api/v1/stats", 403},
		{"pcx_ana_day", "DELETE", "/api/v1/listens/1", 403},
		{"pcx_ana_widget", "GET", "/api/v1/pocket/widget", 200},
		{"pcx_ana_widget", "GET", "/api/v1/auth/me", 200},
		{"pcx_ana_widget", "GET", "/api/v1/dashboard", 403},
		{"pcx_ana_widget", "GET", "/api/v1/devices", 403},
		{"pcx_ana_widget", "POST", "/api/v1/now", 403},
		{"pcx_ana_widget", "POST", "/api/v1/devices/web-1/command", 403},
		{"pcx_ana_widget", "GET", day, 403},
		{"pcx_ana_widget", "GET", "/api/v1/pocket/search?q=night", 403},
		// nothing can be kept here: the scope is not offered and there is no route
		{"pcx_ana_capture", "POST", "/api/v1/pocket/capture", 404},
		{"pcx_ana_capture", "GET", "/api/v1/pocket/search?q=night", 403},
		// the older scopes keep their own routes and do not open the new ones
		{"pcx_ana_read", "GET", "/api/v1/day/2026-11-20", 200},
		{"pcx_ana_read", "GET", day, 403},
		{"pcx_ana_read", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_ana_read", "GET", "/api/v1/pocket/search?q=night", 403},
		{"pcx_ana_playlists", "GET", "/api/v1/search?q=night", 200},
		{"pcx_ana_playlists", "GET", "/api/v1/pocket/search?q=night", 403},
		{"pcx_ana_playlists", "GET", "/api/v1/pocket/widget", 403},
		{"pcx_ana_play", "GET", "/api/v1/search?q=night", 200},
		{"pcx_ana_play", "GET", "/api/v1/pocket/search?q=night", 403},
		{"pcx_ana_play", "GET", day, 403},
	} {
		if code, body := rawCall(t, h, c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	// asked of linkAllows itself: each scope reads its one route and writes nothing
	for _, c := range []struct {
		scope, method, path string
		want                bool
	}{
		{core.ScopeSearch, "GET", core.SearchPath, true},
		{core.ScopeSearch, "POST", core.SearchPath, false},
		{core.ScopeDay, "GET", core.DayPath + "2026-11-20", true},
		{core.ScopeDay, "PUT", core.DayPath + "2026-11-20", false},
		{core.ScopeWidget, "GET", core.WidgetPath, true},
		{core.ScopeWidget, "DELETE", core.WidgetPath, false},
		{core.ScopeCapture, "POST", core.CapturePath, false},
		{core.ScopeSearch, "GET", "/api/v1/search", false},
		{core.ScopeDay, "GET", "/api/v1/day/2026-11-20", false},
		{"listening:read", "GET", core.WidgetPath, false},
		{"playlists:write", "GET", core.SearchPath, false},
	} {
		r, _ := http.NewRequest(c.method, c.path, nil)
		if got := linkAllows([]string{c.scope}, r); got != c.want {
			t.Errorf("linkAllows(%s, %s %s) = %v, want %v", c.scope, c.method, c.path, got, c.want)
		}
	}
	// the token answers for its person only
	if got := pocketFind(t, h, "pcx_ana_search", "q=night"); found(got) != "album Night Drive | song Blue Hour | song Neon Rain" {
		t.Errorf("Ana through a link = %s", found(got))
	}
	if got := pocketFind(t, h, "pcx_ben_search", "q=night"); len(got) != 0 {
		t.Errorf("Ben through a link found Ana's music: %s", found(got))
	}
	if d := pocketDayOf(t, h, "pcx_ana_day", "2026-11-20", "UTC"); len(d.Items) != 1 || d.Items[0].Title != "Blue Hour" {
		t.Errorf("Ana's day through a link = %+v", d)
	}
	if d := pocketDayOf(t, h, "pcx_ben_day", "2026-11-20", "UTC"); len(d.Items) != 0 {
		t.Errorf("Ben's day through a link = %+v", d)
	}
	if w := pocketWidgetOf(t, h, "pcx_ana_widget"); w.Line != "Last played: Blue Hour by Ana Band" {
		t.Errorf("Ana's widget through a link = %+v", w)
	}
	if w := pocketWidgetOf(t, h, "pcx_ben_widget"); w.Line != "" || len(w.Items) != 0 {
		t.Errorf("Ben's widget through a link = %+v", w)
	}
	// reading through a link recorded nothing and touched no playlist
	if _, hist := call(t, h, tok["ana"], "GET", "/api/v1/history", nil); hist["total"].(float64) != 1 {
		t.Errorf("plays after the link calls = %v", hist["total"])
	}
}

// The manifest tells Core the shelf, what can be asked here and the scopes
// behind it; a quick line is not among them.
func TestManifestCapabilities(t *testing.T) {
	s, _, _, manifest := capServer(t)
	s.cfg.CoreRegistrationKey = "pcr_test"
	if err := s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := manifest()
	if m["slug"] != "music" || m["category"] != "Media" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || day == nil || widget == nil || m["capture"] != nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, m["capture"])
	}
	if fmt.Sprint(search["bangs"]) != "[music song album]" || fmt.Sprint(search["kinds"]) != "[album artist song]" || search["what"] == "" {
		t.Errorf("search: %v", search)
	}
	if day["label"] != "Music" || day["one"] != "song" || day["many"] != "songs" || day["unit"] != "min" || day["what"] == "" {
		t.Errorf("day: %v", day)
	}
	if widget["title"] != "Recently played" || widget["what"] == "" {
		t.Errorf("widget: %v", widget)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"listening:read", "playlists:write", "music:play", core.ScopeSearch, core.ScopeDay, core.ScopeWidget, core.ScopeMoveExport, core.ScopeMoveImport} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
	if offered[core.ScopeCapture] {
		t.Error("pocket:capture is offered")
	}
}
