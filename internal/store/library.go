package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// ---- the per-person library index ----

const songCols = `id, title, album, album_id, artist, artist_id, album_artist, album_artist_id, genre, genres, year, track, disc,
	duration, bitrate, suffix, size, bit_depth, sample_rate, path, added_at, mbid, bpm, rg_track, rg_album, rg_peak, starred_at, rating, server_plays`

func scanSong(row interface{ Scan(...any) error }, extra ...any) (Song, error) {
	var s Song
	dst := []any{&s.ID, &s.Title, &s.Album, &s.AlbumID, &s.Artist, &s.ArtistID, &s.AlbumArtist, &s.AlbumArtistID, &s.Genre, &s.Genres,
		&s.Year, &s.Track, &s.Disc, &s.Duration, &s.Bitrate, &s.Suffix, &s.Size, &s.BitDepth, &s.SampleRate, &s.Path, &s.AddedAt, &s.MBID, &s.BPM,
		&s.RGTrack, &s.RGAlbum, &s.RGPeak, &s.StarredAt, &s.Rating, &s.ServerPlays}
	err := row.Scan(append(dst, extra...)...)
	return s, err
}

const albumCols = `id, name, sort_name, artist, artist_id, year, genre, genres, song_count, duration, added_at, compilation, mbid, starred_at, rating, server_plays`

func scanAlbum(row interface{ Scan(...any) error }, extra ...any) (Album, error) {
	var a Album
	var comp int
	dst := []any{&a.ID, &a.Name, &a.SortName, &a.Artist, &a.ArtistID, &a.Year, &a.Genre, &a.Genres, &a.SongCount, &a.Duration, &a.AddedAt, &comp, &a.MBID, &a.StarredAt, &a.Rating, &a.ServerPlays}
	err := row.Scan(append(dst, extra...)...)
	a.Compilation = comp == 1
	return a, err
}

const artistCols = `id, name, sort_name, album_count, song_count, mbid, starred_at, rating`

func scanArtist(row interface{ Scan(...any) error }, extra ...any) (Artist, error) {
	var a Artist
	dst := []any{&a.ID, &a.Name, &a.SortName, &a.AlbumCount, &a.SongCount, &a.MBID, &a.StarredAt, &a.Rating}
	err := row.Scan(append(dst, extra...)...)
	return a, err
}

// Fold lowercases and strips accents so "Björk" matches "bjork".
func Fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return strings.ToLower(strings.TrimSpace(out))
}

// LibraryWriter replaces one person's whole index in a single transaction.
type LibraryWriter struct {
	tx  *sql.Tx
	uid int64
	ss  *sql.Stmt
	as  *sql.Stmt
	rs  *sql.Stmt
	fs  *sql.Stmt
	n   [3]int
}

