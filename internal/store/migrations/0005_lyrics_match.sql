-- Most Subsonic servers report made-up song paths (Navidrome: Artist/Album/NN - Title.ext), so a
-- song file and the person's song are matched by artist, album and title as well.
CREATE INDEX songs_match ON songs(user_id, lower(artist), lower(album), lower(title));
CREATE INDEX lyrics_files_match ON lyrics_files(lower(artist), lower(album), lower(title));
