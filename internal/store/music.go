package store

import (
	"context"
	"encoding/json"
)

// ---- connections ----

// GetConnection returns a person's music-server connection.
func (s *Store) GetConnection(ctx context.Context, uid int64) (*Connection, error) {
	var c Connection
	err := s.DB.QueryRowContext(ctx, `SELECT user_id, kind, server_url, username, secret, server_type, server_version, updated_at FROM connections WHERE user_id = ?`, uid).
		Scan(&c.UserID, &c.Kind, &c.ServerURL, &c.Username, &c.Secret, &c.ServerType, &c.ServerVersion, &c.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// SaveConnection stores a person's connection.
func (s *Store) SaveConnection(ctx context.Context, c *Connection) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO connections (user_id, kind, server_url, username, secret, server_type, server_version, updated_at) VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET kind = excluded.kind, server_url = excluded.server_url, username = excluded.username, secret = excluded.secret,
		server_type = excluded.server_type, server_version = excluded.server_version, updated_at = excluded.updated_at`,
		c.UserID, c.Kind, c.ServerURL, c.Username, c.Secret, c.ServerType, c.ServerVersion, Now())
	return err
}

// DeleteConnection forgets a person's connection.
func (s *Store) DeleteConnection(ctx context.Context, uid int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM connections WHERE user_id = ?`, uid)
	return err
}

// ---- smart playlists ----

const smartCols = `id, name, description, rules, sort, limit_count, limit_minutes, playlist_id, song_count, duration, refresh, refreshed_at, error, created_at, updated_at`

func scanSmart(row interface{ Scan(...any) error }) (SmartPlaylist, error) {
	var p SmartPlaylist
	var rules string
	err := row.Scan(&p.ID, &p.Name, &p.Description, &rules, &p.Sort, &p.LimitCount, &p.LimitMinutes, &p.PlaylistID, &p.SongCount, &p.Duration, &p.Refresh, &p.RefreshedAt, &p.Error, &p.CreatedAt, &p.UpdatedAt)
	if err == nil {
		_ = json.Unmarshal([]byte(rules), &p.Rules)
		if p.Rules.Conditions == nil {
			p.Rules.Conditions = []Rule{}
		}
	}
	return p, err
}