// BeginLibrary starts replacing a person's index. Nothing is visible until Commit.
func (s *Store) BeginLibrary(ctx context.Context, uid int64) (*LibraryWriter, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	w := &LibraryWriter{tx: tx, uid: uid}
	fail := func(err error) (*LibraryWriter, error) { _ = tx.Rollback(); return nil, err }
	for _, q := range []string{`DELETE FROM songs WHERE user_id = ?`, `DELETE FROM albums WHERE user_id = ?`, `DELETE FROM artists WHERE user_id = ?`, `DELETE FROM search_fts WHERE user_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, uid); err != nil {
			return fail(err)
		}
	}
	if w.ss, err = tx.PrepareContext(ctx, `INSERT OR REPLACE INTO songs (user_id, `+songCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`); err != nil {
		return fail(err)
	}
	if w.as, err = tx.PrepareContext(ctx, `INSERT OR REPLACE INTO albums (user_id, `+albumCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`); err != nil {
		return fail(err)
	}
	if w.rs, err = tx.PrepareContext(ctx, `INSERT OR REPLACE INTO artists (user_id, `+artistCols+`) VALUES (?,?,?,?,?,?,?,?,?)`); err != nil {
		return fail(err)
	}
	if w.fs, err = tx.PrepareContext(ctx, `INSERT INTO search_fts (user_id, kind, item_id, name, extra) VALUES (?,?,?,?,?)`); err != nil {
		return fail(err)
	}
	return w, nil
}

// Song adds a song.
func (w *LibraryWriter) Song(ctx context.Context, s Song) error {
	_, err := w.ss.ExecContext(ctx, w.uid, s.ID, s.Title, s.Album, s.AlbumID, s.Artist, s.ArtistID, s.AlbumArtist, s.AlbumArtistID, s.Genre, s.Genres,
		s.Year, s.Track, s.Disc, s.Duration, s.Bitrate, s.Suffix, s.Size, s.BitDepth, s.SampleRate, s.Path, s.AddedAt, s.MBID, s.BPM,
		s.RGTrack, s.RGAlbum, s.RGPeak, s.StarredAt, s.Rating, s.ServerPlays)
	if err != nil {
		return err
	}
	w.n[0]++
	_, err = w.fs.ExecContext(ctx, w.uid, "song", s.ID, Fold(s.Title), Fold(s.Artist+" "+s.Album))
	return err
}

// Album adds an album.
func (w *LibraryWriter) Album(ctx context.Context, a Album) error {
	_, err := w.as.ExecContext(ctx, w.uid, a.ID, a.Name, a.SortName, a.Artist, a.ArtistID, a.Year, a.Genre, a.Genres, a.SongCount, a.Duration, a.AddedAt,
		boolInt(a.Compilation), a.MBID, a.StarredAt, a.Rating, a.ServerPlays)
	if err != nil {
		return err
	}
	w.n[1]++
	_, err = w.fs.ExecContext(ctx, w.uid, "album", a.ID, Fold(a.Name), Fold(a.Artist))
	return err
}

// Artist adds an artist.
func (w *LibraryWriter) Artist(ctx context.Context, a Artist) error {
	_, err := w.rs.ExecContext(ctx, w.uid, a.ID, a.Name, a.SortName, a.AlbumCount, a.SongCount, a.MBID, a.StarredAt, a.Rating)
	if err != nil {
		return err
	}
	w.n[2]++
	_, err = w.fs.ExecContext(ctx, w.uid, "artist", a.ID, Fold(a.Name), "")
	return err
}

// Rollback abandons the new index; the old one stays.
func (w *LibraryWriter) Rollback() { _ = w.tx.Rollback() }

// Commit swaps the new index in and records the sync.
func (w *LibraryWriter) Commit(ctx context.Context, serverKey, lastScan string) error {
	_, err := w.tx.ExecContext(ctx, `INSERT INTO library_sync (user_id, server_key, last_scan, synced_at, checked_at, state, progress, error, songs, albums, artists)
		VALUES (?, ?, ?, ?, ?, 'idle', 100, '', ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET server_key = excluded.server_key, last_scan = excluded.last_scan, synced_at = excluded.synced_at,
		checked_at = excluded.checked_at, state = 'idle', progress = 100, error = '', songs = excluded.songs, albums = excluded.albums, artists = excluded.artists`,
		w.uid, serverKey, lastScan, Now(), Now(), w.n[0], w.n[1], w.n[2])
	if err != nil {
		_ = w.tx.Rollback()
		return err
	}
	return w.tx.Commit()
}

// Sync returns a person's index state.
func (s *Store) Sync(ctx context.Context, uid int64) SyncState {
	var st SyncState
	if err := s.DB.QueryRowContext(ctx, `SELECT server_key, last_scan, synced_at, checked_at, state, progress, error, songs, albums, artists FROM library_sync WHERE user_id = ?`, uid).
		Scan(&st.ServerKey, &st.LastScan, &st.SyncedAt, &st.CheckedAt, &st.State, &st.Progress, &st.Error, &st.Songs, &st.Albums, &st.Artists); err != nil {
		st.State = "never"
	}
	return st
}

// SetSyncState records progress or an error without touching the index.
func (s *Store) SetSyncState(ctx context.Context, uid int64, state string, progress int, errText string) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO library_sync (user_id, state, progress, error, checked_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET state = excluded.state, progress = excluded.progress, error = excluded.error, checked_at = excluded.checked_at`,
		uid, state, progress, errText, Now())
}

// ForgetLibrary drops a person's index (they disconnected or changed accounts).
func (s *Store) ForgetLibrary(ctx context.Context, uid int64) error {
	for _, q := range []string{`DELETE FROM songs WHERE user_id = ?`, `DELETE FROM albums WHERE user_id = ?`, `DELETE FROM artists WHERE user_id = ?`,
		`DELETE FROM search_fts WHERE user_id = ?`, `DELETE FROM library_sync WHERE user_id = ?`} {
		if _, err := s.DB.ExecContext(ctx, q, uid); err != nil {
			return err
		}
	}
	return nil
}

// ---- reading ----

// GetSong returns one song of the person's index.
func (s *Store) GetSong(ctx context.Context, uid int64, id string) (*Song, error) {
	so, err := scanSong(s.DB.QueryRowContext(ctx, `SELECT `+songCols+` FROM songs WHERE user_id = ? AND id = ?`, uid, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &so, nil
}

// SongsByID returns songs in the order of ids (missing ids are skipped).
func (s *Store) SongsByID(ctx context.Context, uid int64, ids []string) ([]Song, error) {
	if len(ids) == 0 {
		return []Song{}, nil
	}
	byID := map[string]Song{}
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		chunk := ids[start:end]
		args := []any{uid}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT `+songCols+` FROM songs WHERE user_id = ? AND id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			so, err := scanSong(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			byID[so.ID] = so
		}
		rows.Close()
	}
	out := make([]Song, 0, len(ids))
	for _, id := range ids {
		if so, ok := byID[id]; ok {
			out = append(out, so)
		}
	}
	return out, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// QuerySongs runs a SELECT over songs with a WHERE fragment.
func (s *Store) QuerySongs(ctx context.Context, uid int64, where, order string, limit int, args ...any) ([]Song, error) {
	q := `SELECT ` + songCols + ` FROM songs WHERE user_id = ?`
	if where != "" {
		q += ` AND (` + where + `)`
	}
	if order != "" {
		q += ` ORDER BY ` + order
	}
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, append([]any{uid}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Song{}
	for rows.Next() {
		so, err := scanSong(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, so)
	}
	return out, rows.Err()
}

// AlbumSongs returns an album's songs in disc/track order.
func (s *Store) AlbumSongs(ctx context.Context, uid int64, albumID string) ([]Song, error) {
	return s.QuerySongs(ctx, uid, `album_id = ?`, `disc, track, title`, 0, albumID)
}

// GetAlbum returns one album.
func (s *Store) GetAlbum(ctx context.Context, uid int64, id string) (*Album, error) {
	a, err := scanAlbum(s.DB.QueryRowContext(ctx, `SELECT `+albumCols+` FROM albums WHERE user_id = ? AND id = ?`, uid, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

// GetArtist returns one artist.
func (s *Store) GetArtist(ctx context.Context, uid int64, id string) (*Artist, error) {
	a, err := scanArtist(s.DB.QueryRowContext(ctx, `SELECT `+artistCols+` FROM artists WHERE user_id = ? AND id = ?`, uid, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

// AlbumFilter narrows the album grid.
type AlbumFilter struct {
	Sort     string // added | name | artist | year | plays | played | random | starred
	Genre    string
	Year     int
	Decade   int
	ArtistID string
	Starred  bool
	Unplayed bool
	Limit    int
	Offset   int
	Seed     int64 // random order, stable per page load
}

// ListAlbums pages through albums with the person's play counts.
func (s *Store) ListAlbums(ctx context.Context, uid int64, f AlbumFilter) ([]Album, int, error) {
	where := []string{"a.user_id = ?"}
	args := []any{uid}
	if f.Genre != "" {
		where = append(where, "a.genres LIKE ?")
		args = append(args, "%|"+f.Genre+"|%")
	}
	if f.Year > 0 {
		where = append(where, "a.year = ?")
		args = append(args, f.Year)
	}
	if f.Decade > 0 {
		where = append(where, "a.year BETWEEN ? AND ?")
		args = append(args, f.Decade, f.Decade+9)
	}
	if f.ArtistID != "" {
		where = append(where, "(a.artist_id = ? OR a.id IN (SELECT DISTINCT album_id FROM songs WHERE user_id = ? AND (artist_id = ? OR album_artist_id = ?)))")
		args = append(args, f.ArtistID, uid, f.ArtistID, f.ArtistID)
	}
	if f.Starred {
		where = append(where, "a.starred_at <> ''")
	}
	if f.Unplayed {
		where = append(where, "COALESCE(p.plays, 0) = 0")
	}
	order := "a.added_at DESC, a.sort_name"
	switch f.Sort {
	case "name":
		order = "a.sort_name, a.name"
	case "artist":
		order = "a.artist COLLATE NOCASE, a.year, a.sort_name"
	case "year":
		order = "a.year DESC, a.artist COLLATE NOCASE"
	case "year_asc":
		order = "CASE WHEN a.year = 0 THEN 9999 ELSE a.year END, a.artist COLLATE NOCASE"
	case "plays":
		order = "COALESCE(p.plays, 0) DESC, p.last DESC"
	case "played":
		order = "p.last IS NULL, p.last DESC"
	case "starred":
		order = "a.starred_at DESC"
	case "random":
		// stable for one seed, so paging a shuffled grid neither repeats nor skips
		order = fmt.Sprintf("((unicode(substr(a.id, 2, 1)) * 131 + unicode(substr(a.id, 5, 1)) * 31 + unicode(substr(a.id, 9, 1)) * 7 + %d) %% 997), a.id", f.Seed%997)
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 60
	}
	base := ` FROM albums a LEFT JOIN (SELECT album_id, COUNT(1) plays, MAX(played_at) last FROM listens WHERE user_id = ? AND album_id <> '' GROUP BY album_id) p ON p.album_id = a.id WHERE ` + strings.Join(where, " AND ")
	qargs := append([]any{uid}, args...)
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1)`+base, qargs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.`+strings.ReplaceAll(albumCols, ", ", ", a.")+`, COALESCE(p.plays, 0), COALESCE(p.last, '')`+base+` ORDER BY `+order+fmt.Sprintf(` LIMIT %d OFFSET %d`, f.Limit, f.Offset), qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Album{}
	for rows.Next() {
		var a Album
		var plays int
		var last string
		a, err = scanAlbum(rows, &plays, &last)
		if err != nil {
			return nil, 0, err
		}
		a.Plays, a.LastPlayed = plays, last
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// fillAlbumPlays adds the person's play counts to albums read without them.
func (s *Store) fillAlbumPlays(ctx context.Context, uid int64, as []Album) {
	if len(as) == 0 {
		return
	}
	idx := map[string]int{}
	args := []any{uid}
	for i, a := range as {
		idx[a.ID] = i
		args = append(args, a.ID)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT album_id, COUNT(1), MAX(played_at) FROM listens WHERE user_id = ? AND album_id IN (`+placeholders(len(as))+`) GROUP BY album_id`, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, last string
		var n int
		if rows.Scan(&id, &n, &last) == nil {
			if i, ok := idx[id]; ok {
				as[i].Plays, as[i].LastPlayed = n, last
			}
		}
	}
}

// FillAlbumPlays adds the person's play counts to albums.
func (s *Store) FillAlbumPlays(ctx context.Context, uid int64, as []Album) {
	s.fillAlbumPlays(ctx, uid, as)
}

// FillSongPlays adds the person's play counts to songs.
func (s *Store) FillSongPlays(ctx context.Context, uid int64, ss []Song) {
	if len(ss) == 0 {
		return
	}
	idx := map[string][]int{}
	ids := []string{}
	for i, so := range ss {
		if _, ok := idx[so.ID]; !ok {
			ids = append(ids, so.ID)
		}
		idx[so.ID] = append(idx[so.ID], i)
	}
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := []any{uid}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT song_id, COUNT(1), MAX(played_at) FROM listens WHERE user_id = ? AND song_id IN (`+placeholders(len(chunk))+`) GROUP BY song_id`, args...)
		if err != nil {
			return
		}
		for rows.Next() {
			var id, last string
			var n int
			if rows.Scan(&id, &n, &last) == nil {
				for _, i := range idx[id] {
					ss[i].Plays, ss[i].LastPlayed = n, last
				}
			}
		}
		rows.Close()
	}
}

// ArtistFilter narrows the artist list.
type ArtistFilter struct {
	Sort        string // name | plays | albums | added
	Starred     bool
	AlbumArtist bool // only artists with albums of their own
	Genre       string
	Limit       int
	Offset      int
}

// ListArtists pages through artists.
func (s *Store) ListArtists(ctx context.Context, uid int64, f ArtistFilter) ([]Artist, int, error) {
	where := []string{"r.user_id = ?"}
	args := []any{uid}
	if f.Starred {
		where = append(where, "r.starred_at <> ''")
	}
	if f.AlbumArtist {
		where = append(where, "r.album_count > 0")
	}
	if f.Genre != "" {
		where = append(where, "r.id IN (SELECT album_artist_id FROM songs WHERE user_id = ? AND genres LIKE ?)")
		args = append(args, uid, "%|"+f.Genre+"|%")
	}
	order := "r.sort_name, r.name"
	switch f.Sort {
	case "plays":
		order = "COALESCE(p.plays, 0) DESC, r.sort_name"
	case "albums":
		order = "r.album_count DESC, r.sort_name"
	case "songs":
		order = "r.song_count DESC, r.sort_name"
	case "played":
		order = "p.last IS NULL, p.last DESC"
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	base := ` FROM artists r LEFT JOIN (SELECT artist_id, COUNT(1) plays, MAX(played_at) last FROM listens WHERE user_id = ? AND artist_id <> '' GROUP BY artist_id) p ON p.artist_id = r.id WHERE ` + strings.Join(where, " AND ")
	qargs := append([]any{uid}, args...)
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1)`+base, qargs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.`+strings.ReplaceAll(artistCols, ", ", ", r.")+`, COALESCE(p.plays, 0), COALESCE(p.last, '')`+base+` ORDER BY `+order+fmt.Sprintf(` LIMIT %d OFFSET %d`, f.Limit, f.Offset), qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Artist{}
	for rows.Next() {
		var a Artist
		var plays int
		var last string
		a, err = scanArtist(rows, &plays, &last)
		if err != nil {
			return nil, 0, err
		}
		a.Plays, a.LastPlayed = plays, last
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// GenreCount is a genre with its size in the person's library.
type GenreCount struct {
	Name   string `json:"name"`
	Songs  int    `json:"songs"`
	Albums int    `json:"albums"`
	Plays  int    `json:"plays"`
}

// Genres lists genres with song/album counts and the person's plays.
func (s *Store) Genres(ctx context.Context, uid int64) ([]GenreCount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT genres, album_id FROM songs WHERE user_id = ? AND genres <> ''`, uid)
	if err != nil {
		return nil, err
	}
	type acc struct {
		songs  int
		albums map[string]bool
	}
	m := map[string]*acc{}
	for rows.Next() {
		var gs, album string
		if err := rows.Scan(&gs, &album); err != nil {
			rows.Close()
			return nil, err
		}
		for _, g := range SplitGenres(gs) {
			a := m[g]
			if a == nil {
				a = &acc{albums: map[string]bool{}}
				m[g] = a
			}
			a.songs++
			a.albums[album] = true
		}
	}
	rows.Close()
	plays := map[string]int{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT genre, COUNT(1) FROM listens WHERE user_id = ? AND genre <> '' GROUP BY genre`, uid); err == nil {
		for rows.Next() {
			var g string
			var n int
			if rows.Scan(&g, &n) == nil {
				plays[g] = n
			}
		}
		rows.Close()
	}
	out := make([]GenreCount, 0, len(m))
	for g, a := range m {
		out = append(out, GenreCount{Name: g, Songs: a.songs, Albums: len(a.albums), Plays: plays[g]})
	}
	return out, nil
}

// SplitGenres turns "|Rock|Metal|" into [Rock Metal].
func SplitGenres(s string) []string {
	var out []string
	for _, g := range strings.Split(s, "|") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// JoinGenres is the stored form: "|Rock|Metal|".
func JoinGenres(gs []string) string {
	if len(gs) == 0 {
		return ""
	}
	return "|" + strings.Join(gs, "|") + "|"
}

// SearchHit is one search result.
type SearchHit struct {
	Kind string
	ID   string
}

// Search finds songs, albums and artists by words (prefix match, accents ignored).
func (s *Store) Search(ctx context.Context, uid int64, q string, kind string, limit int) ([]SearchHit, error) {
	words := strings.FieldsFunc(Fold(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) == 0 {
		return nil, nil
	}
	var terms []string
	for _, w := range words {
		terms = append(terms, `"`+w+`"*`)
	}
	match := strings.Join(terms, " ")
	where := `search_fts MATCH ? AND user_id = ?`
	args := []any{match, uid}
	if kind != "" {
		where += ` AND kind = ?`
		args = append(args, kind)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT kind, item_id FROM search_fts WHERE `+where+fmt.Sprintf(` ORDER BY bm25(search_fts, 0, 0, 0, 10, 1) LIMIT %d`, limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.Kind, &h.ID); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// AlbumsByID returns albums in the order of ids.
func (s *Store) AlbumsByID(ctx context.Context, uid int64, ids []string) ([]Album, error) {
	if len(ids) == 0 {
		return []Album{}, nil
	}
	args := []any{uid}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+albumCols+` FROM albums WHERE user_id = ? AND id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	byID := map[string]Album{}
	for rows.Next() {
		a, err := scanAlbum(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byID[a.ID] = a
	}
	rows.Close()
	out := []Album{}
	for _, id := range ids {
		if a, ok := byID[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// ArtistsByID returns artists in the order of ids.
func (s *Store) ArtistsByID(ctx context.Context, uid int64, ids []string) ([]Artist, error) {
	if len(ids) == 0 {
		return []Artist{}, nil
	}
	args := []any{uid}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+artistCols+` FROM artists WHERE user_id = ? AND id IN (`+placeholders(len(ids))+`)`, args...)
	if err != nil {
		return nil, err
	}
	byID := map[string]Artist{}
	for rows.Next() {
		a, err := scanArtist(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byID[a.ID] = a
	}
	rows.Close()
	out := []Artist{}
	for _, id := range ids {
		if a, ok := byID[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// ArtistAlbums returns albums an artist made or appears on, with plays.
func (s *Store) ArtistAlbums(ctx context.Context, uid int64, artistID string) (own, appears []Album, err error) {
	all, _, err := s.ListAlbums(ctx, uid, AlbumFilter{ArtistID: artistID, Sort: "year", Limit: 500})
	if err != nil {
		return nil, nil, err
	}
	own, appears = []Album{}, []Album{}
	for _, a := range all {
		if a.ArtistID == artistID {
			own = append(own, a)
		} else {
			appears = append(appears, a)
		}
	}
	return own, appears, nil
}

// SetStarred updates the index after a star/unstar.
func (s *Store) SetStarred(ctx context.Context, uid int64, kind, id string, on bool) error {
	table := map[string]string{"song": "songs", "album": "albums", "artist": "artists"}[kind]
	if table == "" {
		return ErrNotFound
	}
	v := ""
	if on {
		v = Now()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE `+table+` SET starred_at = ? WHERE user_id = ? AND id = ?`, v, uid, id)
	return err
}

// SetRating updates the index after a rating.
func (s *Store) SetRating(ctx context.Context, uid int64, kind, id string, rating int) error {
	table := map[string]string{"song": "songs", "album": "albums", "artist": "artists"}[kind]
	if table == "" {
		return ErrNotFound
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE `+table+` SET rating = ? WHERE user_id = ? AND id = ?`, rating, uid, id)
	return err
}

// ApplyStarred makes the index's stars match the server's (stars change without a scan).
func (s *Store) ApplyStarred(ctx context.Context, uid int64, kind string, starred map[string]string) error {
	table := map[string]string{"song": "songs", "album": "albums", "artist": "artists"}[kind]
	if table == "" {
		return ErrNotFound
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET starred_at = '' WHERE user_id = ? AND starred_at <> ''`, uid); err != nil {
		_ = tx.Rollback()
		return err
	}
	for id, at := range starred {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET starred_at = ? WHERE user_id = ? AND id = ?`, at, uid, id); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// LibraryTotals counts what's in the person's index.
func (s *Store) LibraryTotals(ctx context.Context, uid int64) map[string]int64 {
	out := map[string]int64{}
	var n, d int64
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(SUM(duration), 0) FROM songs WHERE user_id = ?`, uid).Scan(&n, &d)
	out["songs"], out["seconds"] = n, d
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM albums WHERE user_id = ?`, uid).Scan(&n)
	out["albums"] = n
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM artists WHERE user_id = ? AND album_count > 0`, uid).Scan(&n)
	out["artists"] = n
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM songs WHERE user_id = ? AND starred_at <> ''`, uid).Scan(&n)
	out["starred"] = n
	return out
}
