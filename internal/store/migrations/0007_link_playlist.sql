-- "Add from a link" can also put the new tracks in one of the person's playlists once the server has scanned them.
ALTER TABLE link_downloads ADD COLUMN playlist_id TEXT NOT NULL DEFAULT '';
ALTER TABLE link_downloads ADD COLUMN playlist_name TEXT NOT NULL DEFAULT '';
