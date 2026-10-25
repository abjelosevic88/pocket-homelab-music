package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- live updates: devices, now playing, remote control ----
//
// Every open Pocket Music (browser tab, phone) is a device. It reports what
// it plays every few seconds; the person's other devices see it at once over
// server-sent events ("playing on iPhone"), can pause or skip it, or take
// the queue over ("play here"). Events never cross people.

type liveEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Live events carry ids and small facts, never whole objects: a page that
// shows the thing refetches it. Each goes only to the person it is about;
// box-wide facts (lyrics files, the lyrics jobs) to everyone who can see them.
//
//	devices / device / command      this person's devices (now playing, remote control)
//	sync         {state, progress, songs, …}   library index rebuild; state "idle" = refreshed
//	listens      {ids, song_ids} | {deleted: [ids]} | {imported: n}
//	stars        {kind, ids, on} | {refreshed: true}   (web, /rest star/unstar, server stars)
//	ratings      {kind, ids, rating}
//	playlists    {action: created|updated|deleted, ids, smart_id?}
//	requests     {ids, action: added|updated|deleted}  Lidarr album requests
//	lyrics       {file_ids}            a song's lyrics changed (everyone)
//	lyrics_job   {kind: run|index, state, done, total} (run: admins, index: everyone)
//	import       {id, state?, seen, imported}         history import progress
//	hello        {server_time}         first on every (re)connect: refetch what is shown

type liveSub struct {
	uid   int64
	admin bool
	ch    chan liveEvent
	check chan struct{} // look at the sign-in again now (logout, token deleted)
	gone  chan struct{} // closed: too slow, or the account is gone
	once  sync.Once
}

func (c *liveSub) stop() { c.once.Do(func() { close(c.gone) }) }

type hub struct {
	mu   sync.Mutex
	subs map[int64]map[*liveSub]struct{}
	n    int
}

const (
	liveMaxPerUser = 16   // tabs and phones of one person
	liveMaxTotal   = 2048 // the whole box
	liveBuffer     = 64   // events queued per stream before it counts as too slow
)

// liveKeepAlive is how often a comment keeps proxies from closing the stream,
// and how often the sign-in behind it is checked again.
var liveKeepAlive = 20 * time.Second

func newHub() *hub { return &hub{subs: map[int64]map[*liveSub]struct{}{}} }

// subscribe adds a stream for one person; false when there are too many.
func (h *hub) subscribe(uid int64, admin bool) (*liveSub, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs[uid]) >= liveMaxPerUser || h.n >= liveMaxTotal {
		return nil, false
	}
	c := &liveSub{uid: uid, admin: admin, ch: make(chan liveEvent, liveBuffer), check: make(chan struct{}, 1), gone: make(chan struct{})}
	if h.subs[uid] == nil {
		h.subs[uid] = map[*liveSub]struct{}{}
	}
	h.subs[uid][c] = struct{}{}
	h.n++
	return c, true
}

func (h *hub) unsubscribe(c *liveSub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[c.uid][c]; !ok {
		return
	}
	delete(h.subs[c.uid], c)
	h.n--
	if len(h.subs[c.uid]) == 0 {
		delete(h.subs, c.uid)
	}
}

// deliver queues ev without ever blocking: a stream that can't keep up is
// ended instead, and its page reconnects and refetches (nothing is lost).
func deliver(c *liveSub, ev liveEvent) {
	select {
	case <-c.gone:
	case c.ch <- ev:
	default:
		c.stop()
	}
}

// send delivers an event to every open device of one person.
func (h *hub) send(uid int64, typ string, data any) {
	ev := liveEvent{Type: typ, Data: data}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs[uid] {
		deliver(c, ev)
	}
}

// broadcast delivers a box-wide event (lyrics files, lyrics jobs) to everyone,
// or only to admins.
func (h *hub) broadcast(typ string, data any, adminsOnly bool) {
	ev := liveEvent{Type: typ, Data: data}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, cs := range h.subs {
		for c := range cs {
			if !adminsOnly || c.admin {
				deliver(c, ev)
			}
		}
	}
}

