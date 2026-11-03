-- Albums and tracks fetched from a link (YouTube, Bandcamp, SoundCloud… through yt-dlp) straight into the music folder.
CREATE TABLE link_downloads (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  url         TEXT NOT NULL,
  artist      TEXT NOT NULL DEFAULT '',
  album       TEXT NOT NULL DEFAULT '',
  year        INTEGER NOT NULL DEFAULT 0,
  genre       TEXT NOT NULL DEFAULT '',
  folder      TEXT NOT NULL DEFAULT '', -- the album folder, relative to MUSIC_DIR
  cover_url   TEXT NOT NULL DEFAULT '',
  channel     TEXT NOT NULL DEFAULT '', -- the uploader: a new artist folder gets its picture when the artist is the channel
  channel_url TEXT NOT NULL DEFAULT '',
  tracks      TEXT NOT NULL DEFAULT '[]', -- [{url, title, number, file}]
  state       TEXT NOT NULL DEFAULT 'queued', -- queued | downloading | scanning | done | failed
  detail      TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  album_id    TEXT NOT NULL DEFAULT '', -- the album in the library once the server scanned it
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX link_downloads_user ON link_downloads(user_id, created_at);
CREATE INDEX link_downloads_state ON link_downloads(state);
