package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- plays ----

// tzOffset is the person's clock (minutes east of UTC) from ?tz=, else the server's.
func tzOffset(r *http.Request) int {
	if v := r.URL.Query().Get("tz"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= -14*60 && n <= 14*60 {
			return n
		}
	}
	_, off := time.Now().Zone()
	return off / 60
}

func zone(offMin int) *time.Location { return time.FixedZone("", offMin*60) }

// parseWhen reads a play time: unix milliseconds or RFC 3339.
func parseWhen(v any) (time.Time, bool) {
	switch x := v.(type) {
	case float64:
		if x > 1e12 {
			return time.UnixMilli(int64(x)), true
		}
		if x > 1e9 {
			return time.Unix(int64(x), 0), true
		}
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// handleAddListen records a finished (or long enough) play and passes it on
// to the music server, which forwards it to Last.fm / ListenBrainz / Maloja.
func (s *Server) handleAddListen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		SongID   string `json:"song_id"`
		PlayedAt any    `json:"played_at"`
		MSPlayed int64  `json:"ms_played"`
		Device   string `json:"device"`
	}
	if err := decode(r, &in); err != nil || in.SongID == "" {
		writeErr(w, http.StatusBadRequest, "song_id is required")
		return
	}
	at, ok := parseWhen(in.PlayedAt)
	if !ok || at.After(time.Now().Add(5*time.Minute)) {
		at = time.Now().Add(-time.Duration(in.MSPlayed) * time.Millisecond)
	}
	songs, err := s.songsFor(ctx, u.ID, []string{in.SongID})
	if err != nil {
		s.fail(w, err, "song")
		return
	}
	if len(songs) == 0 {
		writeErr(w, http.StatusNotFound, "song not found")
		return
	}
	so := songs[0]
	l := &store.Listen{SongID: so.ID, Title: so.Title, Artist: so.Artist, Album: so.Album, AlbumID: so.AlbumID, ArtistID: firstNonEmpty(so.ArtistID, so.AlbumArtistID),
		Genre: so.Genre, Duration: so.Duration, PlayedAt: store.Stamp(at), MSPlayed: max(0, in.MSPlayed), Source: "web", Device: trim(in.Device, 80)}
	if linkApp(ctx) != "" {
		l.Source = "api"
	}
	added, err := s.store.AddListen(ctx, u.ID, l)
	if err != nil {
		s.fail(w, err, "save play")
		return
	}
	if added {
		st, _ := s.store.GetSettings(ctx, u.ID)
		if st.Scrobble {
			go func() {
				c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if mc, err := s.musicFor(c, u); err == nil {
					if err := mc.c.Scrobble(c, so.ID, at, true); err != nil {
						s.log.Debug("scrobble", "err", err)
					}
				}
			}()
		}
		s.hub.send(u.ID, "listen", l)
	}
	writeJSON(w, http.StatusOK, map[string]any{"listen": l, "added": added})
}