// recheck makes the person's streams verify their sign-in now.
func (h *hub) recheck(uid int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs[uid] {
		select {
		case c.check <- struct{}{}:
		default:
		}
	}
}

// drop ends every stream of a deleted account.
func (h *hub) drop(uid int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs[uid] {
		c.stop()
		h.n--
	}
	delete(h.subs, uid)
}

// handleEvents streams the person's live updates (server-sent events).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	rc := http.NewResponseController(w)
	c, ok := s.hub.subscribe(u.ID, u.Role == "admin")
	if !ok {
		writeErr(w, http.StatusTooManyRequests, "too many live connections")
		return
	}
	defer s.hub.unsubscribe(c)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(b string) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Second))
		if _, err := fmt.Fprint(w, b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	event := func(ev liveEvent) bool {
		b, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		return write("data: " + string(b) + "\n\n")
	}
	if !write("retry: 3000\n\n") ||
		!event(liveEvent{Type: "hello", Data: map[string]any{"server_time": time.Now().UnixMilli()}}) ||
		!event(liveEvent{Type: "devices", Data: s.deviceList(r.Context(), u.ID)}) {
		return
	}
	// signedIn: a revoked session, a deleted token or account ends the stream,
	// not only the next request.
	signedIn := func() bool {
		again, _, err := s.resolveAuth(r)
		if err != nil || again == nil || again.ID != u.ID {
			write("event: unauthorized\ndata: {}\n\n")
			return false
		}
		s.hub.mu.Lock()
		c.admin = again.Role == "admin"
		s.hub.mu.Unlock()
		return true
	}
	tick := time.NewTicker(liveKeepAlive)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.gone:
			return // too slow or the account is gone: the page reconnects and refetches
		case ev := <-c.ch:
			if !event(ev) {
				return
			}
		case <-c.check:
			if !signedIn() {
				return
			}
		case <-tick.C:
			if !signedIn() || !write(": keep-alive\n\n") {
				return
			}
		}
	}
}

// deviceView is a device with its song, for other devices.
type deviceView struct {
	store.Device
	Song   *store.Song `json:"song"`
	Active bool        `json:"active"` // reported in the last two minutes (an app: still inside the song)
	Length int         `json:"queue_length"`
	Kind   string      `json:"kind"`            // web | app (a Subsonic app through /rest: no remote control)
	Until  string      `json:"until,omitempty"` // an app's song ends about then, unless it says otherwise
}

// deviceParam is the {id} of a device route, decoded: a Subsonic app's id has a
// colon ("subsonic:amperfy"), which the browser escapes and the router keeps escaped.
func deviceParam(r *http.Request) string {
	id := chi.URLParam(r, "id")
	if v, err := url.PathUnescape(id); err == nil {
		return v
	}
	return id
}

// appDevice: a Subsonic app seen through the gateway. It reports once per song
// ("now playing"), never its position or a pause.
func appDevice(id string) bool { return strings.HasPrefix(id, "subsonic:") }

func (s *Server) deviceList(ctx context.Context, uid int64) []deviceView {
	ds, err := s.store.Devices(ctx, uid)
	if err != nil {
		return []deviceView{}
	}
	out := make([]deviceView, 0, len(ds))
	for _, d := range ds {
		out = append(out, s.viewDevice(ctx, uid, d))
	}
	return out
}

