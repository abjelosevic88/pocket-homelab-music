# How it works

## Why on top of Navidrome, not instead of it

A music server is mostly plumbing that Navidrome already does well: reading tags from tens of thousands of files
(TagLib), watching folders, transcoding with ffmpeg, cover art and artist images, the Subsonic API that every
music app on phones, desktops and car stereos speaks, and scrobbling to Last.fm / ListenBrainz. Rewriting that
would cost months and give nothing a listener can feel; worse, it would cut off every Subsonic client in the
house.

What a server does *not* do is what makes music feel personal: knowing what you actually listen to, making
something new out of your own shelves every day, telling you what's missing, connecting your music to the rest of
your life. That is what Pocket Music is:

| Navidrome (or any OpenSubsonic server) | Pocket Music |
|---|---|
| Files, tags, scanning, artwork | A fast player for the whole household, phone and desktop |
| Streaming and transcoding | Listening history it owns (plus Maloja / ListenBrainz imports), stats, a year recap |
| Playlists, stars, ratings (per account) | Daily mixes, radio, "make a mix" from a sentence, smart playlists *written back as real playlists* |
| The Subsonic API for every other app | Synced lyrics (files first, LRCLIB after), devices and hand-off between them |
| Scrobbling onwards | Lidarr: discographies with what's missing, new releases, requests that tell you when they arrive |
| | Pocket Core: one sign-in, per-person logins, Pocket AI, Pocket Day / dashboards via app links |

The split mirrors Pocket Photos on Immich. Each side keeps doing what it is best at, and you can keep using the
Navidrome web UI or any Subsonic app alongside: playlists, smart playlists, stars and play counts show up there too.

## Per person, always

Every person signs in to Pocket Music with their own account and plays with **their own** account on the music
server (typed in once and sealed with `DATA_DIR/secret.key`, or handed out by Pocket Core per request). Everything
Pocket Music keeps — the library index, plays, smart playlists, requests, devices, settings — is keyed by person;
another person's ids answer 404, and admins get no read access to anyone's listening.

## The library index

To make browsing, search, mixes and stats instant, each person's library (what their account can see) is copied
into SQLite: songs, albums, artists, genres, stars and a full-text index (accents folded, prefix matching). The copy
is fetched with `search3`'s empty query, 500 at a time, *before* any write, then swapped in in one transaction —
40,000 songs take about ten seconds. It is rebuilt after every new scan on the server (checked every
`SYNC_EVERY`), and once a day; stars are refreshed on every check because they change without a scan. Playing never
waits on the index: streams, covers and lyrics always go to the server.

## Playing

The browser never sees the server's credentials: streams, covers and downloads go through Pocket Music
(`/api/v1/stream/{id}`, byte ranges passed through). The player keeps two `<audio>` elements: the next song loads
while this one plays, so albums run gapless and crossfades are possible. Transcoded streams can't be seeked past
what is buffered, so a seek there restarts the stream at that second (`timeOffset`, the OpenSubsonic
`transcodeOffset` extension). If a browser can't decode the chosen format, it falls back to AAC by itself.

Volume levelling uses the files' ReplayGain tags (album gain when an album plays in order). The lock screen and
media keys work through the Media Session API.

A play counts once half the song (configurable) or four minutes have actually been heard — skipping around doesn't
count. It is stored here and passed on to the server, which forwards it to Last.fm, ListenBrainz or Maloja.

## Devices

Every open Pocket Music is a device that reports what it plays every 15 seconds (and on every change). The person's
other devices see it immediately over server-sent events: "Playing on iPhone", with pause and next, and **Play
here**, which moves the queue and the position over and pauses the other device.

## Mixes and radio

Everything is computed from the index and the person's plays, deterministically per day:

- **Daily Mix 1–4**: one per favourite genre (weighted by the last six months), about a third songs you know and
  love, the rest songs you haven't played by artists you like; never the same artist twice in a row.
- **On Repeat**, **Rediscover** (favourites not heard in four months), **Fresh in your library**, **Deep Cuts**
  (never-played songs by your most played artists), **Time Capsule** (this month in earlier years), **Loved**,
  **Focus** (background genres, kept out of everything else).
- **Radio** from a song, album, artist or genre blends the server's similar songs (Last.fm, cached for a day),
  what you played around that song before, and the same genre and era.
- **Make a mix**: the AI (Pocket AI or your own key) sees only genre names and artist names, never files or plays,
  and answers with a plan — genres, artists, years, how familiar — that runs over your library here. Without AI the
  plan is read from the words: genre names, artist names, decades ("80s"), moods ("rainy", "workout", "focus").

## Smart playlists

Rules over the index and your plays (genre, artist, year, plays, last played, added, loved, never played, rating,
length, format, bitrate, BPM, folder), sorted and limited, are evaluated here and written to a real playlist on the
music server, refreshed daily. Your phone app and car stereo see them like any other playlist.

## Lidarr

An artist page asks Lidarr for the artist's whole discography (by MusicBrainz id, else by name) and matches it to
your library: missing albums are greyed and one click from being searched for. New releases come from Lidarr's
calendar, filtered to artists you have. Requests are followed every 15 minutes; when every track has arrived the
person gets a push and the music server is asked to scan.
