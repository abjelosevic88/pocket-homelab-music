-- The lyrics library: when each song (by its path in the music library) was
-- last asked for at LRCLIB and what came back, so songs LRCLIB doesn't know
-- yet are only retried every few days.
CREATE TABLE lyrics_attempts (
  path     TEXT PRIMARY KEY,
  tried_at TEXT NOT NULL,
  result   TEXT NOT NULL DEFAULT '' -- synced | plain | none
);

CREATE TABLE lyrics_runs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  finished_at TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL DEFAULT 'running', -- running | done | stopped | error
  trigger     TEXT NOT NULL DEFAULT '',        -- schedule | manual | folder
  total       INTEGER NOT NULL DEFAULT 0,
  checked     INTEGER NOT NULL DEFAULT 0,
  have        INTEGER NOT NULL DEFAULT 0,      -- already had a .lrc
  fetched     INTEGER NOT NULL DEFAULT 0,
  plain       INTEGER NOT NULL DEFAULT 0,
  not_found   INTEGER NOT NULL DEFAULT 0,
  backoff     INTEGER NOT NULL DEFAULT 0,
  missing     INTEGER NOT NULL DEFAULT 0,      -- song files without artist or title tags
  errors      INTEGER NOT NULL DEFAULT 0,
  note        TEXT NOT NULL DEFAULT ''
);
