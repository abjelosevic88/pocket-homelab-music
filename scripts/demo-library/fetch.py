#!/usr/bin/env python3
"""Fetch the Pocket Music demo library from archive.org.

Downloads a pinned set of Creative Commons / public-domain albums into
    <target>/<Album Artist>/<YYYY> - <Album>/NN. Title.mp3   (+ cover.jpg)
and rewrites every file's tags (ID3v2.4) so title, artist, album,
albumartist, track, date and genre are consistent.

Standard library only. Idempotent: existing tracks and covers are skipped
(use --retag to rewrite tags on files that are already there).

Each album's license is checked against the live archive.org metadata API
before anything is downloaded; an album whose license changed is skipped.
Downloads are sequential and identify themselves with a User-Agent.

Usage:
    scripts/demo-library/fetch.py [TARGET_DIR] [--retag] [--only ID ...]
    scripts/demo-library/fetch.py --write-credits scripts/demo-library/CREDITS.md
"""

import argparse
import hashlib
import json
import os
import re
import struct
import sys
import time
import urllib.parse
import urllib.request

USER_AGENT = (
    "pocket-music-demo-library/1.0 "
    "(+https://github.com/abjelosevic88/pocket-homelab-music; sequential demo fetch)"
)
DEFAULT_TARGET = os.path.expanduser("~/.cache/pocket-music-dev/demo-library")

# Licenses we accept for redistribution in a public demo library.
ALLOWED_LICENSES = (
    re.compile(r"^creativecommons\.org/licenses/by/[0-9.]+(/[a-z]{2})?$"),
    re.compile(r"^creativecommons\.org/licenses/by-sa/[0-9.]+(/[a-z]{2})?$"),
    re.compile(r"^creativecommons\.org/publicdomain/zero/1\.0$"),
    re.compile(r"^creativecommons\.org/publicdomain/mark/1\.0$"),
)

