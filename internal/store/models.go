package store

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Connection is how a person reaches the music server.
type Connection struct {
	UserID        int64  `json:"-"`
	Kind          string `json:"kind"` // direct | core | off
	ServerURL     string `json:"server_url"`
	Username      string `json:"username"`
	Secret        string `json:"-"` // sealed password
	ServerType    string `json:"server_type"`
	ServerVersion string `json:"server_version"`
	UpdatedAt     string `json:"updated_at"`
}

// Song is one track in a person's library index.
type Song struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Album         string   `json:"album"`
	AlbumID       string   `json:"album_id"`
	Artist        string   `json:"artist"`
	ArtistID      string   `json:"artist_id"`
	AlbumArtist   string   `json:"album_artist"`
	AlbumArtistID string   `json:"album_artist_id"`
	Genre         string   `json:"genre"`
	Genres        string   `json:"-"`
	Year          int      `json:"year"`
	Track         int      `json:"track"`
	Disc          int      `json:"disc"`
	Duration      int      `json:"duration"`
	Bitrate       int      `json:"bitrate"`
	Suffix        string   `json:"suffix"`
	Size          int64    `json:"size"`
	BitDepth      int      `json:"bit_depth"`
	SampleRate    int      `json:"sample_rate"`
	Path          string   `json:"-"`
	AddedAt       string   `json:"added_at"`
	MBID          string   `json:"mbid"`
	BPM           int      `json:"bpm"`
	RGTrack       *float64 `json:"rg_track"`
	RGAlbum       *float64 `json:"rg_album"`
	RGPeak        *float64 `json:"rg_peak"`
	StarredAt     string   `json:"starred_at"`
	Rating        int      `json:"rating"`
	ServerPlays   int      `json:"-"`

	// from the person's listens (filled where asked for)
	Plays      int    `json:"plays,omitempty"`
	LastPlayed string `json:"last_played,omitempty"`
}

// Album is one album in a person's library index.
type Album struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SortName    string `json:"-"`
	Artist      string `json:"artist"`
	ArtistID    string `json:"artist_id"`
	Year        int    `json:"year"`
	Genre       string `json:"genre"`
	Genres      string `json:"-"`
	SongCount   int    `json:"song_count"`
	Duration    int    `json:"duration"`
	AddedAt     string `json:"added_at"`
	Compilation bool   `json:"compilation"`
	MBID        string `json:"mbid"`
	StarredAt   string `json:"starred_at"`
	Rating      int    `json:"rating"`
	ServerPlays int    `json:"-"`

	Plays      int    `json:"plays,omitempty"`
	LastPlayed string `json:"last_played,omitempty"`
}

// Artist is one artist in a person's library index.
type Artist struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SortName   string `json:"-"`
	AlbumCount int    `json:"album_count"`
	SongCount  int    `json:"song_count"`
	MBID       string `json:"mbid"`
	StarredAt  string `json:"starred_at"`
	Rating     int    `json:"rating"`

	Plays      int    `json:"plays,omitempty"`
	LastPlayed string `json:"last_played,omitempty"`
}

// SyncState is the library index's state for one person.
type SyncState struct {
	ServerKey string `json:"-"`
	LastScan  string `json:"last_scan"`
	SyncedAt  string `json:"synced_at"`
	CheckedAt string `json:"checked_at"`
	State     string `json:"state"`
	Progress  int    `json:"progress"`
	Error     string `json:"error"`
	Songs     int    `json:"songs"`
	Albums    int    `json:"albums"`
	Artists   int    `json:"artists"`
}

// Listen is one play.
type Listen struct {
	ID        int64  `json:"id"`
	SongID    string `json:"song_id"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album"`
	AlbumID   string `json:"album_id"`
	ArtistID  string `json:"artist_id"`
	Genre     string `json:"genre"`
	Duration  int    `json:"duration"`
	PlayedAt  string `json:"played_at"`
	MSPlayed  int64  `json:"ms_played"`
	Source    string `json:"source"`
	Device    string `json:"device"`
	CreatedAt string `json:"created_at"`
}

