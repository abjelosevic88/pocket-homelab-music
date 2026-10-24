# Configuration

Everything is read from environment variables; every value has a default, so a bare `docker run -v ./data:/data` works.
Integration values seed the owner's (first admin's) own settings on first start, once; after that the values saved under
**Settings** win, and clearing one there keeps it cleared.

## Server

| Variable | Default | What it does |
|---|---|---|
| `PORT` / `HOST` | `8080` / `0.0.0.0` | Listen address |
| `BASE_URL` | – | Public URL: notification links, the OIDC callback, what Pocket Core shows |
| `DATA_DIR` | `./data` (`/data` in the image) | SQLite database, `secret.key`, backups |
| `DB_PATH` | `$DATA_DIR/pocket-music.db` | Override the database location |
| `TZ` | – | Time zone for "today", streaks and the weekly recap |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug`, `warn`, `error`; `json` |
| `TRUST_PROXY` | `true` | Honour `X-Forwarded-For` |
| `METRICS_ENABLED` | `true` | Prometheus `/metrics` (row counts only, never content) |
| `USER_AGENT` | `PocketMusic/1.0 (+repo URL)` | Sent to LRCLIB, Maloja, ListenBrainz |

## Sign-in

| Variable | Default | What it does |
|---|---|---|
| `AUTH_MODE` | `local` | `local` (built-in accounts), `proxy` (trust `AUTH_PROXY_HEADER`), `none` (single user, LAN only) |
| `AUTH_PROXY_HEADER` / `AUTH_PROXY_EMAIL_HEADER` | `Remote-User` / `Remote-Email` | Headers for proxy mode |
| `SESSION_TTL` | `720h` | Cookie lifetime |
| `SECURE_COOKIES` | `false` | Set `true` behind HTTPS (on plain http the browser drops the cookie and login bounces) |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | Create the first admin on an empty database (otherwise the UI asks) |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | – | Sign in with an OpenID Connect provider (PocketID, Authelia, Authentik, Keycloak…). The callback is `BASE_URL/api/v1/auth/oidc/callback`. With Pocket Core none of these are needed. |
| `OIDC_NAME` / `OIDC_AUTO_CREATE` | `single sign-on` / `true` | Button label; give an unknown verified email a member account |

## The music server

Pocket Music never touches your files. It plays them through a music server that speaks the (Open)Subsonic API —
[Navidrome](https://www.navidrome.org) is the one it is built and tested against; Gonic, Airsonic-Advanced and
others work for everything the Subsonic API covers. **Every person uses their own account** on that server, so
playlists, loves, ratings and play counts on the server stay theirs.

| Variable | Default | What it does |
|---|---|---|
| `NAVIDROME_URL` | – | The server's address as this container reaches it (`http://navidrome:4533`). Without it, each person types an address under Settings → Library. |
| `NAVIDROME_PUBLIC_URL` | – | What a browser opens for "Open the server" |
| `NAVIDROME_USER` / `NAVIDROME_PASSWORD` | – | The owner's own account, copied once into their connection (the password is sealed with `DATA_DIR/secret.key`) |
| `SYNC_EVERY` | `10m` | How often each person's library index is compared with the server's last scan |

With **Pocket Core**, a person's Navidrome login can come from Core instead (Core → Settings → My logins, service
type `navidrome`): nothing is stored here, and switching the link off in Core stops access at once.

What the server should have, for everything to work:

- **Transcoding** (Navidrome has it built in with ffmpeg): phones get 256 kbps instead of FLAC by default.
- **ReplayGain tags** in the files, for volume levelling.
- **A Last.fm API key** in Navidrome (`ND_LASTFM_APIKEY`): radio uses its "similar songs", artist pages its biographies
  and similar artists. Without it radio still works from your own listening and genres.
- Lyrics: embedded or `.lrc` files next to the songs are used first; LRCLIB fills the gaps.

## The lyrics library

