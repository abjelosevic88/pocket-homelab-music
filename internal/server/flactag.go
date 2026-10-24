package server

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A small FLAC metadata reader/writer: enough to read the length from
// STREAMINFO and to add one Vorbis comment (UNSYNCEDLYRICS) without touching
// the audio. The audio frames are copied byte for byte into a temp file next
// to the original, which then replaces it, so a failure halfway leaves the
// original as it was.

type flacBlock struct {
	typ  byte
	data []byte
}

func readFlacBlocks(r io.Reader) ([]flacBlock, error) {
	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != "fLaC" {
		return nil, errors.New("not a FLAC file")
	}
	var blocks []flacBlock
	for {
		h := make([]byte, 4)
		if _, err := io.ReadFull(r, h); err != nil {
			return nil, err
		}
		last := h[0]&0x80 != 0
		n := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
		if n > 16<<20 {
			return nil, errors.New("FLAC metadata block too large")
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		blocks = append(blocks, flacBlock{typ: h[0] & 0x7f, data: data})
		if last {
			return blocks, nil
		}
		if len(blocks) > 512 {
			return nil, errors.New("too many FLAC metadata blocks")
		}
	}
}

// flacDuration is the song's length in whole seconds from STREAMINFO (0 if unknown).
func flacDuration(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	head := make([]byte, 8+34)
	if _, err := io.ReadFull(f, head); err != nil || string(head[:4]) != "fLaC" || head[4]&0x7f != 0 {
		return 0
	}
	b := head[8:]
	rate := uint64(b[10])<<12 | uint64(b[11])<<4 | uint64(b[12])>>4
	total := uint64(b[13]&0x0f)<<32 | uint64(binary.BigEndian.Uint32(b[14:18]))
	if rate == 0 || total == 0 {
		return 0
	}
	return int((total + rate/2) / rate)
}

type vorbisComment struct {
	vendor   string
	comments []string
}

func parseVorbisComment(d []byte) (vorbisComment, error) {
	var vc vorbisComment
	rd := bytes.NewReader(d)
	str := func() (string, error) {
		var n uint32
		if err := binary.Read(rd, binary.LittleEndian, &n); err != nil {
			return "", err
		}
		if int64(n) > int64(rd.Len()) {
			return "", errors.New("bad Vorbis comment")
		}
		b := make([]byte, n)
		_, err := io.ReadFull(rd, b)
		return string(b), err
	}
	var err error
	if vc.vendor, err = str(); err != nil {
		return vc, err
	}
	var count uint32
	if err := binary.Read(rd, binary.LittleEndian, &count); err != nil {
		return vc, err
	}
	for i := uint32(0); i < count; i++ {
		c, err := str()
		if err != nil {
			return vc, err
		}
		vc.comments = append(vc.comments, c)
	}
	return vc, nil
}

func (vc vorbisComment) bytes() []byte {
	var b bytes.Buffer
	put := func(s string) {
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(s)))
		b.WriteString(s)
	}
	put(vc.vendor)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(vc.comments)))
	for _, c := range vc.comments {
		put(c)
	}
	return b.Bytes()
}

func (vc vorbisComment) has(key string) bool {
	for _, c := range vc.comments {
		if k, v, ok := strings.Cut(c, "="); ok && strings.EqualFold(k, key) && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// flacHasTag says whether the FLAC file has a non-empty comment key.
func flacHasTag(path, key string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	blocks, err := readFlacBlocks(bufio.NewReader(f))
	if err != nil {
		return false
	}
	for _, b := range blocks {
		if b.typ == 4 {
			if vc, err := parseVorbisComment(b.data); err == nil && vc.has(key) {
				return true
			}
		}
	}
	return false
}

// flacAddTag adds key=value unless the key is already there (then it is a
// no-op and returns false).
func flacAddTag(path, key, value string) (bool, error) {
	src, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return false, err
	}
	br := bufio.NewReader(src)
	blocks, err := readFlacBlocks(br)
	if err != nil {
		return false, err
	}
	found := false
	for i, b := range blocks {
		if b.typ != 4 {
			continue
		}
		vc, err := parseVorbisComment(b.data)
		if err != nil {
			return false, err
		}
		if vc.has(key) {
			return false, nil
		}
		vc.comments = append(vc.comments, key+"="+value)
		blocks[i].data = vc.bytes()
		found = true
		break
	}
	if !found {
		vc := vorbisComment{vendor: "Pocket Music", comments: []string{key + "=" + value}}
		// after STREAMINFO
		blocks = append(blocks[:1], append([]flacBlock{{typ: 4, data: vc.bytes()}}, blocks[1:]...)...)
	}
	for _, b := range blocks {
		if len(b.data) >= 1<<24 {
			return false, errors.New("lyrics too long for a FLAC comment")
		}
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".pocket-music-*.flac.tmp")
	if err != nil {
		return false, err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	w := bufio.NewWriterSize(tmp, 1<<20)
	w.WriteString("fLaC")
	for i, b := range blocks {
		h := b.typ
		if i == len(blocks)-1 {
			h |= 0x80
		}
		n := len(b.data)
		w.Write([]byte{h, byte(n >> 16), byte(n >> 8), byte(n)})
		w.Write(b.data)
	}
	// the audio, as it is (br has buffered the bytes after the last block)
	copied, err := io.Copy(w, br)
	if err != nil {
		return false, err
	}
	if err := w.Flush(); err != nil {
		return false, err
	}
	if copied == 0 {
		return false, errors.New("FLAC file has no audio after its metadata")
	}
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	// last sanity check before replacing: the new file parses
	if chk, err := os.Open(tmp.Name()); err == nil {
		_, perr := readFlacBlocks(bufio.NewReader(chk))
		chk.Close()
		if perr != nil {
			return false, fmt.Errorf("rewritten file does not parse: %w", perr)
		}
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	ok = true
	return true, nil
}
