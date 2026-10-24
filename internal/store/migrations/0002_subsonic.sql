-- A person's login for Subsonic apps (phone, car, desktop players) that talk
-- to Pocket Music's /rest gateway. The app password is sealed with
-- DATA_DIR/secret.key: the Subsonic token scheme (md5(password + salt)) needs
-- it in clear text to verify a request.
CREATE TABLE subsonic_logins (
  user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  username     TEXT NOT NULL UNIQUE COLLATE NOCASE,
  secret       TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  last_used_at TEXT NOT NULL DEFAULT '',
  last_client  TEXT NOT NULL DEFAULT ''
);
