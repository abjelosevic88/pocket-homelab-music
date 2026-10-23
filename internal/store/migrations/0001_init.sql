CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);


-- How a person reaches the music server: their own account (kind direct,
-- password sealed with DATA_DIR/secret.key) or the account Pocket Core holds
-- for them (kind core: nothing stored here). No row = Core if the box has one.
CREATE TABLE connections (
  user_id        INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  kind           TEXT NOT NULL DEFAULT 'direct',   -- direct | core | off
  server_url     TEXT NOT NULL DEFAULT '',         -- '' = NAVIDROME_URL
  username       TEXT NOT NULL DEFAULT '',
  secret         TEXT NOT NULL DEFAULT '',
  server_type    TEXT NOT NULL DEFAULT '',
  server_version TEXT NOT NULL DEFAULT '',
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Each person's copy of the library index (what THEIR account can see), so
-- search, smart playlists, mixes and stats run locally and fast. Rebuilt from
-- the music server whenever it finished a new scan.
CREATE TABLE songs (
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id              TEXT NOT NULL,
  title           TEXT NOT NULL DEFAULT '',
  album           TEXT NOT NULL DEFAULT '',
  album_id        TEXT NOT NULL DEFAULT '',
  artist          TEXT NOT NULL DEFAULT '',
  artist_id       TEXT NOT NULL DEFAULT '',
  album_artist    TEXT NOT NULL DEFAULT '',
  album_artist_id TEXT NOT NULL DEFAULT '',
  genre           TEXT NOT NULL DEFAULT '',          -- first genre
  genres          TEXT NOT NULL DEFAULT '',          -- every genre, '|'-joined with leading/trailing '|'
  year            INTEGER NOT NULL DEFAULT 0,
  track           INTEGER NOT NULL DEFAULT 0,
  disc            INTEGER NOT NULL DEFAULT 0,
  duration        INTEGER NOT NULL DEFAULT 0,        -- seconds
  bitrate         INTEGER NOT NULL DEFAULT 0,
  suffix          TEXT NOT NULL DEFAULT '',
  size            INTEGER NOT NULL DEFAULT 0,
  bit_depth       INTEGER NOT NULL DEFAULT 0,
  sample_rate     INTEGER NOT NULL DEFAULT 0,
  path            TEXT NOT NULL DEFAULT '',
  added_at        TEXT NOT NULL DEFAULT '',
  mbid            TEXT NOT NULL DEFAULT '',
  bpm             INTEGER NOT NULL DEFAULT 0,
  rg_track        REAL,
  rg_album        REAL,
  rg_peak         REAL,
  starred_at      TEXT NOT NULL DEFAULT '',
  rating          INTEGER NOT NULL DEFAULT 0,
  server_plays    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, id)
) WITHOUT ROWID;
CREATE INDEX songs_album ON songs(user_id, album_id, disc, track);
CREATE INDEX songs_artist ON songs(user_id, artist_id);
CREATE INDEX songs_aartist ON songs(user_id, album_artist_id);
CREATE INDEX songs_genre ON songs(user_id, genre);
CREATE INDEX songs_added ON songs(user_id, added_at);

CREATE TABLE albums (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id          TEXT NOT NULL,
  name        TEXT NOT NULL DEFAULT '',
  sort_name   TEXT NOT NULL DEFAULT '',
  artist      TEXT NOT NULL DEFAULT '',
  artist_id   TEXT NOT NULL DEFAULT '',
  year        INTEGER NOT NULL DEFAULT 0,
  genre       TEXT NOT NULL DEFAULT '',
  genres      TEXT NOT NULL DEFAULT '',
  song_count  INTEGER NOT NULL DEFAULT 0,
  duration    INTEGER NOT NULL DEFAULT 0,
  added_at    TEXT NOT NULL DEFAULT '',
  compilation INTEGER NOT NULL DEFAULT 0,
  mbid        TEXT NOT NULL DEFAULT '',
  starred_at  TEXT NOT NULL DEFAULT '',
  rating      INTEGER NOT NULL DEFAULT 0,
  server_plays INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, id)
) WITHOUT ROWID;
CREATE INDEX albums_artist ON albums(user_id, artist_id);
CREATE INDEX albums_added ON albums(user_id, added_at);

CREATE TABLE artists (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id          TEXT NOT NULL,
  name        TEXT NOT NULL DEFAULT '',
  sort_name   TEXT NOT NULL DEFAULT '',
  album_count INTEGER NOT NULL DEFAULT 0,
  song_count  INTEGER NOT NULL DEFAULT 0,
  mbid        TEXT NOT NULL DEFAULT '',
  starred_at  TEXT NOT NULL DEFAULT '',
  rating      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, id)
) WITHOUT ROWID;

