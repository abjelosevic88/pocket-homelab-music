# Backup

Everything Pocket Music keeps is in `DATA_DIR` (`/data` in the container):

| File | What it is |
|---|---|
| `pocket-music.db` (+ `-wal`, `-shm`) | Plays, smart playlists, requests, settings, accounts, and each person's library index |
| `secret.key` | Seals the music-server passwords people typed in. Lose it and they type them again; nothing else is lost. |
| `core.token`, `core.token.oidc.json` | The app's Pocket Core registration (re-created by registering again) |
| `backups/` | Snapshots made by `pocket-music backup` or Settings → Backup & data |

The library index is rebuilt from the music server on its own, so what matters is plays, smart playlists and
settings. Your music, playlists, stars and ratings live on the music server — back that up there.

**Never copy the live database file.** Use one of:

```sh
docker exec pocket-music pocket-music backup            # writes data/backups/pocket-music-<time>.db
curl -H "Authorization: Bearer pmu_…" https://music.example.com/api/v1/backup -o music.db   # admins
```

Each person can export their own plays as CSV or JSON (with smart playlists) under Settings → Backup & data, and
import the JSON on another box.

To restore: stop the app, replace `pocket-music.db` with a snapshot (delete stale `-wal`/`-shm`), start it.