With the music folder mounted **read-write** (the same folder the music server reads), an admin can have synced
lyrics fetched for the whole library and saved as `.lrc` files next to the songs (Settings → Lyrics): every
player — Navidrome, phone apps, the car — shows them, offline too. Everything about it is a setting there: the
nightly hour, how many days before a song LRCLIB didn't know is asked again, what happens to lyrics without timings
(the FLAC `UNSYNCEDLYRICS` tag, an untimed `.lrc`, or nothing), the pause between requests, newest-first or A–Z, a
scan request to the music server afterwards and a push with the result. A failing LRCLIB never counts as "not
found". It reads tags from the files itself, so it works before anyone has connected an account.

The **Lyrics** page (in the sidebar) shows every song in the music folder with the lyrics it has — synced, plain or
none, from a `.lrc` beside it or its own tags — and searches their words. Admins fetch the missing ones there (one
song, or every song matching a search, in the background), choose between LRCLIB's matches, edit the text, shift all
timings, sync plain lyrics by tapping along with the song, and delete wrong ones (optionally keeping the song from
being looked up again). Saving writes the files, so every app sees the change; Now Playing shows it at once. Songs
edited by hand are left alone by the nightly run. The page's index reads the folder after every run, once a day and
on *Re-read folder*; only new and changed files are read again.

| Variable | Default | What it does |
|---|---|---|
| `MUSIC_DIR` | – | Where the music folder is mounted in this container (e.g. `/music`, mounted `rw`). Without it the lyrics library is off; "Save to library" in Now Playing needs it too. |

Download hooks can fetch lyrics for a new album at once: `POST /api/v1/lyrics-library/run {"folder":"Artist/2024 - Album"}`
with an admin's token (the folder is relative to the music folder). Moving from a script that kept its own list of
songs already asked about (a JSON file of path → unix time, like `fetch-lyrics.py`'s `state.json`)?
`pocket-music import-lyrics-state state.json /old/music/root` — with the app stopped — carries it over.

## Optional services

| Variable | Default | What it does |
|---|---|---|
| `LIDARR_URL` / `LIDARR_API_KEY` | – | The owner's Lidarr: discographies with missing albums on artist pages, New releases, album requests. Everyone else connects their own (or through Pocket Core, service type `lidarr`) under Settings → Lidarr. |
| `MALOJA_URL` | – | Maloja to import listening history from (its read API is public) |
| `LYRICS_URL` | `https://lrclib.net` | LRCLIB for lyrics the files don't have; `off` disables online lookups for everybody |
| `NOTIFY_URL` / `NOTIFY_FORMAT` | – / guessed | Apprise `…/notify/<key>`, an ntfy topic URL or a JSON webhook: arrived albums, the weekly recap |
| `OPENAI_API_KEY` / `OPENAI_MODEL` / `OPENAI_BASE_URL` | – / `gpt-4o-mini` / OpenAI | The owner's AI for "Make a mix" (any OpenAI-compatible API: Ollama, LM Studio, OpenRouter). With Pocket Core, Pocket AI is used when its link is on. |

## Pocket Core

| Variable | Default | What it does |
|---|---|---|
| `CORE_URL` / `CORE_REGISTRATION_KEY` | – | [Pocket Core](https://github.com/abjelosevic88/pocket-homelab-core): register on start (the key is needed once; the app token is kept in `$DATA_DIR/core.token`), "Sign in with Pocket Core", accept the tokens Core gives paired phones, get each person's Navidrome / Lidarr / Maloja login from Core, use Pocket AI, publish events, offer `listening:read` and `playlists:write` to other Pocket apps |
| `CORE_APP_URL` | `BASE_URL` | How Core reaches this app for health checks (e.g. `http://host.docker.internal:8200`) |

With `CORE_URL` empty the app behaves exactly as a standalone app: every Core-only choice stays visible, greyed out
with a note.

## Demo data

`DEMO_DATA=true` gives the first account three months of made-up listening history (shaped like a real week:
evenings, weekends, a few favourite albums) and two smart playlists, once its library has been indexed. Use it with
the Creative Commons demo library in [scripts/demo-library](../scripts/demo-library).
