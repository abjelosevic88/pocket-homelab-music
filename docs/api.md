# API

JSON over HTTP at `/api/v1`. Authenticate with the session cookie (the web app), a personal token from
Settings → API & shortcuts (`Authorization: Bearer pmu_…`), a token Pocket Core gave a paired phone, or a Pocket Core
app-link token (`pcx_…`, limited to the scopes below). Everything answers for the person the token belongs to; other
people's ids answer 404. Times are UTC RFC 3339; `?tz=<minutes east of UTC>` sets "today" for day-bucketed answers.

A person without a working music-server connection gets `409 {"code":"not_connected","error":"…"}` from anything
that needs the server.

## Library

| | |
|---|---|
| `GET /home` | Everything Home shows: mixes, jump back in, recently added, on this day, rediscover, never played, top artists, requests, today, streak, library totals and index state |
| `GET /albums?sort=&genre=&year=&decade=&artist=&starred=1&unplayed=1&limit=&offset=&seed=` | Albums; `sort` = `added` `played` `plays` `name` `artist` `year` `year_asc` `random` `starred` |
| `GET /albums/{id}?info=1` | An album with songs, more by the artist, quality, notes (Last.fm) |
| `GET /artists?sort=&starred=1&all=1` · `GET /artists/{id}?info=1` | Artists; an artist with albums, appears on, top songs, biography, similar artists in the library |
| `GET /artists/{id}/discography` | The artist's albums in Lidarr matched to the library (`state`: have, partial, missing, downloading, requested, upcoming) |
| `GET /songs?sort=&genre=&artist=&starred=1&lossless=1&decade=` · `GET /songs/{id}` · `POST /songs/by-id {"ids":[…]}` | Songs |
| `GET /genres` | Genres with song/album counts and your plays |
| `GET /search?q=&limit=` | Artists, albums and songs (accents ignored, prefix matching) |
| `POST /star {"kind":"song|album|artist","id":"…","on":true}` · `POST /rate {"kind","id","rating":0-5}` | Saved on the music server |
| `GET /lyrics/{songId}` | `{"source":"server|lrclib|","synced":true,"lines":[{"t":12300,"text":"…"}]}` (`t` in ms, -1 when not synced) |
| `GET /cover/{id}?size=` | Cover art (`al-<album>`, `ar-<artist>`, `pl-<playlist>` or a song id) |
| `GET /stream/{id}` · `?q=320&format=mp3|aac|opus&offset=<s>` | The original file with byte ranges, or transcoded by the server |
| `GET /download/{id}` | The original file as an attachment |
| `GET /sync` · `POST /sync` · `POST /sync?scan=1` | Index state; rebuild now; ask the server to scan for new files |

## Playing

| | |
|---|---|
| `POST /listens {"song_id","played_at":<unix ms or RFC 3339>,"ms_played","device"}` | Record a play (passed on to the music server when scrobbling is on). The same song at the same second counts once. |
| `DELETE /listens/{id}` | Remove a play |
| `POST /now {"device_id","device_name","song_id","queue":[ids],"queue_index","position_ms","playing","started"}` | A device's report; `queue` only when it changed |
| `GET /devices` · `GET /devices/{id}/queue` · `POST /devices/{id}/command {"action":"play|pause|toggle|next|previous|seek|volume","value"}` · `DELETE /devices/{id}` | Your devices, take one's queue over, remote control |
| `GET /events` | Server-sent events: `devices`, `device`, `command`, `sync`, `listen`, `import` |
| `GET /server/now-playing` | What your other Subsonic players are playing according to the server |

## Lyrics

| | |
|---|---|
| `GET /lyrics/{id}` | `{"source":"server|lrclib|","synced","lines":[{"t":<ms>,"text"}],"saveable"}` — the files' own first, LRCLIB after (as the person prefers) |
| `POST /lyrics/{id}/save` | Admins: write the song's LRCLIB lyrics as a `.lrc` next to it (needs `MUSIC_DIR`) |
| `GET /lyrics-library` | Admins: whether the music folder is writable, running, totals and the last 10 runs |
| `POST /lyrics-library/run` · `{"folder":"Artist/Album"}` | Admins: start a whole-library run (202), or do one folder now and answer with its counts |
| `POST /lyrics-library/stop` | Admins: stop a running run (the next one carries on) |

## Mixes, radio, playlists