func (s *Server) viewDevice(ctx context.Context, uid int64, d store.Device) deviceView {
	v := deviceView{Device: d}
	if d.SongID != "" {
		v.Song, _ = s.store.GetSong(ctx, uid, d.SongID)
	}
	v.Kind = "web"
	if t, err := time.Parse(time.RFC3339Nano, d.UpdatedAt); err == nil {
		v.Active = time.Since(t) < 2*time.Minute
		if appDevice(d.ID) {
			// playing while the song can still be playing: from its start, its length and a little
			v.Kind = "app"
			end := t.Add(30 * time.Second)
			if v.Song != nil && v.Song.Duration > 0 {
				end = t.Add(time.Duration(v.Song.Duration)*time.Second + 30*time.Second)
			}
			v.Active = d.Playing && time.Now().Before(end)
			v.Playing = v.Active
			v.Until = end.UTC().Format(time.RFC3339Nano)
		}
	}
	var q []string
	if json.Unmarshal([]byte(d.Queue), &q) == nil {
		v.Length = len(q)
	}
	return v
}

// handleNow is a device's report: what plays, where in it, the queue when it changed.
func (s *Server) handleNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		DeviceID   string    `json:"device_id"`
		DeviceName string    `json:"device_name"`
		SongID     string    `json:"song_id"`
		Queue      *[]string `json:"queue"`
		QueueIndex int       `json:"queue_index"`
		PositionMS int64     `json:"position_ms"`
		Playing    bool      `json:"playing"`
		Started    bool      `json:"started"` // a new song began: tell the music server "now playing"
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.DeviceID) == "" {
		writeErr(w, http.StatusBadRequest, "device_id is required")
		return
	}
	d := &store.Device{ID: trim(in.DeviceID, 64), Name: trim(in.DeviceName, 80), SongID: in.SongID, QueueIndex: in.QueueIndex, PositionMS: max(0, in.PositionMS), Playing: in.Playing}
	if in.Queue != nil {
		q := *in.Queue
		if len(q) > 5000 {
			q = q[:5000]
		}
		b, _ := json.Marshal(q)
		d.Queue = string(b)
	}
	if err := s.store.SaveDevice(ctx, u.ID, d); err != nil {
		s.fail(w, err, "save device")
		return
	}
	if cur, err := s.store.GetDevice(ctx, u.ID, d.ID); err == nil {
		s.hub.send(u.ID, "device", s.viewDevice(ctx, u.ID, *cur))
	}
	if in.Started && in.SongID != "" {
		st, _ := s.store.GetSettings(ctx, u.ID)
		if st.Scrobble {
			go s.nowPlaying(u, in.SongID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) nowPlaying(u *store.User, songID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	mc, err := s.musicFor(ctx, u)
	if err != nil {
		return
	}
	if err := mc.c.Scrobble(ctx, songID, time.Time{}, false); err != nil {
		s.log.Debug("now playing", "err", err)
	}
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.deviceList(r.Context(), actorID(r.Context())))
}

// handleDeviceQueue returns a device's queue as songs (to take it over).
func (s *Server) handleDeviceQueue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	d, err := s.store.GetDevice(ctx, uid, deviceParam(r))
	if err != nil {
		s.fail(w, err, "device")
		return
	}
	var ids []string
	_ = json.Unmarshal([]byte(d.Queue), &ids)
	songs, err := s.songsFor(ctx, uid, ids)
	if err != nil {
		s.fail(w, err, "queue")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": s.viewDevice(ctx, uid, *d), "songs": songs})
}

// handleDeviceCommand forwards a remote-control command to one device.
func (s *Server) handleDeviceCommand(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	var in struct {
		Action string  `json:"action"` // play | pause | toggle | next | previous | seek | volume | stop
		Value  float64 `json:"value"`
		From   string  `json:"from"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	switch in.Action {
	case "play", "pause", "toggle", "next", "previous", "seek", "volume", "stop":
	default:
		writeErr(w, http.StatusBadRequest, "unknown action")
		return
	}
	s.hub.send(uid, "command", map[string]any{"target": deviceParam(r), "action": in.Action, "value": in.Value, "from": in.From})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	if err := s.store.DeleteDevice(r.Context(), uid, deviceParam(r)); err != nil {
		s.fail(w, err, "delete device")
		return
	}
	s.hub.send(uid, "devices", s.deviceList(r.Context(), uid))
	w.WriteHeader(http.StatusNoContent)
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
