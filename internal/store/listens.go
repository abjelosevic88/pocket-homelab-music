package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// StampFormat is how play times are stored: UTC to the second, so string
// order is time order and the same play from two sources compares equal.
const StampFormat = "2006-01-02T15:04:05Z"

// Stamp formats a play time.
func Stamp(t time.Time) string { return t.UTC().Format(StampFormat) }

// AddListen records a play. The same song started at the same second is
// recorded once (a retried request, an import overlapping a live play).
// It returns false when the play was already there.
func (s *Store) AddListen(ctx context.Context, uid int64, l *Listen) (bool, error) {
	return addListen(ctx, s.DB, uid, l)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func addListen(ctx context.Context, db execer, uid int64, l *Listen) (bool, error) {
	if l.Source == "" {
		l.Source = "web"
	}
	res, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO listens (user_id, song_id, title, artist, album, album_id, artist_id, genre, duration, played_at, ms_played, source, device)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, uid, l.SongID, l.Title, l.Artist, l.Album, l.AlbumID, l.ArtistID, l.Genre, l.Duration, l.PlayedAt, l.MSPlayed, l.Source, l.Device)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return false, nil
	}
	l.ID, _ = res.LastInsertId()
	return true, nil
}

// AddListens imports many plays in one transaction and returns how many were new.
func (s *Store) AddListens(ctx context.Context, uid int64, ls []Listen) (int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range ls {
		ok, err := addListen(ctx, tx, uid, &ls[i])
		if err != nil {
			_ = tx.Rollback()
			return 0, err
		}
		if ok {
			n++
		}
	}
	return n, tx.Commit()
}

const listenCols = `id, song_id, title, artist, album, album_id, artist_id, genre, duration, played_at, ms_played, source, device, created_at`

func scanListen(row interface{ Scan(...any) error }) (Listen, error) {
	var l Listen
	err := row.Scan(&l.ID, &l.SongID, &l.Title, &l.Artist, &l.Album, &l.AlbumID, &l.ArtistID, &l.Genre, &l.Duration, &l.PlayedAt, &l.MSPlayed, &l.Source, &l.Device, &l.CreatedAt)
	return l, err
}