# Pinned album list. "tracks" are (file name inside the archive.org item,
# clean title); the track number is the list position.
ALBUMS = [
    {
        "id": "mt022",
        "artist": "Lena Selyanina",
        "album": "Piano Paintings",
        "year": 2009,
        "genre": "Classical",
        "license": "http://creativecommons.org/licenses/by/3.0/",
        "cover": "mt022_artwork_front400x400.jpg",
        "tracks": [
            ("01_-_Footsteps.mp3", "Footsteps"),
            ("02_-_Spring.mp3", "Spring"),
            ("03_-_Spirit_of_St._Louis.mp3", "Spirit of St. Louis"),
            ("04_-_An_Empty_Stage.mp3", "An Empty Stage"),
            ("05_-_Woman_with_Black_Cravat.mp3", "Woman with Black Cravat"),
            ("06_-_Zona.mp3", "Zona"),
            ("07_-_Mountain_Heights.mp3", "Mountain Heights"),
            ("08_-_Rameau_in_Meditation.mp3", "Rameau in Meditation"),
        ],
    },
    {
        "id": "jamendo-180361",
        "artist": "Sergey Cheremisinov",
        "album": "Forgotten Stars",
        "year": 2015,
        "genre": "Modern Classical",
        "license": "http://creativecommons.org/licenses/by/3.0/",
        "cover": "cover.jpg",
        "tracks": [
            ("01-1588851-Sergey Cheremisinov-Labyrinth.mp3", "Labyrinth"),
            ("02-1588850-Sergey Cheremisinov-Back to Home.mp3", "Back to Home"),
            ("03-1588849-Sergey Cheremisinov-Mother_s Hands.mp3", "Mother's Hands"),
            ("04-1588852-Sergey Cheremisinov-Forgotten Stars.mp3", "Forgotten Stars"),
            ("05-1588853-Sergey Cheremisinov-Birds Walk Away.mp3", "Birds Walk Away"),
            ("06-1588854-Sergey Cheremisinov-Sirius.mp3", "Sirius"),
            ("07-1588855-Sergey Cheremisinov-The Loneliness.mp3", "The Loneliness"),
        ],
    },
    {
        "id": "KaiEngelTheRun",
        "artist": "Kai Engel",
        "album": "The Run",
        "year": 2017,
        "genre": "Electronic",
        "license": "http://creativecommons.org/licenses/by/3.0/",
        "cover": "cover.jpg",
        "tracks": [
            ("Kai Engel - The Run - 01 Seeker.mp3", "Seeker"),
            ("Kai Engel - The Run - 02 Run.mp3", "Run"),
            ("Kai Engel - The Run - 03 Harbor.mp3", "Harbor"),
            ("Kai Engel - The Run - 04 Imminence.mp3", "Imminence"),
            ("Kai Engel - The Run - 05 Passages.mp3", "Passages"),
            ("Kai Engel - The Run - 06 Scenery.mp3", "Scenery"),
            ("Kai Engel - The Run - 07 Tumult.mp3", "Tumult"),
            ("Kai Engel - The Run - 08 Chance.mp3", "Chance"),
            ("Kai Engel - The Run - 09 Denouement.mp3", "Denouement"),
        ],
    },
    {
        "id": "DirectionlessEP",
        "artist": "Broke For Free",
        "album": "Directionless EP",
        "year": 2011,
        "genre": "Hip-Hop",
        "license": "http://creativecommons.org/licenses/by/3.0/",
        "cover": "cover.jpg",
        "tracks": [
            ("Broke For Free - Directionless EP - 01 Night Owl.mp3", "Night Owl"),
            ("Broke For Free - Directionless EP - 02 My Always Mood.mp3", "My Always Mood"),
            ("Broke For Free - Directionless EP - 03 Day Bird.mp3", "Day Bird"),
            ("Broke For Free - Directionless EP - 04 My Luck.mp3", "My Luck"),
            ("Broke For Free - Directionless EP - 05 Mell's Parade.mp3", "Mell's Parade"),
            ("Broke For Free - Directionless EP - 06 Only Instrumental.mp3", "Only Instrumental"),
        ],
    },
    {
        "id": "ChrisZabriskieThoughtless",
        "artist": "Chris Zabriskie",
        "album": "Thoughtless",
        "year": 2015,
        "genre": "Ambient",
        "license": "http://creativecommons.org/licenses/by/3.0/",
        "cover": "cover.jpg",
        "tracks": [
            ("Chris Zabriskie - Thoughtless - 01 Everybody's Got Problems That Aren't Mine.mp3",
             "Everybody's Got Problems That Aren't Mine"),
            ("Chris Zabriskie - Thoughtless - 02 Another Version of You.mp3", "Another Version of You"),
            ("Chris Zabriskie - Thoughtless - 03 There's a Special Place for Some People.mp3",
             "There's a Special Place for Some People"),
            ("Chris Zabriskie - Thoughtless - 04 I Can't Imagine Where I'd Be Without It.mp3",
             "I Can't Imagine Where I'd Be Without It"),
            ("Chris Zabriskie - Thoughtless - 05 Rewound.mp3", "Rewound"),
        ],
    },
    {
        "id": "cz-blackhole",
        "artist": "Chris Zabriskie",
        "album": "The Black Hole",
        "year": 2006,
        "genre": "Indie Rock",
        "license": "https://creativecommons.org/licenses/by/4.0/",
        "cover": "Cover - The Black Hole.jpg",
        "tracks": [
            ("01 - You've Got the Body + I've Got the Brains.mp3", "You've Got the Body + I've Got the Brains"),
            ("02 - March to the Sky.mp3", "March to the Sky"),
            ("03 - I Will Take You Home.mp3", "I Will Take You Home"),
            ("04 - It Seems Like I Was Just Here.mp3", "It Seems Like I Was Just Here"),
            ("05 - Song About Ghosts + Family.mp3", "Song About Ghosts + Family"),
            ("06 - Set This Thing on Fire.mp3", "Set This Thing on Fire"),
            ("07 - I'm Not Through With You.mp3", "I'm Not Through With You"),
            ("08 - It's All Around You.mp3", "It's All Around You"),
            ("09 - The Stars Are Closer Than You.mp3", "The Stars Are Closer Than You"),
            ("10 - Grandfather.mp3", "Grandfather"),
        ],
    },
    {
        "id": "Monplaisir-RelaxingUkulele",
        "artist": "Monplaisir",
        "album": "Relaxing Ukulele",
        "year": 2017,
        "genre": "Acoustic",
        "license": "http://creativecommons.org/publicdomain/zero/1.0/",
        "cover": "cover.jpg",
        "tracks": [
            ("Monplaisir - Relaxing Ukulele - 01 Red Hair, Blue Sky.mp3", "Red Hair, Blue Sky"),
            ("Monplaisir - Relaxing Ukulele - 02 Sincere Love.mp3", "Sincere Love"),
            ("Monplaisir - Relaxing Ukulele - 03 Time Gap.mp3", "Time Gap"),
            ("Monplaisir - Relaxing Ukulele - 04 First.mp3", "First"),
            ("Monplaisir - Relaxing Ukulele - 05 Stay Quiet.mp3", "Stay Quiet"),
            ("Monplaisir - Relaxing Ukulele - 06 On the road, looking at your feet.mp3",
             "On the road, looking at your feet"),
            ("Monplaisir - Relaxing Ukulele - 07 Floating Temple.mp3", "Floating Temple"),
            ("Monplaisir - Relaxing Ukulele - 08 Hop.mp3", "Hop"),
        ],
    },
    {
        "id": "Monplaisir-FiftySecondsOfRain",
        "artist": "Monplaisir",
        "album": "Fifty Seconds of Rain",
        "year": 2017,
        "genre": "Folk",
        "license": "http://creativecommons.org/publicdomain/zero/1.0/",
        "cover": "cover.jpg",
        "tracks": [
            ("Monplaisir - Fifty seconds of rain - 01 No need to.mp3", "No need to"),
            ("Monplaisir - Fifty seconds of rain - 02 Close to you.mp3", "Close to you"),
            ("Monplaisir - Fifty seconds of rain - 03 Narcissus smells like headache.mp3",
             "Narcissus smells like headache"),
            ("Monplaisir - Fifty seconds of rain - 04 Skysand.mp3", "Skysand"),
            ("Monplaisir - Fifty seconds of rain - 05 Spell me your love.mp3", "Spell me your love"),
            ("Monplaisir - Fifty seconds of rain - 06 Waves.mp3", "Waves"),
            ("Monplaisir - Fifty seconds of rain - 07 Try to do something.mp3", "Try to do something"),
            ("Monplaisir - Fifty seconds of rain - 08 This is not a rock song.mp3", "This is not a rock song"),
        ],
    },
    {
        "id": "aer007",
        "artist": "Bluebridge Quartet",
        "album": "Adjusted for Low Noise Tape",
        "year": 2007,
        "genre": "Jazz",
        "license": "http://creativecommons.org/licenses/by-sa/3.0/",
        "cover": "bbqaer007_cover.jpg",
        "tracks": [
            ("Aer007_bluebridgeQuartet_01spektrum.mp3", "Spektrum"),
            ("Aer007_bluebridgeQuartet_02tandberg.mp3", "Tandberg"),
            ("Aer007_bluebridgeQuartet_03karp.mp3", "Karp"),
            ("Aer007_bluebridgeQuartet_04landet.mp3", "Landet"),
        ],
    },
    {
        "id": "komiku-the-adventure-goes-on-vol.-2",
        "artist": "Komiku",
        "album": "The Adventure Goes On, Vol. 2",
        "year": 2021,
        "genre": "Soundtrack",
        "license": "http://creativecommons.org/publicdomain/zero/1.0/",
        "cover": "cover.jpg",
        "tracks": [
            ("Komiku - The adventure goes on, vol.2 - 01 Morning's chill.mp3", "Morning's chill"),
            ("Komiku - The adventure goes on, vol.2 - 02 The Ceremony.mp3", "The Ceremony"),
            ("Komiku - The adventure goes on, vol.2 - 03 Another day in the country side.mp3",
             "Another day in the country side"),
            ("Komiku - The adventure goes on, vol.2 - 04 The mini game.mp3", "The mini game"),
            ("Komiku - The adventure goes on, vol.2 - 05 Awkward cute dance.mp3", "Awkward cute dance"),
            ("Komiku - The adventure goes on, vol.2 - 06 Should we be afraid or sad -.mp3",
             "Should we be afraid or sad?"),
            ("Komiku - The adventure goes on, vol.2 - 07 Intense fight.mp3", "Intense fight"),
            ("Komiku - The adventure goes on, vol.2 - 08 I miss you but I need to move forward.mp3",
             "I miss you but I need to move forward"),
        ],
    },
]