func (s *Server) handleDeleteListen(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteListen(r.Context(), actorID(r.Context()), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete play")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHistory lists plays, newest first, a page of days at a time (?before=YYYY-MM-DD).
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	tz := zone(tzOffset(r))
	to := ""
	if b := r.URL.Query().Get("before"); b != "" {
		if t, err := time.ParseInLocation(time.RFC3339, b, time.UTC); err == nil {
			to = store.Stamp(t)
		} else if d, err := time.ParseInLocation("2006-01-02", b, tz); err == nil {
			to = store.Stamp(d)
		}
	}
	limit := queryInt(r, "limit")
	if limit <= 0 {
		limit = 150
	}
	ls, err := s.store.Listens(ctx, uid, "", to, limit, r.URL.Query().Get("q"))
	if err != nil {
		s.fail(w, err, "history")
		return
	}
	n, first := s.store.CountListens(ctx, uid)
	writeJSON(w, http.StatusOK, map[string]any{"listens": ls, "total": n, "first": first, "more": len(ls) == limit})
}

// ---- stats ----

type period struct {
	Key, Label string
	From, To   time.Time
}

// periodFor turns ?period=week|month|year|all|YYYY|YYYY-MM into a range in the person's time zone.
func periodFor(r *http.Request, now time.Time) period {
	tz := now.Location()
	p := r.URL.Query().Get("period")
	today := dayStart(now)
	switch {
	case p == "" || p == "month":
		return period{"month", "Last 30 days", today.AddDate(0, 0, -29), today.AddDate(0, 0, 1)}
	case p == "week":
		return period{"week", "Last 7 days", today.AddDate(0, 0, -6), today.AddDate(0, 0, 1)}
	case p == "year":
		return period{"year", "Last 12 months", today.AddDate(-1, 0, 1), today.AddDate(0, 0, 1)}
	case p == "all":
		return period{"all", "All time", time.Date(1970, 1, 1, 0, 0, 0, 0, tz), today.AddDate(0, 0, 1)}
	case len(p) == 4:
		if y, err := strconv.Atoi(p); err == nil {
			return period{p, p, time.Date(y, 1, 1, 0, 0, 0, 0, tz), time.Date(y+1, 1, 1, 0, 0, 0, 0, tz)}
		}
	case len(p) == 7:
		if t, err := time.ParseInLocation("2006-01", p, tz); err == nil {
			return period{p, t.Format("January 2006"), t, t.AddDate(0, 1, 0)}
		}
	}
	return period{"month", "Last 30 days", today.AddDate(0, 0, -29), today.AddDate(0, 0, 1)}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	off := tzOffset(r)
	now := time.Now().In(zone(off))
	p := periodFor(r, now)
	from, to := store.Stamp(p.From), store.Stamp(p.To)
	totals, err := s.store.PeriodTotals(ctx, uid, from, to, off)
	if err != nil {
		s.fail(w, err, "stats")
		return
	}
	// the same length of time just before, for "+12 % vs the previous 30 days"
	span := p.To.Sub(p.From)
	prev := store.Totals{}
	if p.Key != "all" {
		prev, _ = s.store.PeriodTotals(ctx, uid, store.Stamp(p.From.Add(-span)), from, off)
	}
	n := max(queryInt(r, "limit"), 10)
	artists, _ := s.store.TopBy(ctx, uid, "artist", from, to, n)
	albums, _ := s.store.TopBy(ctx, uid, "album", from, to, n)
	songs, _ := s.store.TopBy(ctx, uid, "song", from, to, n)
	genres, _ := s.store.TopBy(ctx, uid, "genre", from, to, 12)
	series := "day"
	if span > 120*24*time.Hour {
		series = "month"
	}
	timeline, _ := s.store.Series(ctx, uid, series, from, to, off)
	hours, _ := s.store.Series(ctx, uid, "hour", from, to, off)
	weekdays, _ := s.store.Series(ctx, uid, "weekday", from, to, off)
	n2, first := s.store.CountListens(ctx, uid)
	// years with plays, for the period picker and "Your year in music"
	years := []string{}
	if first != "" {
		if t, err := time.Parse(time.RFC3339Nano, first); err == nil {
			for y := now.Year(); y >= t.Year(); y-- {
				years = append(years, strconv.Itoa(y))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"period": map[string]any{"key": p.Key, "label": p.Label, "from": p.From.Format("2006-01-02"), "to": p.To.AddDate(0, 0, -1).Format("2006-01-02")},
		"totals": totals, "previous": prev, "artists": artists, "albums": albums, "songs": songs, "genres": genres,
		"timeline": timeline, "timeline_by": series, "hours": hours, "weekdays": weekdays,
		"streak": s.streak(ctx, uid, off, now), "all_plays": n2, "first_play": first, "years": years,
	})
}

// streak is the run of consecutive days with music, ending today or yesterday, and the longest ever.
func (s *Server) streak(ctx context.Context, uid int64, off int, now time.Time) map[string]int {
	days, _ := s.store.ListenDays(ctx, uid, off, 4000)
	if len(days) == 0 {
		return map[string]int{"current": 0, "longest": 0}
	}
	current, longest, run := 0, 0, 0
	var prev time.Time
	for i, d := range days {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			continue
		}
		if i == 0 || prev.Sub(t) != 24*time.Hour {
			run = 1
		} else {
			run++
		}
		longest = max(longest, run)
		prev = t
	}
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	if days[0] == today || days[0] == yesterday {
		current = 1
		p, _ := time.Parse("2006-01-02", days[0])
		for _, d := range days[1:] {
			t, err := time.Parse("2006-01-02", d)
			if err != nil || p.Sub(t) != 24*time.Hour {
				break
			}
			current++
			p = t
		}
	}
	return map[string]int{"current": current, "longest": longest}
}

// handleRecap is "your year in music" (or a month): the shape of a Wrapped story.
func (s *Server) handleRecap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	off := tzOffset(r)
	now := time.Now().In(zone(off))
	p := periodFor(r, now)
	from, to := store.Stamp(p.From), store.Stamp(p.To)
	totals, err := s.store.PeriodTotals(ctx, uid, from, to, off)
	if err != nil {
		s.fail(w, err, "recap")
		return
	}
	artists, _ := s.store.TopBy(ctx, uid, "artist", from, to, 5)
	songs, _ := s.store.TopBy(ctx, uid, "song", from, to, 5)
	albums, _ := s.store.TopBy(ctx, uid, "album", from, to, 5)
	genres, _ := s.store.TopBy(ctx, uid, "genre", from, to, 5)
	hours, _ := s.store.Series(ctx, uid, "hour", from, to, off)
	months, _ := s.store.Series(ctx, uid, "month", from, to, off)
	days, _ := s.store.Series(ctx, uid, "day", from, to, off)
	peakHour, peakMonth, bigDay := "", "", store.Bucket{}
	best := 0
	for _, h := range hours {
		if h.Plays > best {
			best, peakHour = h.Plays, h.Key
		}
	}
	best = 0
	for _, m := range months {
		if m.Plays > best {
			best, peakMonth = m.Plays, m.Key
		}
	}
	for _, d := range days {
		if d.Seconds > bigDay.Seconds {
			bigDay = d
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"period": map[string]any{"key": p.Key, "label": p.Label}, "totals": totals,
		"artists": artists, "songs": songs, "albums": albums, "genres": genres, "peak_hour": peakHour, "peak_month": peakMonth, "biggest_day": bigDay,
		"streak": s.streak(ctx, uid, off, now)})
}

