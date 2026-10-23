package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// handleHome is everything the Home page shows, in one call.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	off := tzOffset(r)
	now := time.Now().In(zone(off))
	sync := s.store.Sync(ctx, uid)
	out := map[string]any{"sync": sync, "syncing": s.isSyncing(uid), "totals": s.store.LibraryTotals(ctx, uid)}
	if sync.SyncedAt == "" {
		// nothing indexed yet: say why (not connected, or the first sync is running)
		if _, err := s.musicFor(ctx, userFrom(ctx)); err != nil {
			out["not_connected"] = err.Error()
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["jump_back"] = s.albumsFromListens(ctx, uid, `SELECT album_id, MAX(played_at) m FROM listens WHERE user_id = ? AND album_id <> '' GROUP BY album_id ORDER BY m DESC LIMIT 12`, uid)
	added, _, _ := s.store.ListAlbums(ctx, uid, store.AlbumFilter{Sort: "added", Limit: 18})
	out["recently_added"] = added
	md := now.Format("-01-02")
	out["on_this_day"] = s.albumsFromListens(ctx, uid, `SELECT album_id, COUNT(1) n FROM listens WHERE user_id = ? AND album_id <> '' AND substr(played_at, 5, 6) = ? AND played_at < ? GROUP BY album_id ORDER BY n DESC LIMIT 10`,
		uid, md, store.Stamp(now.AddDate(0, -11, 0)))
	out["rediscover"] = s.albumsFromListens(ctx, uid, `SELECT album_id, COUNT(1) n FROM listens WHERE user_id = ? AND album_id <> '' GROUP BY album_id HAVING n >= 5 AND MAX(played_at) < ? ORDER BY n DESC LIMIT 12`,
		uid, store.Stamp(now.AddDate(0, -6, 0)))
	seed := int64(now.YearDay()*1000 + int(uid))
	unplayed, _, _ := s.store.ListAlbums(ctx, uid, store.AlbumFilter{Sort: "random", Unplayed: true, Seed: seed, Limit: 12})
	out["never_played"] = unplayed
	today := dayStart(now)
	topArtists, _ := s.store.TopBy(ctx, uid, "artist", store.Stamp(today.AddDate(0, 0, -29)), store.Stamp(today.AddDate(0, 0, 1)), 10)
	out["top_artists"] = topArtists
	if mixes, err := s.mixesFor(ctx, uid, off); err == nil {
		out["mixes"] = mixes
	}
	reqs, _ := s.store.Requests(ctx, uid, false)
	var recent []store.Request
	for _, rq := range reqs {
		if rq.State != "arrived" || rq.ArrivedAt > store.Stamp(now.AddDate(0, 0, -7)) {
			recent = append(recent, rq)
		}
	}
	out["requests"] = head(recent, 8)
	day, _ := s.store.PeriodTotals(ctx, uid, store.Stamp(today), store.Stamp(today.AddDate(0, 0, 1)), off)
	out["today"] = day
	out["streak"] = s.streak(ctx, uid, off, now)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) albumsFromListens(ctx context.Context, uid int64, q string, args ...any) []store.Album {
	rows, err := s.store.DB.QueryContext(ctx, q, args...)
	if err != nil {
		s.log.Warn("home", "err", err)
		return []store.Album{}
	}
	var ids []string
	for rows.Next() {
		var id string
		var x any
		if rows.Scan(&id, &x) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	as, _ := s.store.AlbumsByID(ctx, uid, ids)
	s.store.FillAlbumPlays(ctx, uid, as)
	return as
}

// weeklyRecaps sends each person who asked for it a short summary on Monday morning.
func (s *Server) weeklyRecaps(ctx context.Context) {
	now := time.Now()
	if now.Weekday() != time.Monday || now.Hour() < 9 {
		return
	}
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	week := now.Format("2006-W") + fmt.Sprint(isoWeek(now))
	for _, uid := range ids {
		st, _ := s.store.GetSettings(ctx, uid)
		if !st.NotifyWeekly || st.NotifyURL == "" {
			continue
		}
		key := fmt.Sprintf("recap:%d:%s", uid, week)
		if _, _, done := s.store.CacheGet(ctx, key); done {
			continue
		}
		_ = s.store.CachePut(ctx, key, "1")
		today := dayStart(now)
		from, to := store.Stamp(today.AddDate(0, 0, -7)), store.Stamp(today)
		t, _ := s.store.PeriodTotals(ctx, uid, from, to, 0)
		if t.Plays == 0 {
			continue
		}
		artists, _ := s.store.TopBy(ctx, uid, "artist", from, to, 3)
		names := []string{}
		for _, a := range artists {
			names = append(names, a.Name)
		}
		body := fmt.Sprintf("%s of music last week, %d songs (%d new to you). Most played: %s.", humanMinutes(t.Seconds), t.Plays, t.NewSongs, joinAnd(names))
		if err := s.notifyUser(ctx, st, "Your week in music", body, "musical_note"); err != nil {
			s.log.Warn("weekly recap", "user", uid, "err", err)
		}
	}
}

func isoWeek(t time.Time) int {
	_, w := t.ISOWeek()
	return w
}
