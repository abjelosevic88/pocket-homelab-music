# Tags one downloaded file for "From a link" (run as python3 -I -c <this> <json>).
# Every tag the site wrote is replaced; the picture (a square JPEG) is embedded.
import base64, json, sys
from mutagen.flac import FLAC, Picture
from mutagen.id3 import ID3, APIC, COMM, TALB, TCON, TDRC, TIT2, TPE1, TPE2, TRCK, ID3NoHeaderError
from mutagen.mp4 import MP4, MP4Cover
from mutagen.oggopus import OggOpus
from mutagen.oggvorbis import OggVorbis

a = json.loads(sys.argv[1])
path, tags = a["file"], {k: v for k, v in a["tags"].items() if v}
pic = open(a["picture"], "rb").read() if a.get("picture") else None
ext = path.rsplit(".", 1)[-1].lower()


def vorbis_picture():
    p = Picture()
    p.type, p.mime, p.desc, p.data = 3, "image/jpeg", "Cover", pic
    return p


if ext in ("opus", "ogg", "oga", "flac"):
    if ext == "flac":
        f = FLAC(path)
    else:
        try:
            f = OggOpus(path)
        except Exception:
            f = OggVorbis(path)
    if f.tags is None:
        f.add_tags()
    f.tags.clear()
    for k, v in tags.items():
        f[k] = [v]
    if pic:
        if ext == "flac":
            f.clear_pictures()
            f.add_picture(vorbis_picture())
        else:
            f["metadata_block_picture"] = [base64.b64encode(vorbis_picture().write()).decode("ascii")]
    f.save()
elif ext in ("m4a", "mp4", "aac"):
    f = MP4(path)
    if f.tags is None:
        f.add_tags()
    f.tags.clear()
    keys = {"title": "\xa9nam", "artist": "\xa9ART", "albumartist": "aART", "album": "\xa9alb", "date": "\xa9day", "genre": "\xa9gen", "comment": "\xa9cmt"}
    for k, v in tags.items():
        if k == "tracknumber":
            f.tags["trkn"] = [(int(v), 0)]
        elif k in keys:
            f.tags[keys[k]] = [v]
    if pic:
        f.tags["covr"] = [MP4Cover(pic, imageformat=MP4Cover.FORMAT_JPEG)]
    f.save()
elif ext == "mp3":
    try:
        f = ID3(path)
        f.delete(path)
    except ID3NoHeaderError:
        pass
    f = ID3()
    frames = {"title": TIT2, "artist": TPE1, "albumartist": TPE2, "album": TALB, "date": TDRC, "genre": TCON, "tracknumber": TRCK}
    for k, v in tags.items():
        if k == "comment":
            f.add(COMM(encoding=3, lang="eng", desc="", text=[v]))
        elif k in frames:
            f.add(frames[k](encoding=3, text=[v]))
    if pic:
        f.add(APIC(encoding=3, mime="image/jpeg", type=3, desc="Cover", data=pic))
    f.save(path, v2_version=3)
else:
    sys.exit("no tag writer for ." + ext)