// ---- Pocket Day, Homepage, the sidebar ----

// handleDay is one day's listening for Pocket Day: minutes, top artists, the songs.
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	off := tzOffset(r)
	d, err := time.ParseInLocation("2006-01-02", chi.URLParam(r, "date"), zone(off))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "date is YYYY-MM-DD")
		return
	}
	from, to := store.Stamp(d), store.Stamp(d.AddDate(0, 0, 1))
	totals, _ := s.store.PeriodTotals(ctx, uid, from, to, off)
	ls, err := s.store.Listens(ctx, uid, from, to, 1000, "")
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].PlayedAt < ls[j].PlayedAt })
	artists, _ := s.store.TopBy(ctx, uid, "artist", from, to, 5)
	albums, _ := s.store.TopBy(ctx, uid, "album", from, to, 5)
	summary := ""
	if totals.Plays > 0 {
		names := []string{}
		for _, a := range artists {
			if len(names) == 3 {
				break
			}
			names = append(names, a.Name)
		}
		summary = fmt.Sprintf("Listened to %s of music (%d songs), mostly %s.", humanMinutes(totals.Seconds), totals.Plays, joinAnd(names))
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": d.Format("2006-01-02"), "summary": summary, "totals": totals, "artists": artists, "albums": albums, "listens": ls})
}

func humanMinutes(sec int64) string {
	m := sec / 60
	if m < 60 {
		return fmt.Sprintf("%d min", m)
	}
	h, m := m/60, m%60
	if m == 0 {
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// handleDashboard is the Homepage widget and the sidebar badges.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	off := tzOffset(r)
	now := time.Now().In(zone(off))
	today := dayStart(now)
	day, _ := s.store.PeriodTotals(ctx, uid, store.Stamp(today), store.Stamp(today.AddDate(0, 0, 1)), off)
	week, _ := s.store.PeriodTotals(ctx, uid, store.Stamp(today.AddDate(0, 0, -6)), store.Stamp(today.AddDate(0, 0, 1)), off)
	top, _ := s.store.TopBy(ctx, uid, "artist", store.Stamp(today.AddDate(0, 0, -6)), store.Stamp(today.AddDate(0, 0, 1)), 1)
	lib := s.store.LibraryTotals(ctx, uid)
	open, _ := s.store.Requests(ctx, uid, true)
	smart, _ := s.store.ListSmart(ctx, uid)
	playing := "—"
	for _, d := range s.deviceList(ctx, uid) {
		if d.Active && d.Playing && d.Song != nil {
			playing = d.Song.Artist + " — " + d.Song.Title
			break
		}
	}
	topArtist := "—"
	if len(top) > 0 {
		topArtist = top[0].Name
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"now_playing": playing, "minutes_today": day.Seconds / 60, "plays_today": day.Plays, "minutes_week": week.Seconds / 60, "plays_week": week.Plays,
		"top_artist_week": topArtist, "songs": lib["songs"], "albums": lib["albums"], "artists": lib["artists"], "starred": lib["starred"],
		"requests_open": len(open), "smart_playlists": len(smart), "streak": s.streak(ctx, uid, off, now)["current"],
	})
}