// ListSmart lists a person's smart playlists.
func (s *Store) ListSmart(ctx context.Context, uid int64) ([]SmartPlaylist, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+smartCols+` FROM smart_playlists WHERE user_id = ? ORDER BY name COLLATE NOCASE`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SmartPlaylist{}
	for rows.Next() {
		p, err := scanSmart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetSmart returns one of the person's smart playlists.
func (s *Store) GetSmart(ctx context.Context, uid, id int64) (*SmartPlaylist, error) {
	p, err := scanSmart(s.DB.QueryRowContext(ctx, `SELECT `+smartCols+` FROM smart_playlists WHERE user_id = ? AND id = ?`, uid, id))
	if err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// SmartByPlaylist finds the smart playlist behind a server playlist id.
func (s *Store) SmartByPlaylist(ctx context.Context, uid int64, playlistID string) (*SmartPlaylist, error) {
	p, err := scanSmart(s.DB.QueryRowContext(ctx, `SELECT `+smartCols+` FROM smart_playlists WHERE user_id = ? AND playlist_id = ? AND playlist_id <> ''`, uid, playlistID))
	if err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// SaveSmart creates or updates a smart playlist (rules, sort, limits, name).
func (s *Store) SaveSmart(ctx context.Context, uid int64, p *SmartPlaylist) error {
	rules, _ := json.Marshal(p.Rules)
	if p.ID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO smart_playlists (user_id, name, description, rules, sort, limit_count, limit_minutes, refresh) VALUES (?,?,?,?,?,?,?,?)`,
			uid, p.Name, p.Description, string(rules), p.Sort, p.LimitCount, p.LimitMinutes, p.Refresh)
		if err != nil {
			return err
		}
		p.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE smart_playlists SET name = ?, description = ?, rules = ?, sort = ?, limit_count = ?, limit_minutes = ?, refresh = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		p.Name, p.Description, string(rules), p.Sort, p.LimitCount, p.LimitMinutes, p.Refresh, Now(), p.ID, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SmartRefreshed records the outcome of building a smart playlist.
func (s *Store) SmartRefreshed(ctx context.Context, uid, id int64, playlistID string, songs, seconds int, errText string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE smart_playlists SET playlist_id = CASE WHEN ? = '' THEN playlist_id ELSE ? END, song_count = ?, duration = ?, refreshed_at = ?, error = ? WHERE id = ? AND user_id = ?`,
		playlistID, playlistID, songs, seconds, Now(), errText, id, uid)
	return err
}

// DeleteSmart removes a smart playlist.
func (s *Store) DeleteSmart(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM smart_playlists WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SmartOwners lists everyone with smart playlists (for the daily refresh).
func (s *Store) SmartOwners(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT user_id FROM smart_playlists WHERE refresh = 'daily'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// ---- devices ----

// SaveDevice records what a device plays.
func (s *Store) SaveDevice(ctx context.Context, uid int64, d *Device) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO devices (user_id, id, name, song_id, queue, queue_index, position_ms, playing, updated_at) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT(user_id, id) DO UPDATE SET name = excluded.name, song_id = excluded.song_id, queue = CASE WHEN excluded.queue = '' THEN devices.queue ELSE excluded.queue END,
		queue_index = excluded.queue_index, position_ms = excluded.position_ms, playing = excluded.playing, updated_at = excluded.updated_at`,
		uid, d.ID, d.Name, d.SongID, d.Queue, d.QueueIndex, d.PositionMS, boolInt(d.Playing), Now())
	return err
}

// Devices lists a person's devices, most recent first.
func (s *Store) Devices(ctx context.Context, uid int64) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, song_id, queue, queue_index, position_ms, playing, updated_at FROM devices WHERE user_id = ? ORDER BY updated_at DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		var p int
		if err := rows.Scan(&d.ID, &d.Name, &d.SongID, &d.Queue, &d.QueueIndex, &d.PositionMS, &p, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Playing = p == 1
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDevice returns one device of the person.
func (s *Store) GetDevice(ctx context.Context, uid int64, id string) (*Device, error) {
	var d Device
	var p int
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, song_id, queue, queue_index, position_ms, playing, updated_at FROM devices WHERE user_id = ? AND id = ?`, uid, id).
		Scan(&d.ID, &d.Name, &d.SongID, &d.Queue, &d.QueueIndex, &d.PositionMS, &p, &d.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	d.Playing = p == 1
	return &d, nil
}

// DeleteDevice forgets a device.
func (s *Store) DeleteDevice(ctx context.Context, uid int64, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE user_id = ? AND id = ?`, uid, id)
	return err
}

// ---- requests ----

const requestCols = `id, artist, album, year, artist_mbid, album_mbid, lidarr_album_id, cover_url, state, detail, arrived_at, created_at, updated_at`

func scanRequest(row interface{ Scan(...any) error }) (Request, error) {
	var r Request
	err := row.Scan(&r.ID, &r.Artist, &r.Album, &r.Year, &r.ArtistMBID, &r.AlbumMBID, &r.LidarrAlbumID, &r.CoverURL, &r.State, &r.Detail, &r.ArrivedAt, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

// AddRequest records an album request (one per album per person).
func (s *Store) AddRequest(ctx context.Context, uid int64, r *Request) error {
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM requests WHERE user_id = ? AND album_mbid = ? AND album_mbid <> ''`, uid, r.AlbumMBID).Scan(&id); err == nil {
		r.ID = id
		_, err = s.DB.ExecContext(ctx, `UPDATE requests SET state = 'requested', detail = '', lidarr_album_id = ?, updated_at = ? WHERE id = ?`, r.LidarrAlbumID, Now(), id)
		return err
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO requests (user_id, artist, album, year, artist_mbid, album_mbid, lidarr_album_id, cover_url, state) VALUES (?,?,?,?,?,?,?,?,?)`,
		uid, r.Artist, r.Album, r.Year, r.ArtistMBID, r.AlbumMBID, r.LidarrAlbumID, r.CoverURL, "requested")
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// Requests lists a person's requests (open ones first).
func (s *Store) Requests(ctx context.Context, uid int64, openOnly bool) ([]Request, error) {
	q := `SELECT ` + requestCols + ` FROM requests WHERE user_id = ?`
	if openOnly {
		q += ` AND state IN ('requested', 'downloading')`
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY CASE WHEN state IN ('requested','downloading') THEN 0 ELSE 1 END, updated_at DESC LIMIT 200`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpenRequestOwners lists people with requests still waiting.
func (s *Store) OpenRequestOwners(ctx context.Context) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT user_id FROM requests WHERE state IN ('requested', 'downloading')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// SetRequestState updates a request.
func (s *Store) SetRequestState(ctx context.Context, uid, id int64, state, detail string) error {
	arrived := ""
	if state == "arrived" {
		arrived = Now()
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE requests SET state = ?, detail = ?, arrived_at = CASE WHEN ? <> '' THEN ? ELSE arrived_at END, updated_at = ? WHERE id = ? AND user_id = ?`,
		state, detail, arrived, arrived, Now(), id, uid)
	return err
}

// DeleteRequest removes a request from the list (Lidarr keeps the album).
func (s *Store) DeleteRequest(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM requests WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- imports ----

// StartImport records a running import.
func (s *Store) StartImport(ctx context.Context, uid int64, source, account string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO imports (user_id, source, account) VALUES (?, ?, ?)`, uid, source, account)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ImportProgress updates counters (state done/error finishes it).
func (s *Store) ImportProgress(ctx context.Context, id int64, state string, seen, imported, matched int, errText string) {
	fin := ""
	if state != "running" {
		fin = Now()
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE imports SET state = ?, seen = ?, imported = ?, matched = ?, error = ?, finished_at = ? WHERE id = ?`, state, seen, imported, matched, errText, fin, id)
}

// Imports lists a person's imports.
func (s *Store) Imports(ctx context.Context, uid int64) ([]Import, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, source, account, state, seen, imported, matched, error, started_at, finished_at FROM imports WHERE user_id = ? ORDER BY id DESC LIMIT 20`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Import{}
	for rows.Next() {
		var i Import
		if err := rows.Scan(&i.ID, &i.Source, &i.Account, &i.State, &i.Seen, &i.Imported, &i.Matched, &i.Error, &i.StartedAt, &i.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// FailRunningImports marks imports cut off by a restart.
func (s *Store) FailRunningImports(ctx context.Context) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE imports SET state = 'error', error = 'stopped by a restart; start it again (plays already imported are kept)', finished_at = ? WHERE state = 'running'`, Now())
	_, _ = s.DB.ExecContext(ctx, `UPDATE library_sync SET state = 'idle' WHERE state = 'syncing'`)
}

// ---- Subsonic app logins ----

// SubsonicLogin is a person's login for Subsonic apps.
type SubsonicLogin struct {
	UserID     int64  `json:"-"`
	Username   string `json:"username"`
	Secret     string `json:"-"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at"`
	LastClient string `json:"last_client"`
}

const subsonicCols = `user_id, username, secret, created_at, last_used_at, last_client`

func scanSubsonic(row interface{ Scan(...any) error }) (*SubsonicLogin, error) {
	var l SubsonicLogin
	if err := row.Scan(&l.UserID, &l.Username, &l.Secret, &l.CreatedAt, &l.LastUsedAt, &l.LastClient); err != nil {
		return nil, notFound(err)
	}
	return &l, nil
}

// GetSubsonicLogin returns a person's login.
func (s *Store) GetSubsonicLogin(ctx context.Context, uid int64) (*SubsonicLogin, error) {
	return scanSubsonic(s.DB.QueryRowContext(ctx, `SELECT `+subsonicCols+` FROM subsonic_logins WHERE user_id = ?`, uid))
}

// SubsonicLoginByName finds a login by its user name (case-insensitive).
func (s *Store) SubsonicLoginByName(ctx context.Context, name string) (*SubsonicLogin, error) {
	return scanSubsonic(s.DB.QueryRowContext(ctx, `SELECT `+subsonicCols+` FROM subsonic_logins WHERE username = ?`, name))
}

// SaveSubsonicLogin creates or updates a person's login (ErrConflict when the name is taken).
func (s *Store) SaveSubsonicLogin(ctx context.Context, l *SubsonicLogin) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO subsonic_logins (user_id, username, secret) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET username = excluded.username, secret = excluded.secret`, l.UserID, l.Username, l.Secret)
	return conflict(err)
}

// TouchSubsonicLogin records the last use.
func (s *Store) TouchSubsonicLogin(ctx context.Context, uid int64, client string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE subsonic_logins SET last_used_at = ?, last_client = ? WHERE user_id = ?`, Now(), client, uid)
}

// DeleteSubsonicLogin removes a person's login.
func (s *Store) DeleteSubsonicLogin(ctx context.Context, uid int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM subsonic_logins WHERE user_id = ?`, uid)
	return err
}
