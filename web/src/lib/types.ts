export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export interface Song {
  id: string; title: string; album: string; album_id: string; artist: string; artist_id: string; album_artist: string; album_artist_id: string
  genre: string; year: number; track: number; disc: number; duration: number; bitrate: number; suffix: string; size: number
  bit_depth: number; sample_rate: number; added_at: string; mbid: string; bpm: number
  rg_track: number | null; rg_album: number | null; rg_peak: number | null
  starred_at: string; rating: number; plays?: number; last_played?: string
}
export interface Album {
  id: string; name: string; artist: string; artist_id: string; year: number; genre: string; song_count: number; duration: number
  added_at: string; compilation: boolean; mbid: string; starred_at: string; rating: number; plays?: number; last_played?: string
}
export interface Artist { id: string; name: string; album_count: number; song_count: number; mbid: string; starred_at: string; rating: number; plays?: number; last_played?: string }
export interface Genre { name: string; songs: number; albums: number; plays: number }

export interface Playlist {
  id: string; name: string; comment: string; owner: string; public: boolean; songCount: number; duration: number; created: string; changed: string
  coverArt: string; readonly: boolean; mine: boolean; smart_id?: number; covers: string[]
}
export interface Rule { field: string; op: string; value: string; value2?: string }
export interface SmartPlaylist {
  id: number; name: string; description: string; rules: { match: 'all' | 'any'; conditions: Rule[] }; sort: string; limit_count: number; limit_minutes: number
  playlist_id: string; song_count: number; duration: number; refresh: 'daily' | 'manual'; refreshed_at: string; error: string; created_at: string; updated_at: string
}

export interface Mix { key: string; name: string; description: string; kind: string; song_count: number; duration: number; covers: string[]; color: string }

export interface Listen {
  id: number; song_id: string; title: string; artist: string; album: string; album_id: string; artist_id: string; genre: string
  duration: number; played_at: string; ms_played: number; source: string; device: string
}
export interface Top { key: string; name: string; artist?: string; album?: string; album_id?: string; artist_id?: string; plays: number; seconds: number }
export interface Totals { plays: number; seconds: number; artists: number; albums: number; songs: number; days: number; new_songs: number }
export interface Bucket { key: string; plays: number; seconds: number }
export interface Stats {
  period: { key: string; label: string; from: string; to: string }; totals: Totals; previous: Totals
  artists: Top[]; albums: Top[]; songs: Top[]; genres: Top[]; timeline: Bucket[]; timeline_by: 'day' | 'month'; hours: Bucket[]; weekdays: Bucket[]
  streak: { current: number; longest: number }; all_plays: number; first_play: string; years: string[]
}

export interface SyncState { last_scan: string; synced_at: string; checked_at: string; state: string; progress: number; error: string; songs: number; albums: number; artists: number }
export interface Connection {
  connected: boolean; message?: string; mode: 'direct' | 'core' | 'off'; via?: 'direct' | 'core'; account?: string; username?: string; custom_url?: string
  server_type?: string; server_version?: string; public_url?: string; server_url: boolean; core: boolean; core_configured: boolean; sync: SyncState; syncing: boolean
}

export interface Device {
  id: string; name: string; song_id: string; queue_index: number; position_ms: number; playing: boolean; updated_at: string
  song: Song | null; active: boolean; queue_length: number
}

export interface Request {
  id: number; artist: string; album: string; year: number; artist_mbid: string; album_mbid: string; lidarr_album_id: number; cover_url: string
  state: 'requested' | 'downloading' | 'arrived' | 'failed'; detail: string; arrived_at: string; created_at: string; updated_at: string
}
export interface DiscAlbum {
  lidarr_id: number; foreign_id: string; title: string; year: number; type: string; secondary: boolean; monitored: boolean; have: number; total: number
  cover: string; library_id: string; state: 'have' | 'partial' | 'missing' | 'downloading' | 'requested' | 'upcoming'; download_left?: number; download_total?: number
}
export interface Release extends DiscAlbum { artist: string; artist_id: string; date: string }

export interface Lyrics { source: '' | 'server' | 'lrclib'; synced: boolean; lines: { t: number; text: string }[] }

export interface ImportRun { id: number; source: string; account: string; state: 'running' | 'done' | 'error'; seen: number; imported: number; matched: number; error: string; started_at: string; finished_at: string }

export interface Settings {
  core_configured: boolean; core_registered?: boolean
  title: string; date_format: string; time_format: string
  quality: string; format: string; replay_gain: string; crossfade: number; scrobble: boolean; scrobble_pct: number
  skip_ambient: boolean; ambient_genres: string; hide_genres: string
  notify_url: string; notify_format: string; notify_arrived: boolean; notify_weekly: boolean; notify_configured: boolean
  lidarr_mode: 'core' | 'direct' | 'off'; lidarr_url: string; lidarr_key: string
  maloja_url: string; maloja_default: string; listenbrainz_user: string; listenbrainz_token: string
  lyrics_online: boolean; lyrics_available: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string; navidrome_url: boolean; navidrome_public_url: string
}

export interface Home {
  sync: SyncState; syncing: boolean; not_connected?: string; totals: { songs: number; albums: number; artists: number; seconds: number; starred: number }
  jump_back?: Album[]; recently_added?: Album[]; on_this_day?: Album[]; rediscover?: Album[]; never_played?: Album[]; top_artists?: Top[]
  mixes?: Mix[]; requests?: Request[]; today?: Totals; streak?: { current: number; longest: number }
}