| | |
|---|---|
| `GET /mixes` · `GET /mixes/{key}` | Today's mixes (with `mixes_to_server` on, also written to the music server each morning as “Pocket · …” playlists) (`daily-1`…`daily-4`, `repeat`, `rediscover`, `fresh`, `deep`, `capsule`, `loved`, `focus`) |
| `POST /mix {"prompt","count","save":false}` | Make a mix from a sentence; `save` also creates a playlist |
| `GET /radio?song=|album=|artist=|genre=&limit=` | Radio songs |
| `GET /shuffle?genre=&decade=&starred=1&limit=` | The library shuffled, artists spread out |
| `GET /playlists` · `POST /playlists {"name","comment","song_ids","public"}` · `GET /playlists/{id}` · `PUT /playlists/{id} {"name","comment","public","add":[ids],"remove":[positions],"song_ids":[…]}` · `DELETE /playlists/{id}` | Playlists on the music server |
| `GET /smart` · `POST /smart` · `GET|PUT|DELETE /smart/{id}` · `POST /smart/{id}/refresh` · `POST /smart/preview` | Smart playlists: `{"name","description","rules":{"match":"all|any","conditions":[{"field","op","value","value2"}]},"sort","limit_count","limit_minutes","refresh":"daily|manual"}` |

## History and stats

| | |
|---|---|
| `GET /history?before=&q=&limit=` · `GET /history/export[?format=json]` | Plays, newest first; CSV or JSON |
| `GET /stats?period=week|month|year|all|YYYY|YYYY-MM` | Totals (and the same span before), top artists/albums/songs/genres, timeline, hours, weekdays, streak |
| `GET /recap?period=YYYY` | "Your year in music" |
| `GET /day/{YYYY-MM-DD}` | One day for Pocket Day: summary sentence, totals, top artists and albums, every play |
| `GET /dashboard` | For a Homepage widget: `now_playing`, `minutes_today`, `plays_today`, `minutes_week`, `top_artist_week`, `songs`, `albums`, `artists`, `requests_open`, `streak` |
| `GET /imports` · `POST /imports {"source":"maloja","url"}` / `{"source":"listenbrainz","user","token"}` | Import listening history |

## Lidarr

| | |
|---|---|
| `GET /lidarr/status` | Whether your Lidarr connection works |
| `GET /lidarr/search?q=` | Artists and albums from MusicBrainz (through Lidarr), marked when already in the library |
| `POST /requests {"lidarr_id"}` or `{"foreign_id","artist_foreign_id","artist","title"}` | Monitor and search an album (adding its artist, monitoring nothing else, when needed) |
| `GET /requests` · `DELETE /requests/{id}` | Your requests: requested, downloading, arrived, failed |
| `GET /releases` | Lidarr's calendar for the last 45 and next 120 days |

## Subsonic apps (`/rest/*`)

Pocket Music answers the Subsonic API at `/rest/…` for phone, car and desktop apps (Amperfy, play:Sub, Symfonium,
Substreamer, Feishin…). An app signs in with the person's **app login** — a user name and a generated password
from Settings → Phone & car apps (token `t`+`s`, plain `p`/`enc:` or an OpenSubsonic `apiKey` = a `pmu_` token, or the token Pocket Core gave a paired phone — that is how Pocket Homelab signs in), never
their music server password. Every call is passed to the music server with the person's own account; `scrobble`
also lands in their history (device = the app's name) and "now playing" shows the app as a device. Ten wrong
passwords in 15 minutes lock a user name for a while.

| | |
|---|---|
| `GET /subsonic` | Your app login: user name, when it was made and last used, by which app |
| `POST /subsonic/password {"username"}` | Make or renew the app password (returned once); the user name is optional |
| `DELETE /subsonic` | Turn app sign-in off |

## Pocket Core app links

Other Pocket apps may act for a person (who allows it in Pocket Core) with these scopes:

- `listening:read` — `GET /dashboard`, `/history`, `/stats`, `/recap`, `/devices`, `/day/{date}`.
- `playlists:write` — `GET /search`, `/genres`, `/playlists`, `/songs|albums|artists|playlists/{id}`, `/cover/{id}`,
  `POST /playlists`, `PUT /playlists/{id}`.

Events published on the Core bus (each with `user_email`): `music.loved`, `music.playlist_created`,
`music.album_requested`, `music.album_arrived`, `music.mix_made`.

## Homepage widget

```yaml
- Music:
    href: https://music.example.com
    icon: https://music.example.com/favicon.svg
    widget:
      type: customapi
      url: https://music.example.com/api/v1/dashboard
      headers: { Authorization: Bearer pmu_… }
      mappings:
        - { field: now_playing, label: Playing }
        - { field: minutes_today, label: Today, suffix: min }
        - { field: top_artist_week, label: Top this week }
        - { field: streak, label: Streak, suffix: days }
```
