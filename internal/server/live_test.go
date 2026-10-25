package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sseEvent is one event read from /api/v1/events.
type sseEvent struct {
	Type string         `json:"type"`
	Data map[string]any `json:"-"`
	Raw  json.RawMessage
}

// openEvents connects to the live stream and returns its events, in order;
// the channel closes when the server ends the stream.
func openEvents(t *testing.T, base, tok string) <-chan sseEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	out := make(chan sseEvent, 256)
	go func() {
		defer close(out)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		name := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				if name != "" {
					out <- sseEvent{Type: name}
					name = ""
					continue
				}
				var ev struct {
					Type string          `json:"type"`
					Data json.RawMessage `json:"data"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
					e := sseEvent{Type: ev.Type, Raw: ev.Data}
					_ = json.Unmarshal(ev.Data, &e.Data)
					out <- e
				}
			}
		}
	}()
	return out
}

// next waits for the next event that isn't about devices.
func next(t *testing.T, ch <-chan sseEvent, within time.Duration) (sseEvent, bool) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return sseEvent{}, false
			}
			if ev.Type == "devices" || ev.Type == "device" || ev.Type == "hello" {
				continue
			}
			return ev, true
		case <-deadline:
			return sseEvent{}, false
		}
	}
}

func expect(t *testing.T, ch <-chan sseEvent, typ string) sseEvent {
	t.Helper()
	ev, ok := next(t, ch, 3*time.Second)
	if !ok || ev.Type != typ {
		t.Fatalf("want a %q event, got %+v (ok=%v)", typ, ev, ok)
	}
	return ev
}

func TestLiveHubIsPerPersonAndDropsSlowStreams(t *testing.T) {
	h := newHub()
	ana, _ := h.subscribe(1, true)
	ben, _ := h.subscribe(2, false)
	h.send(1, "stars", map[string]any{"ids": []string{"s1"}})
	if len(ana.ch) != 1 || len(ben.ch) != 0 {
		t.Fatalf("ana %d, ben %d events: a person's event reached someone else", len(ana.ch), len(ben.ch))
	}
	h.broadcast("lyrics_job", nil, true)
	if len(ana.ch) != 2 || len(ben.ch) != 0 {
		t.Fatalf("an admins-only event reached a member: ana %d, ben %d", len(ana.ch), len(ben.ch))
	}
	h.broadcast("lyrics", nil, false)
	if len(ben.ch) != 1 {
		t.Fatalf("a box-wide event missed ben")
	}

	// a stream that doesn't read is ended, never waited for; the others carry on
	for i := 0; i < liveBuffer+5; i++ {
		h.send(1, "listens", i)
	}
	select {
	case <-ana.gone:
	default:
		t.Fatal("a full stream was not dropped")
	}
	select {
	case <-ben.gone:
		t.Fatal("ben's stream was dropped for ana's slowness")
	default:
	}

	// a cap per person
	for i := 0; i < liveMaxPerUser-1; i++ {
		if _, ok := h.subscribe(2, false); !ok {
			t.Fatalf("stream %d refused", i+2)
		}
	}
	if _, ok := h.subscribe(2, false); ok {
		t.Fatal("more streams than the cap for one person")
	}
	h.drop(2)
	select {
	case <-ben.gone:
	default:
		t.Fatal("drop left a deleted account's stream open")
	}
	if c, ok := h.subscribe(3, false); !ok || c == nil {
		t.Fatal("drop did not free the slots")
	}
}

func TestLiveEventsFromAppsAndWeb(t *testing.T) {
	_, _, tok, h := testServer(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close) // after the streams are cancelled

	anaEv := openEvents(t, srv.URL, tok["ana"])
	benEv := openEvents(t, srv.URL, tok["ben"])
	for _, ch := range []<-chan sseEvent{anaEv, benEv} {
		if ev := <-ch; ev.Type != "hello" {
			t.Fatalf("first event = %+v", ev)
		}
	}

	// a star from a Subsonic app through /rest
	_, out := call(t, h, tok["ana"], "POST", "/api/v1/subsonic/password", map[string]any{"username": "ana.p"})
	pw := out["password"].(string)
	q := tokenAuth("ana.p", pw)
	q.Set("id", "s1")
	if _, r := gatewayCall(t, h, "star", q); r["status"] != "ok" {
		t.Fatalf("star: %v", r)
	}
	ev := expect(t, anaEv, "stars")
	if ev.Data["on"] != true || ev.Data["ids"].([]any)[0] != "s1" {
		t.Fatalf("stars event = %v", ev.Data)
	}
	// and the index followed: the web's loved songs show it
	_, songs := call(t, h, tok["ana"], "GET", "/api/v1/songs?starred=1", nil)
	if list, _ := songs["songs"].([]any); len(list) != 1 {
		t.Fatalf("index after an app star: %v", songs)
	}

	// a rating from an app
	q = tokenAuth("ana.p", pw)
	q.Set("id", "s2")
	q.Set("rating", "4")
	gatewayCall(t, h, "setRating", q)
	if ev := expect(t, anaEv, "ratings"); ev.Data["rating"].(float64) != 4 {
		t.Fatalf("ratings event = %v", ev.Data)
	}

	// a play scrobbled by an app, then one from the web
	q = tokenAuth("ana.p", pw)
	q.Set("id", "s2")
	q.Set("submission", "true")
	gatewayCall(t, h, "scrobble", q)
	if ev := expect(t, anaEv, "listens"); ev.Data["song_ids"].([]any)[0] != "s2" {
		t.Fatalf("listens event = %v", ev.Data)
	}
	if code, _ := call(t, h, tok["ana"], "POST", "/api/v1/listens", map[string]any{"song_id": "s1", "ms_played": 60000}); code != 200 {
		t.Fatalf("add listen: %d", code)
	}
	ev = expect(t, anaEv, "listens")
	id := int64(ev.Data["ids"].([]any)[0].(float64))
	call(t, h, tok["ana"], "DELETE", "/api/v1/listens/"+strconv.FormatInt(id, 10), nil)
	if ev := expect(t, anaEv, "listens"); ev.Data["deleted"] == nil {
		t.Fatalf("delete event = %v", ev.Data)
	}

	// playlists, web and app
	call(t, h, tok["ana"], "POST", "/api/v1/playlists", map[string]any{"name": "Drive", "song_ids": []string{"s1"}})
	if ev := expect(t, anaEv, "playlists"); ev.Data["action"] != "created" {
		t.Fatalf("playlists event = %v", ev.Data)
	}
	q = tokenAuth("ana.p", pw)
	q.Set("name", "From the phone")
	gatewayCall(t, h, "createPlaylist", q)
	expect(t, anaEv, "playlists")

	// ben heard none of it: his first event is his own play
	if code, _ := call(t, h, tok["ben"], "POST", "/api/v1/listens", map[string]any{"song_id": "s9"}); code != 200 {
		t.Fatalf("ben's listen: %d", code)
	}
	if ev := expect(t, benEv, "listens"); ev.Data["song_ids"].([]any)[0] != "s9" {
		t.Fatalf("ben got %v", ev.Data)
	}
	if ev, ok := next(t, anaEv, 200*time.Millisecond); ok {
		t.Fatalf("ana got ben's event: %+v", ev)
	}
}

func TestLiveStreamEndsWhenSignInIsRevoked(t *testing.T) {
	s, _, tok, h := testServer(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close) // after the streams are cancelled
	ctx := context.Background()
	ana, _ := s.store.GetUserByAPIToken(ctx, tok["ana"])

	// deleted in Settings: the stream ends at once
	plain, info, _ := s.store.CreateAPIToken(ctx, ana.ID, "phone")
	ch := openEvents(t, srv.URL, plain)
	<-ch
	call(t, h, tok["ana"], "DELETE", "/api/v1/tokens/"+strconv.FormatInt(info.ID, 10), nil)
	if ev, ok := next(t, ch, 3*time.Second); !ok || ev.Type != "unauthorized" {
		t.Fatalf("after deleting the token: %+v %v", ev, ok)
	}
	if _, ok := next(t, ch, 3*time.Second); ok {
		t.Fatal("stream still open after unauthorized")
	}

	// revoked behind its back: the next keep-alive notices
	old := liveKeepAlive
	liveKeepAlive = 50 * time.Millisecond
	defer func() { liveKeepAlive = old }()
	plain, info, _ = s.store.CreateAPIToken(ctx, ana.ID, "tablet")
	ch = openEvents(t, srv.URL, plain)
	<-ch
	_ = s.store.DeleteAPIToken(ctx, ana.ID, info.ID)
	if ev, ok := next(t, ch, 3*time.Second); !ok || ev.Type != "unauthorized" {
		t.Fatalf("after revoking: %+v %v", ev, ok)
	}
}

// keep-alives keep the stream open: it is not cut like ordinary requests
func TestLiveStreamOutlivesKeepAlives(t *testing.T) {
	s, _, tok, h := testServer(t)
	old := liveKeepAlive
	liveKeepAlive = 20 * time.Millisecond
	defer func() { liveKeepAlive = old }()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ch := openEvents(t, srv.URL, tok["ana"])
	<-ch
	if ev, ok := next(t, ch, 300*time.Millisecond); ok {
		t.Fatalf("unexpected event %+v", ev)
	}
	ana, _ := s.store.GetUserByAPIToken(context.Background(), tok["ana"])
	s.hub.send(ana.ID, "requests", map[string]any{"action": "updated"})
	expect(t, ch, "requests")
}