# --------------------------------------------------------------------------
# helpers
# --------------------------------------------------------------------------

def norm_license(url):
    u = (url or "").strip().lower()
    u = re.sub(r"^https?://", "", u)
    u = re.sub(r"^www\.", "", u)
    return u.rstrip("/")


def license_ok(url):
    n = norm_license(url)
    return any(p.match(n) for p in ALLOWED_LICENSES)


def license_name(url):
    n = norm_license(url)
    m = re.match(r"creativecommons\.org/licenses/([a-z-]+)/([0-9.]+)", n)
    if m:
        return "CC %s %s" % (m.group(1).upper(), m.group(2))
    if n.endswith("publicdomain/zero/1.0"):
        return "CC0 1.0 (public domain dedication)"
    if n.endswith("publicdomain/mark/1.0"):
        return "Public Domain Mark 1.0"
    return url


def canonical_license_url(url):
    return "https://" + norm_license(url) + "/"


def safe_name(s):
    s = s.replace("/", "-").replace("\\", "-").replace(":", " -")
    s = re.sub(r'[<>"|?*\x00-\x1f]', "", s)
    s = re.sub(r"\s+", " ", s).strip().rstrip(".")
    return s or "_"


def album_dir(root, a):
    return os.path.join(root, safe_name(a["artist"]), "%d - %s" % (a["year"], safe_name(a["album"])))


