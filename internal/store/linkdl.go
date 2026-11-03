package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// LinkTrack is one track of a link download.
type LinkTrack struct {
	URL    string `json:"url"`
	Title  string `json:"title"`
	Number int    `json:"number"`
	File   string `json:"file"` // relative to MUSIC_DIR once fetched
}

// LinkDownload is an album or track fetched from a link into the music folder.
type LinkDownload struct {
	ID         int64       `json:"id"`
	UserID     int64       `json:"-"`
	URL        string      `json:"url"`
	Artist     string      `json:"artist"`
	Album      string      `json:"album"`
	Year       int         `json:"year"`
	Genre      string      `json:"genre"`
	Folder     string      `json:"folder"`
	CoverURL   string      `json:"cover_url"`
	Channel    string      `json:"channel"`
	ChannelURL string      `json:"channel_url"`
	Tracks     []LinkTrack `json:"tracks"`
	State      string      `json:"state"`
	Detail     string      `json:"detail"`
	Error      string      `json:"error"`
	AlbumID    string      `json:"album_id"`
	Playlist   string      `json:"playlist_id"` // add the tracks to this playlist when they are in the library
	PlaylistNm string      `json:"playlist_name"`
	CreatedAt  string      `json:"created_at"`
	UpdatedAt  string      `json:"updated_at"`
}

const linkCols = `id, user_id, url, artist, album, year, genre, folder, cover_url, channel, channel_url, tracks, state, detail, error, album_id, playlist_id, playlist_name, created_at, updated_at`

func scanLink(row interface{ Scan(...any) error }) (*LinkDownload, error) {
	var d LinkDownload
	var tracks string
	if err := row.Scan(&d.ID, &d.UserID, &d.URL, &d.Artist, &d.Album, &d.Year, &d.Genre, &d.Folder, &d.CoverURL, &d.Channel, &d.ChannelURL, &tracks, &d.State, &d.Detail, &d.Error, &d.AlbumID, &d.Playlist, &d.PlaylistNm, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(tracks), &d.Tracks)
	if d.Tracks == nil {
		d.Tracks = []LinkTrack{}
	}
	return &d, nil
}

// AddLinkDownload queues a download.
func (s *Store) AddLinkDownload(ctx context.Context, d *LinkDownload) error {
	tracks, _ := json.Marshal(d.Tracks)
	res, err := s.DB.ExecContext(ctx, `INSERT INTO link_downloads (user_id, url, artist, album, year, genre, folder, cover_url, channel, channel_url, tracks, playlist_id, playlist_name, state) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, 'queued')`,
		d.UserID, d.URL, d.Artist, d.Album, d.Year, d.Genre, d.Folder, d.CoverURL, d.Channel, d.ChannelURL, string(tracks), d.Playlist, d.PlaylistNm)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	d.State = "queued"
	return nil
}

// SaveLinkDownload writes a download's progress back.
func (s *Store) SaveLinkDownload(ctx context.Context, d *LinkDownload) error {
	tracks, _ := json.Marshal(d.Tracks)
	d.UpdatedAt = Now()
	_, err := s.DB.ExecContext(ctx, `UPDATE link_downloads SET folder = ?, cover_url = ?, tracks = ?, state = ?, detail = ?, error = ?, album_id = ?, updated_at = ? WHERE id = ?`,
		d.Folder, d.CoverURL, string(tracks), d.State, d.Detail, d.Error, d.AlbumID, d.UpdatedAt, d.ID)
	return err
}

// LinkDownloads lists a person's downloads, newest first.
func (s *Store) LinkDownloads(ctx context.Context, uid int64) ([]LinkDownload, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+linkCols+` FROM link_downloads WHERE user_id = ? ORDER BY id DESC LIMIT 100`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LinkDownload{}
	for rows.Next() {
		d, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// GetLinkDownload returns one download (uid 0 = anyone's, for the worker).
func (s *Store) GetLinkDownload(ctx context.Context, uid, id int64) (*LinkDownload, error) {
	if uid == 0 {
		return scanLink(s.DB.QueryRowContext(ctx, `SELECT `+linkCols+` FROM link_downloads WHERE id = ?`, id))
	}
	return scanLink(s.DB.QueryRowContext(ctx, `SELECT `+linkCols+` FROM link_downloads WHERE id = ? AND user_id = ?`, id, uid))
}

// NextLinkDownload is the oldest download still to do; ones cut off by a
// restart in the middle are picked up again first.
func (s *Store) NextLinkDownload(ctx context.Context) (*LinkDownload, error) {
	return scanLink(s.DB.QueryRowContext(ctx, `SELECT `+linkCols+` FROM link_downloads WHERE state IN ('queued', 'downloading', 'scanning')
		ORDER BY CASE state WHEN 'queued' THEN 1 ELSE 0 END, id LIMIT 1`))
}

// DeleteLinkDownload forgets a download (the files stay).
func (s *Store) DeleteLinkDownload(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM link_downloads WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearLinkDownloads forgets a person's finished and failed downloads.
func (s *Store) ClearLinkDownloads(ctx context.Context, uid int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM link_downloads WHERE user_id = ? AND state IN ('done', 'failed')`, uid)
	return err
}