// Listens returns plays between two UTC times (newest first), optionally only
// before a cursor id.
func (s *Store) Listens(ctx context.Context, uid int64, from, to string, limit int, q string) ([]Listen, error) {
	where := []string{"user_id = ?"}
	args := []any{uid}
	if from != "" {
		where = append(where, "played_at >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "played_at < ?")
		args = append(args, to)
	}
	if q = strings.TrimSpace(q); q != "" {
		where = append(where, "(title LIKE ? OR artist LIKE ? OR album LIKE ?)")
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+listenCols+` FROM listens WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY played_at DESC LIMIT %d`, limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Listen{}
	for rows.Next() {
		l, err := scanListen(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// DeleteListen removes one play of the person.
func (s *Store) DeleteListen(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM listens WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// LastListen is the person's most recent play.
func (s *Store) LastListen(ctx context.Context, uid int64) (*Listen, error) {
	l, err := scanListen(s.DB.QueryRowContext(ctx, `SELECT `+listenCols+` FROM listens WHERE user_id = ? ORDER BY played_at DESC LIMIT 1`, uid))
	if err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

// CountListens counts the person's plays (all sources).
func (s *Store) CountListens(ctx context.Context, uid int64) (int, string) {
	var n int
	var first sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1), MIN(played_at) FROM listens WHERE user_id = ?`, uid).Scan(&n, &first)
	return n, first.String
}

// ---- stats ----

// Top is one row of a top list.
type Top struct {
	Key      string `json:"key"` // song/album/artist id when known
	Name     string `json:"name"`
	Artist   string `json:"artist,omitempty"`
	Album    string `json:"album,omitempty"`
	AlbumID  string `json:"album_id,omitempty"`
	ArtistID string `json:"artist_id,omitempty"`
	Plays    int    `json:"plays"`
	Seconds  int64  `json:"seconds"`
}

// listened is the seconds a play counts for: what was played, else the song length.
const listened = `CASE WHEN ms_played > 0 THEN ms_played / 1000 ELSE duration END`

// TopBy lists the most played artists, albums, songs or genres between two UTC times.
func (s *Store) TopBy(ctx context.Context, uid int64, by, from, to string, limit int) ([]Top, error) {
	var sel, group string
	switch by {
	case "artist":
		sel, group = `MAX(artist_id), artist, '', '', '', MAX(artist_id)`, `lower(artist)`
	case "album":
		sel, group = `MAX(album_id), album, MAX(artist), album, MAX(album_id), MAX(artist_id)`, `lower(album), lower(artist)`
	case "song":
		sel, group = `MAX(song_id), title, MAX(artist), MAX(album), MAX(album_id), MAX(artist_id)`, `lower(title), lower(artist)`
	case "genre":
		sel, group = `genre, genre, '', '', '', ''`, `genre`
	default:
		return nil, fmt.Errorf("unknown top list %q", by)
	}
	where := "user_id = ? AND played_at >= ? AND played_at < ?"
	if by == "genre" {
		where += " AND genre <> ''"
	}
	if by == "album" {
		where += " AND album <> ''"
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sel+`, COUNT(1) n, SUM(`+listened+`) FROM listens WHERE `+where+` GROUP BY `+group+fmt.Sprintf(` ORDER BY n DESC, MAX(played_at) DESC LIMIT %d`, limit), uid, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Top{}
	for rows.Next() {
		var t Top
		if err := rows.Scan(&t.Key, &t.Name, &t.Artist, &t.Album, &t.AlbumID, &t.ArtistID, &t.Plays, &t.Seconds); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Totals are the sums for a period.
type Totals struct {
	Plays    int   `json:"plays"`
	Seconds  int64 `json:"seconds"`
	Artists  int   `json:"artists"`
	Albums   int   `json:"albums"`
	Songs    int   `json:"songs"`
	Days     int   `json:"days"` // days with at least one play
	NewSongs int   `json:"new_songs"`
}

// PeriodTotals sums plays between two UTC times. tz shifts days to the person's clock.
func (s *Store) PeriodTotals(ctx context.Context, uid int64, from, to string, tzOffsetMin int) (Totals, error) {
	var t Totals
	mod := fmt.Sprintf("%+d minutes", tzOffsetMin)
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(`+listened+`), 0), COUNT(DISTINCT lower(artist)), COUNT(DISTINCT lower(album) || '|' || lower(artist)),
		COUNT(DISTINCT lower(title) || '|' || lower(artist)), COUNT(DISTINCT date(played_at, ?))
		FROM listens WHERE user_id = ? AND played_at >= ? AND played_at < ?`, mod, uid, from, to).Scan(&t.Plays, &t.Seconds, &t.Artists, &t.Albums, &t.Songs, &t.Days)
	if err != nil {
		return t, err
	}
	// songs heard for the first time in this period
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM (SELECT lower(title) || '|' || lower(artist) k, MIN(played_at) first FROM listens WHERE user_id = ? GROUP BY k) WHERE first >= ? AND first < ?`, uid, from, to).Scan(&t.NewSongs)
	return t, nil
}

// Bucket is one bar of a time series.
type Bucket struct {
	Key     string `json:"key"`
	Plays   int    `json:"plays"`
	Seconds int64  `json:"seconds"`
}

// Series groups plays by day, month, hour of day or weekday (in the person's time zone).
func (s *Store) Series(ctx context.Context, uid int64, by, from, to string, tzOffsetMin int) ([]Bucket, error) {
	mod := fmt.Sprintf("%+d minutes", tzOffsetMin)
	var key string
	switch by {
	case "day":
		key = `date(played_at, ?)`
	case "month":
		key = `strftime('%Y-%m', played_at, ?)`
	case "hour":
		key = `strftime('%H', played_at, ?)`
	case "weekday":
		key = `strftime('%w', played_at, ?)`
	default:
		return nil, fmt.Errorf("unknown series %q", by)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+key+` k, COUNT(1), COALESCE(SUM(`+listened+`), 0) FROM listens WHERE user_id = ? AND played_at >= ? AND played_at < ? GROUP BY k ORDER BY k`, mod, uid, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Key, &b.Plays, &b.Seconds); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListenDays returns every local day (YYYY-MM-DD) with a play, newest first (for streaks).
func (s *Store) ListenDays(ctx context.Context, uid int64, tzOffsetMin int, limit int) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT date(played_at, ?) d FROM listens WHERE user_id = ? ORDER BY d DESC LIMIT ?`, fmt.Sprintf("%+d minutes", tzOffsetMin), uid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SongStat is a song's play history summary.
type SongStat struct {
	Plays  int
	Recent int // plays in the recent window
	First  string
	Last   string
}

// SongStats returns per-song play summaries (by song id), with "recent"
// counting plays since recentFrom.
func (s *Store) SongStats(ctx context.Context, uid int64, recentFrom string) (map[string]SongStat, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT song_id, COUNT(1), SUM(CASE WHEN played_at >= ? THEN 1 ELSE 0 END), MIN(played_at), MAX(played_at)
		FROM listens WHERE user_id = ? AND song_id <> '' GROUP BY song_id`, recentFrom, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SongStat{}
	for rows.Next() {
		var id string
		var st SongStat
		if err := rows.Scan(&id, &st.Plays, &st.Recent, &st.First, &st.Last); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// SessionNeighbours finds songs the person played within the same listening
// sessions as the seed song (±window around each of its plays): "people who
// played this also played", for one person.
func (s *Store) SessionNeighbours(ctx context.Context, uid int64, songID string, window time.Duration, limit int) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT played_at FROM listens WHERE user_id = ? AND song_id = ? ORDER BY played_at DESC LIMIT 50`, uid, songID)
	if err != nil {
		return nil, err
	}
	var times []time.Time
	for rows.Next() {
		var at string
		if rows.Scan(&at) == nil {
			if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
				times = append(times, t)
			}
		}
	}
	rows.Close()
	out := map[string]int{}
	for _, t := range times {
		r, err := s.DB.QueryContext(ctx, `SELECT song_id FROM listens WHERE user_id = ? AND song_id <> '' AND song_id <> ? AND played_at BETWEEN ? AND ?`,
			uid, songID, Stamp(t.Add(-window)), Stamp(t.Add(window)))
		if err != nil {
			return nil, err
		}
		for r.Next() {
			var id string
			if r.Scan(&id) == nil {
				out[id]++
			}
		}
		r.Close()
		if len(out) > limit*4 {
			break
		}
	}
	return out, nil
}

// RelinkListens attaches imported plays (no song id) to songs of the index by
// folded artist + title, and fills album/genre where they are missing.
func (s *Store) RelinkListens(ctx context.Context, uid int64) (int, error) {
	type key struct{ a, t string }
	rows, err := s.DB.QueryContext(ctx, `SELECT id, artist, title, album, album_id, artist_id, genre, duration FROM songs WHERE user_id = ?`, uid)
	if err != nil {
		return 0, err
	}
	type hit struct {
		id, album, albumID, artistID, genre string
		dur                                 int
	}
	idx := map[key]hit{}
	for rows.Next() {
		var h hit
		var a, t string
		if err := rows.Scan(&h.id, &a, &t, &h.album, &h.albumID, &h.artistID, &h.genre, &h.dur); err != nil {
			rows.Close()
			return 0, err
		}
		k := key{MatchKey(a), MatchKey(t)}
		if _, ok := idx[k]; !ok {
			idx[k] = h
		}
	}
	rows.Close()
	lrows, err := s.DB.QueryContext(ctx, `SELECT id, artist, title FROM listens WHERE user_id = ? AND song_id = ''`, uid)
	if err != nil {
		return 0, err
	}
	type upd struct {
		id int64
		h  hit
	}
	var ups []upd
	for lrows.Next() {
		var id int64
		var a, t string
		if lrows.Scan(&id, &a, &t) == nil {
			if h, ok := idx[key{MatchKey(a), MatchKey(t)}]; ok {
				ups = append(ups, upd{id, h})
			}
		}
	}
	lrows.Close()
	if len(ups) == 0 {
		return 0, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	for _, u := range ups {
		if _, err := tx.ExecContext(ctx, `UPDATE listens SET song_id = ?, album_id = ?, artist_id = ?, genre = CASE WHEN genre = '' THEN ? ELSE genre END,
			album = CASE WHEN album = '' THEN ? ELSE album END, duration = CASE WHEN duration = 0 THEN ? ELSE duration END WHERE id = ?`,
			u.h.id, u.h.albumID, u.h.artistID, u.h.genre, u.h.album, u.h.dur, u.id); err != nil {
			// a duplicate of a live play: keep the live one
			_, _ = tx.ExecContext(ctx, `DELETE FROM listens WHERE id = ?`, u.id)
		}
	}
	return len(ups), tx.Commit()
}

// MatchKey folds a title or artist for matching across sources: case,
// accents, "feat." tails, bracketed versions and punctuation are ignored.
func MatchKey(s string) string {
	s = Fold(s)
	for _, cut := range []string{" feat. ", " feat ", " ft. ", " featuring "} {
		if i := strings.Index(s, cut); i > 0 {
			s = s[:i]
		}
	}
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '(' || r == '[':
			depth++
		case r == ')' || r == ']':
			if depth > 0 {
				depth--
			}
		case depth > 0:
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
		}
	}
	if out := b.String(); out != "" {
		return out
	}
	return s
}