def http_get(url, timeout=120):
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    return urllib.request.urlopen(req, timeout=timeout)


def fetch_metadata(ident):
    last = None
    for attempt in range(4):
        try:
            with http_get("https://archive.org/metadata/" + urllib.parse.quote(ident), timeout=60) as r:
                data = json.load(r)
            if not data.get("metadata"):
                raise RuntimeError("item %s has no metadata (dark or missing?)" % ident)
            return data
        except Exception as e:  # noqa: BLE001 - retry anything network-ish
            last = e
            time.sleep(3 * (attempt + 1))
    raise RuntimeError("metadata for %s failed: %s" % (ident, last))


def file_urls(meta, ident, name):
    """Candidate URLs for one item file.

    The metadata API advertises the datanodes that hold the item
    ("workable_servers", "d1", "d2"); fetching from them directly is often much
    faster than the archive.org/download redirector, which may send us to a
    slow mirror. The redirector stays as the last resort.
    """
    qname = urllib.parse.quote(name)
    urls = []
    item_dir = meta.get("dir")
    if item_dir:
        servers = list(meta.get("workable_servers") or []) + [meta.get("d1"), meta.get("d2")]
        for s in servers:
            u = "https://%s%s/%s" % (s, urllib.parse.quote(item_dir), qname) if s else None
            if u and u not in urls:
                urls.append(u)
    urls.append("https://archive.org/download/%s/%s" % (urllib.parse.quote(ident), qname))
    return urls


def _md5_file(path):
    h = hashlib.md5()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def download(urls, dest, expect_md5=None, expect_size=None):
    """Download one item file to dest + ".part" (md5-verified, with retries).

    An interrupted download is resumed with an HTTP Range request, both on
    retry and on the next run of the script (archive.org can be slow).
    """
    part = dest + ".part"
    last = None
    t0 = time.time()
    for attempt in range(8):
        url = urls[attempt % len(urls)]
        try:
            have = os.path.getsize(part) if os.path.exists(part) else 0
            if expect_size is not None and have > expect_size:
                os.remove(part)
                have = 0
            if expect_size is None or have < expect_size:
                req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
                if have:
                    req.add_header("Range", "bytes=%d-" % have)
                with urllib.request.urlopen(req, timeout=180) as r:
                    mode = "ab" if have and r.status == 206 else "wb"
                    with open(part, mode) as f:
                        while True:
                            chunk = r.read(64 * 1024)
                            if not chunk:
                                break
                            f.write(chunk)
            n = os.path.getsize(part)
            if expect_size is not None and n != expect_size:
                raise RuntimeError("incomplete: %d of %d bytes" % (n, expect_size))
            if expect_md5 and _md5_file(part) != expect_md5:
                os.remove(part)
                raise RuntimeError("md5 mismatch, discarded")
            return part, n, time.time() - t0
        except Exception as e:  # noqa: BLE001 - retry anything network-ish
            last = e
            wait = 5 * (attempt + 1)
            print("      retry %d in %ds (%s)" % (attempt + 1, wait, e), flush=True)
            time.sleep(wait)
    raise RuntimeError("download failed for %s: %s (re-run to resume)" % (urls[-1], last))


# --------------------------------------------------------------------------
# minimal ID3 handling (strip whatever is there, write a clean ID3v2.4 tag)
# --------------------------------------------------------------------------

def _unsyncsafe(b):
    return (b[0] << 21) | (b[1] << 14) | (b[2] << 7) | b[3]


def _syncsafe(n):
    return bytes([(n >> 21) & 0x7F, (n >> 14) & 0x7F, (n >> 7) & 0x7F, n & 0x7F])


