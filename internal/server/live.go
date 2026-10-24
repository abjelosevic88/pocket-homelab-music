package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

type hub struct {
	mu   sync.Mutex
	subs map[int64]map[chan liveEvent]bool
}

func newHub() *hub { return &hub{subs: map[int64]map[chan liveEvent]bool{}} }

func (h *hub) subscribe(uid int64) chan liveEvent {
	ch := make(chan liveEvent, 32)
	h.mu.Lock()
	if h.subs[uid] == nil {
		h.subs[uid] = map[chan liveEvent]bool{}
	}
	h.subs[uid][ch] = true
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(uid int64, ch chan liveEvent) {
	h.mu.Lock()
	delete(h.subs[uid], ch)
	if len(h.subs[uid]) == 0 {
		delete(h.subs, uid)
	}
	h.mu.Unlock()
}

// send delivers an event to every open device of one person (never blocks).
func (h *hub) send(uid int64, typ string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[uid] {
		select {
		case ch <- liveEvent{Type: typ, Data: data}:
		default: // a slow tab misses an update; the next one carries the state again
		}
	}
}

func (h *hub) drop(uid int64) {
	h.mu.Lock()
	delete(h.subs, uid)
	h.mu.Unlock()
}

// handleEvents streams the person's live updates.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	uid := actorID(r.Context())
	ch := s.hub.subscribe(uid)
	defer s.hub.unsubscribe(uid, ch)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	write := func(ev liveEvent) {
		b, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	write(liveEvent{Type: "devices", Data: s.deviceList(r.Context(), uid)})
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-ch:
			write(ev)
		case <-tick.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			fl.Flush()
		}
	}
}

// deviceView is a device with its song, for other devices.
type deviceView struct {
	store.Device
	Song   *store.Song `json:"song"`
	Active bool        `json:"active"` // reported in the last two minutes (an app: still inside the song)
	Length int         `json:"queue_length"`
	Kind   string      `json:"kind"`        // web | app (a Subsonic app through /rest: no remote control)
	Until  string      `json:"until,omitempty"` // an app's song ends about then, unless it says otherwise
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
	d, err := s.store.GetDevice(ctx, uid, chi.URLParam(r, "id"))
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
	s.hub.send(uid, "command", map[string]any{"target": chi.URLParam(r, "id"), "action": in.Action, "value": in.Value, "from": in.From})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	uid := actorID(r.Context())
	if err := s.store.DeleteDevice(r.Context(), uid, chi.URLParam(r, "id")); err != nil {
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