// SmartPlaylist is a rule-based playlist.
type SmartPlaylist struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Rules        Rules  `json:"rules"`
	Sort         string `json:"sort"`
	LimitCount   int    `json:"limit_count"`
	LimitMinutes int    `json:"limit_minutes"`
	PlaylistID   string `json:"playlist_id"`
	SongCount    int    `json:"song_count"`
	Duration     int    `json:"duration"`
	Refresh      string `json:"refresh"`
	RefreshedAt  string `json:"refreshed_at"`
	Error        string `json:"error"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// Rules are a smart playlist's conditions.
type Rules struct {
	Match      string `json:"match"` // all | any
	Conditions []Rule `json:"conditions"`
}

// Rule is one condition: field op value (value2 for "between").
type Rule struct {
	Field  string `json:"field"`
	Op     string `json:"op"`
	Value  string `json:"value"`
	Value2 string `json:"value2,omitempty"`
}

// Device is where a person plays.
type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SongID     string `json:"song_id"`
	Queue      string `json:"-"`
	QueueIndex int    `json:"queue_index"`
	PositionMS int64  `json:"position_ms"`
	Playing    bool   `json:"playing"`
	UpdatedAt  string `json:"updated_at"`
}

// Request is an album asked for through Lidarr.
type Request struct {
	ID            int64  `json:"id"`
	Artist        string `json:"artist"`
	Album         string `json:"album"`
	Year          int    `json:"year"`
	ArtistMBID    string `json:"artist_mbid"`
	AlbumMBID     string `json:"album_mbid"`
	LidarrAlbumID int64  `json:"lidarr_album_id"`
	CoverURL      string `json:"cover_url"`
	State         string `json:"state"`
	Detail        string `json:"detail"`
	ArrivedAt     string `json:"arrived_at"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// Import is one history import.
type Import struct {
	ID         int64  `json:"id"`
	Source     string `json:"source"`
	Account    string `json:"account"`
	State      string `json:"state"`
	Seen       int    `json:"seen"`
	Imported   int    `json:"imported"`
	Matched    int    `json:"matched"`
	Error      string `json:"error"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// Settings are the box-wide title plus one person's preferences.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat string `json:"date_format"`
	TimeFormat string `json:"time_format"`

	// Playback defaults (each device may override its streaming quality).
	Quality       string `json:"quality"`        // auto | original | 320 | 256 | 192 | 128
	Format        string `json:"format"`         // mp3 | aac | opus (when transcoding)
	ReplayGain    string `json:"replay_gain"`    // off | track | album | auto
	Crossfade     int    `json:"crossfade"`      // seconds, 0 = gapless
	Scrobble      bool   `json:"scrobble"`       // tell the music server (it forwards to Last.fm / ListenBrainz / Maloja)
	ScrobblePct   int    `json:"scrobble_pct"`   // a play counts after this share of the song (or 4 min)
	SkipAmbient   bool   `json:"skip_ambient"`   // keep ambient / background genres out of mixes
	AmbientGenres string `json:"ambient_genres"` // which genres count as background
	HideGenres    string `json:"hide_genres"`    // never in mixes or radio

	// What's new: tell the person when requested albums arrive, and once a week a recap.
	NotifyURL     string `json:"notify_url"`
	NotifyFormat  string `json:"notify_format"` // apprise | ntfy | json
	NotifyArrived bool   `json:"notify_arrived"`
	NotifyWeekly  bool   `json:"notify_weekly"`

	// Lidarr: discographies, missing albums, requests.
	LidarrMode string `json:"lidarr_mode"` // core | direct | off
	LidarrURL  string `json:"lidarr_url"`
	LidarrKey  string `json:"lidarr_key"`

	// Maloja / ListenBrainz: where past listening history comes from.
	MalojaURL         string `json:"maloja_url"`
	ListenBrainzUser  string `json:"listenbrainz_user"`
	ListenBrainzToken string `json:"listenbrainz_token"`
	ListenBrainzURL   string `json:"listenbrainz_url"`
	LyricsOnline      bool   `json:"lyrics_online"` // look up LRCLIB when the server has no lyrics
	LyricsPrefer      string `json:"lyrics_prefer"` // synced (synced wins, wherever from) | library (the files' own first)
	LyricsSize        string `json:"lyrics_size"`   // s | m | l
	LyricsAuto        bool   `json:"lyrics_auto"`   // Now Playing opens on the lyrics when a song has synced ones

	// Daily mixes also written as playlists on the music server (for Subsonic apps).
	MixesToServer bool `json:"mixes_to_server"`

	// The lyrics library (box-wide, admins): synced lyrics fetched from LRCLIB
	// and saved as .lrc files next to the songs, so every player shows them.
	LyricsWriter    bool   `json:"lyrics_writer"`
	LyricsHour      int    `json:"lyrics_hour"`       // local hour of the nightly run
	LyricsRetryDays int    `json:"lyrics_retry_days"` // ask again for songs LRCLIB did not know after this many days
	LyricsPlain     string `json:"lyrics_plain"`      // tag (FLAC UNSYNCEDLYRICS) | lrc (untimed .lrc) | skip: lyrics without timings
	LyricsDelayMS   int    `json:"lyrics_delay_ms"`   // pause between LRCLIB requests
	LyricsOrder     string `json:"lyrics_order"`      // added (newest songs first) | path
	LyricsNotify    bool   `json:"lyrics_notify"`     // push a summary when a run found lyrics
	LyricsRescan    bool   `json:"lyrics_rescan"`     // ask the music server to scan after a run that wrote files

	// "Make a mix" turns a sentence into a playlist: Pocket AI through Core,
	// or an OpenAI-compatible API with the person's own key.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}