def strip_tags(data):
    """Return the raw MPEG audio stream with ID3v2/ID3v1/APEv2/Lyrics3 removed."""
    start = 0
    while data[start:start + 3] == b"ID3" and len(data) - start > 10:
        flags = data[start + 5]
        size = _unsyncsafe(data[start + 6:start + 10])
        start += 10 + size + (10 if flags & 0x10 else 0)
        # some taggers leave zero padding after the declared size
        while start < len(data) and data[start] == 0:
            start += 1
    end = len(data)
    changed = True
    while changed:
        changed = False
        if end - start >= 128 and data[end - 128:end - 125] == b"TAG":
            end -= 128
            changed = True
        if end - start >= 15 and data[end - 9:end] == b"LYRICS200":
            try:
                end -= 15 + int(data[end - 15:end - 9])
                changed = True
            except ValueError:
                pass
        if end - start >= 32 and data[end - 32:end - 24] == b"APETAGEX":
            _ver, size, flags = struct.unpack("<III", data[end - 24:end - 12])
            end -= size + (32 if flags & 0x80000000 else 0)
            changed = True
        if end - start >= 10 and data[end - 10:end - 7] == b"3DI":
            end -= 20 + _unsyncsafe(data[end - 4:end])
            changed = True
    return data[start:end]


def _text_frame(fid, text):
    body = b"\x03" + text.encode("utf-8")  # encoding 3 = UTF-8
    return fid.encode("ascii") + _syncsafe(len(body)) + b"\x00\x00" + body


def _url_frame(fid, url):
    body = url.encode("latin-1")
    return fid.encode("ascii") + _syncsafe(len(body)) + b"\x00\x00" + body


def build_id3(tags):
    frames = b"".join([
        _text_frame("TIT2", tags["title"]),
        _text_frame("TPE1", tags["artist"]),
        _text_frame("TPE2", tags["albumartist"]),
        _text_frame("TALB", tags["album"]),
        _text_frame("TRCK", tags["track"]),
        _text_frame("TPOS", "1/1"),
        _text_frame("TDRC", tags["date"]),
        _text_frame("TCON", tags["genre"]),
        _text_frame("TCOP", tags["copyright"]),
        _url_frame("WCOP", tags["license_url"]),
        _url_frame("WOAS", tags["source_url"]),
    ])
    padding = b"\x00" * 512
    body = frames + padding
    return b"ID3\x04\x00\x00" + _syncsafe(len(body)) + body


def write_tagged(src_path, dest_path, tags):
    with open(src_path, "rb") as f:
        audio = strip_tags(f.read())
    if not audio[:2] or audio[0] != 0xFF or (audio[1] & 0xE0) != 0xE0:
        raise RuntimeError("%s: no MPEG frame sync after stripping tags" % src_path)
    tmp = dest_path + ".tagging"
    with open(tmp, "wb") as f:
        f.write(build_id3(tags))
        f.write(audio)
    os.replace(tmp, dest_path)


def track_tags(a, idx, title):
    lic = canonical_license_url(a["license"])
    return {
        "title": title,
        "artist": a["artist"],
        "albumartist": a["artist"],
        "album": a["album"],
        "track": "%d/%d" % (idx, len(a["tracks"])),
        "date": str(a["year"]),
        "genre": a["genre"],
        "copyright": "%d %s, %s" % (a["year"], a["artist"], license_name(a["license"])),
        "license_url": lic,
        "source_url": "https://archive.org/details/" + a["id"],
    }


# --------------------------------------------------------------------------
# credits
# --------------------------------------------------------------------------

def credits_markdown():
    lines = [
        "# Demo library credits",
        "",
        "The Pocket Music demo library is assembled by `fetch.py` from the following",
        "albums on the Internet Archive. Every album is released by its artist under",
        "the license listed (checked against the archive.org metadata API at fetch",
        "time). The audio is not modified; only tags and file names are normalised.",
        "Thank you to the artists and netlabels for sharing their work.",
        "",
        "| Artist | Album | Year | Genre | License | Source |",
        "|---|---|---|---|---|---|",
    ]
    for a in ALBUMS:
        lines.append("| %s | %s | %d | %s | [%s](%s) | https://archive.org/details/%s |" % (
            a["artist"], a["album"], a["year"], a["genre"],
            license_name(a["license"]), canonical_license_url(a["license"]), a["id"]))
    lines.append("")
    return "\n".join(lines)


# --------------------------------------------------------------------------
# main
# --------------------------------------------------------------------------

