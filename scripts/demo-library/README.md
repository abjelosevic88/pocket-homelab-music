# Demo library

A small, real music library for README screenshots, local development and
end-to-end tests: about ten albums of Creative Commons / public-domain music
from the Internet Archive, with real cover art. Albums, licenses and sources
are listed in [CREDITS.md](CREDITS.md).

## Fetch

```sh
python3 scripts/demo-library/fetch.py                 # -> ~/.cache/pocket-music-dev/demo-library
python3 scripts/demo-library/fetch.py /some/other/dir  # custom target
```

- Python 3 standard library only (no mutagen/ffmpeg needed).
- 10 albums, 8 artists, 10 genres, 73 MP3 tracks, about 416 MB (archive.org's
  VBR MP3 derivative where one exists).
- Layout: `<Album Artist>/<YYYY> - <Album>/NN. Title.mp3` plus `cover.jpg`.
- Every album's license is checked against the archive.org metadata API
  before anything is downloaded, and every file is md5-checked.
- Tags are rewritten as ID3v2.4: title, artist, album artist, album, track,
  disc, date, genre, plus copyright/license URL and source URL.
- Safe to re-run: files that already exist are skipped. `--retag` rewrites
  tags in place; `--only <archive.org id>` limits the run to some albums.
- Downloads run one at a time with an identifying User-Agent, straight from
  the datanode the metadata API names. archive.org is sometimes very slow
  (10-20 KB/s); interrupted files resume on the next run.

After changing the album list, regenerate the credits:

```sh
python3 scripts/demo-library/fetch.py --write-credits scripts/demo-library/CREDITS.md
```

## Run a demo Navidrome

```sh
mkdir -p ~/.cache/pocket-music-dev/nd-demo
docker run -d --name pocket-music-demo-navidrome --restart no \
  -p 127.0.0.1:4535:4533 \
  -v ~/.cache/pocket-music-dev/nd-demo:/data \
  -v ~/.cache/pocket-music-dev/demo-library:/music:ro \
  -e ND_DEVAUTOCREATEADMINPASSWORD=devpass123 \
  -e ND_ENABLEINSIGHTSCOLLECTOR=false \
  -e ND_AUTOIMPORTPLAYLISTS=false \
  -e ND_LISTENBRAINZ_ENABLED=false \
  deluan/navidrome:latest
```

Log in as `admin` / `devpass123` at <http://127.0.0.1:4535>. The first scan
takes a few seconds. To check it from the command line:

```sh
curl -s 'http://127.0.0.1:4535/rest/getAlbumList2.view?f=json&v=1.16.1&c=dev&u=admin&p=devpass123&type=newest&size=50'
```

Start over with `docker rm -f pocket-music-demo-navidrome && rm -rf ~/.cache/pocket-music-dev/nd-demo`.