-- Full-text search over the person's index (kind: song | album | artist).
CREATE VIRTUAL TABLE search_fts USING fts5(user_id UNINDEXED, kind UNINDEXED, item_id UNINDEXED, name, extra, tokenize = 'unicode61 remove_diacritics 2');

CREATE TABLE library_sync (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  server_key TEXT NOT NULL DEFAULT '',   -- server URL + account the index came from
  last_scan  TEXT NOT NULL DEFAULT '',   -- the server's lastScan when we synced
  synced_at  TEXT NOT NULL DEFAULT '',
  checked_at TEXT NOT NULL DEFAULT '',
  state      TEXT NOT NULL DEFAULT 'idle', -- idle | syncing | error
  progress   INTEGER NOT NULL DEFAULT 0,
  error      TEXT NOT NULL DEFAULT '',
  songs      INTEGER NOT NULL DEFAULT 0,
  albums     INTEGER NOT NULL DEFAULT 0,
  artists    INTEGER NOT NULL DEFAULT 0
);

-- Every play, owned by Pocket Music: the source of history, stats, mixes and
-- the Pocket Day entry. Song fields are copied so a listen survives the song
-- leaving the library (and imported listens may match no song at all).
CREATE TABLE listens (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  song_id    TEXT NOT NULL DEFAULT '',
  title      TEXT NOT NULL DEFAULT '',
  artist     TEXT NOT NULL DEFAULT '',
  album      TEXT NOT NULL DEFAULT '',
  album_id   TEXT NOT NULL DEFAULT '',
  artist_id  TEXT NOT NULL DEFAULT '',
  genre      TEXT NOT NULL DEFAULT '',
  duration   INTEGER NOT NULL DEFAULT 0,  -- the song's length, seconds
  played_at  TEXT NOT NULL,               -- UTC, when the play started
  ms_played  INTEGER NOT NULL DEFAULT 0,
  source     TEXT NOT NULL DEFAULT 'web', -- web | api | maloja | listenbrainz
  device     TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX listens_user_time ON listens(user_id, played_at);
CREATE INDEX listens_user_song ON listens(user_id, song_id);
CREATE INDEX listens_user_artist ON listens(user_id, artist);
CREATE UNIQUE INDEX listens_dedupe ON listens(user_id, played_at, title, artist);

-- Rule-based playlists, kept as a real playlist on the music server so every
-- Subsonic player (phone, car, desktop) sees them.
CREATE TABLE smart_playlists (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  description  TEXT NOT NULL DEFAULT '',
  rules        TEXT NOT NULL DEFAULT '{}',
  sort         TEXT NOT NULL DEFAULT 'random',
  limit_count  INTEGER NOT NULL DEFAULT 100,
  limit_minutes INTEGER NOT NULL DEFAULT 0,
  playlist_id  TEXT NOT NULL DEFAULT '',     -- on the music server
  song_count   INTEGER NOT NULL DEFAULT 0,
  duration     INTEGER NOT NULL DEFAULT 0,
  refresh      TEXT NOT NULL DEFAULT 'daily', -- daily | manual
  refreshed_at TEXT NOT NULL DEFAULT '',
  error        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX smart_user ON smart_playlists(user_id);

-- Where each device is: what plays, the queue, the position. Lets a person
-- see "playing on iPhone", move playback between devices and pick up later.
CREATE TABLE devices (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  id          TEXT NOT NULL,
  name        TEXT NOT NULL DEFAULT '',
  song_id     TEXT NOT NULL DEFAULT '',
  queue       TEXT NOT NULL DEFAULT '[]',
  queue_index INTEGER NOT NULL DEFAULT 0,
  position_ms INTEGER NOT NULL DEFAULT 0,
  playing     INTEGER NOT NULL DEFAULT 0,
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (user_id, id)
);

-- Albums asked for through Lidarr, to say when they arrive.
CREATE TABLE requests (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  artist      TEXT NOT NULL DEFAULT '',
  album       TEXT NOT NULL DEFAULT '',
  year        INTEGER NOT NULL DEFAULT 0,
  artist_mbid TEXT NOT NULL DEFAULT '',
  album_mbid  TEXT NOT NULL DEFAULT '',
  lidarr_album_id INTEGER NOT NULL DEFAULT 0,
  cover_url   TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL DEFAULT 'requested', -- requested | downloading | arrived | failed
  detail      TEXT NOT NULL DEFAULT '',
  arrived_at  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX requests_user ON requests(user_id, state);

-- History imports (Maloja, ListenBrainz).
CREATE TABLE imports (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source      TEXT NOT NULL,
  account     TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL DEFAULT 'running', -- running | done | error
  seen        INTEGER NOT NULL DEFAULT 0,
  imported    INTEGER NOT NULL DEFAULT 0,
  matched     INTEGER NOT NULL DEFAULT 0,
  error       TEXT NOT NULL DEFAULT '',
  started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  finished_at TEXT NOT NULL DEFAULT ''
);