def fetch_album(root, a, n, total, retag):
    print("[%d/%d] %s - %s (%d) [%s]" % (n, total, a["artist"], a["album"], a["year"], a["id"]), flush=True)
    meta = fetch_metadata(a["id"])
    live = meta["metadata"].get("licenseurl", "")
    if not license_ok(live):
        print("  !! license %r is not CC BY / BY-SA / CC0 / PD - skipping album" % live, flush=True)
        return False
    if norm_license(live) != norm_license(a["license"]):
        print("  !! license changed: pinned %s, live %s - skipping album" % (a["license"], live), flush=True)
        return False
    print("  license ok: %s" % license_name(live), flush=True)
    files = {f["name"]: f for f in meta.get("files", [])}

    d = album_dir(root, a)
    os.makedirs(d, exist_ok=True)
    ok = True

    # cover
    cover_src = files.get(a["cover"])
    existing_cover = [c for c in ("cover.jpg", "cover.png") if os.path.exists(os.path.join(d, c))]
    if existing_cover:
        print("  cover: exists", flush=True)
    elif not cover_src:
        print("  !! cover %r not in item" % a["cover"], flush=True)
        ok = False
    else:
        part, size, secs = download(file_urls(meta, a["id"], a["cover"]), os.path.join(d, "cover"),
                                    cover_src.get("md5"), int(cover_src["size"]) if cover_src.get("size") else None)
        with open(part, "rb") as f:
            magic = f.read(8)
        ext = "png" if magic.startswith(b"\x89PNG") else "jpg"
        os.replace(part, os.path.join(d, "cover." + ext))
        print("  cover: downloaded %.1f MB" % (size / 1e6), flush=True)

    width = max(2, len(str(len(a["tracks"]))))
    for idx, (src_name, title) in enumerate(a["tracks"], 1):
        fname = "%0*d. %s.mp3" % (width, idx, safe_name(title))
        dest = os.path.join(d, fname)
        label = "  %0*d/%0*d %s" % (width, idx, width, len(a["tracks"]), title)
        tags = track_tags(a, idx, title)
        if os.path.exists(dest):
            if retag:
                write_tagged(dest, dest, tags)
                print(label + " - exists, retagged", flush=True)
            else:
                print(label + " - exists, skip", flush=True)
            continue
        src = files.get(src_name)
        if not src:
            print(label + " - !! %r not in item" % src_name, flush=True)
            ok = False
            continue
        part, size, secs = download(file_urls(meta, a["id"], src_name), dest, src.get("md5"),
                                    int(src["size"]) if src.get("size") else None)
        write_tagged(part, dest, tags)
        os.remove(part)
        print(label + " - %.1f MB in %.0fs" % (size / 1e6, secs), flush=True)
    return ok


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("target", nargs="?", default=DEFAULT_TARGET,
                    help="library directory (default: %s)" % DEFAULT_TARGET)
    ap.add_argument("--retag", action="store_true", help="rewrite tags of files that already exist")
    ap.add_argument("--only", nargs="+", metavar="ID", help="only these archive.org identifiers")
    ap.add_argument("--write-credits", metavar="PATH", help="write CREDITS.md to PATH and exit")
    args = ap.parse_args()

    if args.write_credits:
        with open(args.write_credits, "w", encoding="utf-8") as f:
            f.write(credits_markdown())
        print("wrote", args.write_credits)
        return 0

    root = os.path.abspath(os.path.expanduser(args.target))
    os.makedirs(root, exist_ok=True)
    albums = [a for a in ALBUMS if not args.only or a["id"] in args.only]
    print("Pocket Music demo library -> %s (%d albums)" % (root, len(albums)), flush=True)
    failed = []
    for n, a in enumerate(albums, 1):
        try:
            if not fetch_album(root, a, n, len(albums), args.retag):
                failed.append(a["id"])
        except Exception as e:  # noqa: BLE001 - keep going with the other albums
            print("  !! %s" % e, flush=True)
            failed.append(a["id"])

    with open(os.path.join(root, "CREDITS.md"), "w", encoding="utf-8") as f:
        f.write(credits_markdown())

    total_bytes = 0
    tracks = 0
    for dirpath, _dirs, fnames in os.walk(root):
        for fn in fnames:
            total_bytes += os.path.getsize(os.path.join(dirpath, fn))
            tracks += fn.endswith(".mp3")
    print("done: %d tracks, %.1f MB in %s" % (tracks, total_bytes / 1e6, root), flush=True)
    if failed:
        print("incomplete albums: %s (re-run to retry)" % ", ".join(failed), flush=True)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
