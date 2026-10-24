-- The lyrics index: every song file in the music folder (MUSIC_DIR), the
-- lyrics it has (a .lrc beside it, else lyrics in its own tags) and their
-- words, so the Lyrics page can list, filter and search the whole library.
-- Kept up to date by walking the folder: unchanged files (same size, times)
-- are not read again.
CREATE TABLE lyrics_files (
  id         INTEGER PRIMARY KEY,
  path       TEXT NOT NULL UNIQUE,          -- relative to MUSIC_DIR
  size       INTEGER NOT NULL DEFAULT 0,
  mod        INTEGER NOT NULL DEFAULT 0,    -- the song file's mtime (unix ns)
  lrc_mod    INTEGER NOT NULL DEFAULT 0,    -- the .lrc's mtime, 0 = none
  artist     TEXT NOT NULL DEFAULT '',
  title      TEXT NOT NULL DEFAULT '',
  album      TEXT NOT NULL DEFAULT '',
  duration   INTEGER NOT NULL DEFAULT 0,    -- seconds
  kind       TEXT NOT NULL DEFAULT 'none',  -- synced | plain | none
  source     TEXT NOT NULL DEFAULT '',      -- lrc | tag | ''
  lines      INTEGER NOT NULL DEFAULT 0,
  changed_at TEXT NOT NULL DEFAULT ''       -- when the index last saw its lyrics change
);
CREATE INDEX lyrics_files_kind ON lyrics_files(kind, path);
CREATE INDEX lyrics_files_changed ON lyrics_files(changed_at);

-- rowid = lyrics_files.id; words = the lyrics without timings
CREATE VIRTUAL TABLE lyrics_fts USING fts5(artist, title, album, words, tokenize = 'unicode61 remove_diacritics 2');

-- the Lyrics page finds each person's song for a file by its path
CREATE INDEX songs_path ON songs(user_id, path);
