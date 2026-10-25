package server

import (
	"sort"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- typed live events (see live.go for the list) ----
//
// Each is published after the change is stored, with ids only: the pages
// that show the thing fetch it again. Personal events go to that person's
// streams only; lyrics are files on the box, the same for everyone.

// liveState holds what box-wide events need between calls.
type liveState struct {
	mu       sync.Mutex
	lyrics   map[int64]struct{} // lyrics files changed since the last flush
	armed    bool
	lastJob  map[string]time.Time // lyrics_job progress, per kind
	lastJobS map[string]string
}

const liveLyricsFlush = time.Second

func (s *Server) liveListens(uid int64, ls ...*store.Listen) {
	if len(ls) == 0 {
		return
	}
	ids, songs := make([]int64, 0, len(ls)), make([]string, 0, len(ls))
	for _, l := range ls {
		ids = append(ids, l.ID)
		songs = append(songs, l.SongID)
	}
	s.hub.send(uid, "listens", map[string]any{"ids": ids, "song_ids": songs})
}

func (s *Server) liveListenDeleted(uid, id int64) {
	s.hub.send(uid, "listens", map[string]any{"deleted": []int64{id}})
}

func (s *Server) liveListensImported(uid int64, n int) {
	if n > 0 {
		s.hub.send(uid, "listens", map[string]any{"imported": n})
	}
}

func (s *Server) liveStars(uid int64, kind string, ids []string, on bool) {
	if len(ids) > 0 {
		s.hub.send(uid, "stars", map[string]any{"kind": kind, "ids": ids, "on": on})
	}
}

func (s *Server) liveRatings(uid int64, kind string, ids []string, rating int) {
	if len(ids) > 0 {
		s.hub.send(uid, "ratings", map[string]any{"kind": kind, "ids": ids, "rating": rating})
	}
}

// livePlaylists: action is created, updated or deleted; smartID > 0 for a smart playlist.
func (s *Server) livePlaylists(uid int64, action string, ids []string, smartID int64) {
	v := map[string]any{"action": action, "ids": nz(ids)}
	if smartID > 0 {
		v["smart_id"] = smartID
	}
	s.hub.send(uid, "playlists", v)
}

func (s *Server) liveRequests(uid int64, action string, ids ...int64) {
	s.hub.send(uid, "requests", map[string]any{"action": action, "ids": nz(ids)})
}

// liveLyrics notes a changed lyrics file; changes are sent together once a
// second, so a run that writes thousands of files does not flood anyone.
func (s *Server) liveLyrics(fileID int64) {
	if fileID <= 0 {
		return
	}
	l := &s.live
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lyrics == nil {
		l.lyrics = map[int64]struct{}{}
	}
	l.lyrics[fileID] = struct{}{}
	if l.armed {
		return
	}
	l.armed = true
	time.AfterFunc(liveLyricsFlush, s.flushLiveLyrics)
}

func (s *Server) flushLiveLyrics() {
	l := &s.live
	l.mu.Lock()
	ids := make([]int64, 0, len(l.lyrics))
	for id := range l.lyrics {
		ids = append(ids, id)
	}
	l.lyrics, l.armed = nil, false
	l.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	v := map[string]any{"file_ids": ids}
	if len(ids) > 500 {
		v = map[string]any{"file_ids": []int64{}, "many": len(ids)} // refetch everything shown
	}
	s.hub.broadcast("lyrics", v, false)
}

// liveLyricsJob reports a lyrics run (admins) or the index refresh (everyone):
// a new state at once, progress at most every two seconds.
func (s *Server) liveLyricsJob(kind, state string, done, total int) {
	l := &s.live
	l.mu.Lock()
	if l.lastJob == nil {
		l.lastJob, l.lastJobS = map[string]time.Time{}, map[string]string{}
	}
	if state == l.lastJobS[kind] && time.Since(l.lastJob[kind]) < 2*time.Second {
		l.mu.Unlock()
		return
	}
	l.lastJob[kind], l.lastJobS[kind] = time.Now(), state
	l.mu.Unlock()
	s.hub.broadcast("lyrics_job", map[string]any{"kind": kind, "state": state, "done": done, "total": total}, kind == "run")
}

func nz[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}
