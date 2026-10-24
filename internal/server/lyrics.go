package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- lyrics ----
//
// The music server's own lyrics come first (embedded tags or .lrc files next
// to the song, synced when they have timestamps). When it has none, LRCLIB
// (lrclib.net, free, no key) is asked by artist, title, album and length;
// answers and misses are cached so a song is looked up once.

type lyricLine struct {
	T    int    `json:"t"` // ms, -1 when not synced
	Text string `json:"text"`
}

type lyricsOut struct {
	Source   string      `json:"source"` // server | lrclib | ""
	Synced   bool        `json:"synced"`
	Lines    []lyricLine `json:"lines"`
	Saveable bool        `json:"saveable,omitempty"` // can be written next to the song (admins, music folder mounted)
}

var lrcTime = regexp.MustCompile(`\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?\]`)
var lrcOffset = regexp.MustCompile(`(?i)\[offset:\s*([+-]?\d+)\]`)

// parseLRC reads LRC text: synced lines with [mm:ss.xx] stamps, or plain text.
func parseLRC(text string) ([]lyricLine, bool) {
	offset := 0
	if m := lrcOffset.FindStringSubmatch(text); m != nil {
		offset, _ = strconv.Atoi(m[1])
	}
	var lines []lyricLine
	synced := false
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		stamps := lrcTime.FindAllStringSubmatch(raw, -1)
		body := strings.TrimSpace(lrcTime.ReplaceAllString(raw, ""))
		if len(stamps) == 0 {
			if strings.HasPrefix(body, "[") && strings.HasSuffix(body, "]") {
				continue // [ar:…] [ti:…] tags
			}
			lines = append(lines, lyricLine{T: -1, Text: body})
			continue
		}
		synced = true
		for _, m := range stamps {
			min, _ := strconv.Atoi(m[1])
			sec, _ := strconv.Atoi(m[2])
			frac := 0
			if m[3] != "" {
				f := m[3]
				for len(f) < 3 {
					f += "0"
				}
				frac, _ = strconv.Atoi(f)
			}
			lines = append(lines, lyricLine{T: max(0, (min*60+sec)*1000+frac-offset), Text: body})
		}
	}
	if synced {
		kept := lines[:0]
		for _, l := range lines {
			if l.T >= 0 {
				kept = append(kept, l)
			}
		}
		lines = kept
		sort.SliceStable(lines, func(i, j int) bool { return lines[i].T < lines[j].T })
	}
	// drop leading/trailing blank lines
	for len(lines) > 0 && lines[0].Text == "" && !synced {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1].Text == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, synced
}

func (s *Server) handleLyrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	id := chi.URLParam(r, "id")
	songs, err := s.songsFor(ctx, u.ID, []string{id})
	if err != nil {
		s.fail(w, err, "song")
		return
	}
	if len(songs) == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	so := songs[0]
	best := lyricsOut{Lines: []lyricLine{}}
	// a .lrc beside the file is read as it is now: edits show before the next scan
	if file, ok := s.indexedFileFor(ctx, so); ok {
		if l := existingLrc(file); l != "" {
			if b, err := readCapped(l, 1<<20); err == nil {
				if lines, synced := parseLRC(string(b)); len(lines) > 0 {
					best = lyricsOut{Source: "server", Synced: synced, Lines: lines}
				}
			}
		}
	}
	if mc, err := s.musicFor(ctx, u); err == nil && best.Source == "" {
		if sets, err := mc.c.Lyrics(ctx, id); err == nil {
			for _, set := range sets {
				var lines []lyricLine
				synced := set.Synced
				for _, l := range set.Line {
					t := -1
					if l.Start != nil {
						t = max(0, *l.Start-set.Offset)
					}
					if t < 0 {
						synced = false
					}
					lines = append(lines, lyricLine{T: t, Text: l.Value})
				}
				if len(lines) == 0 {
					continue
				}
				// one plain set may hold LRC text as a single value (older tags)
				if !synced && len(lines) <= 2 && lrcTime.MatchString(lines[0].Text) {
					lines, synced = parseLRC(lines[0].Text)
				}
				if best.Source == "" || (synced && !best.Synced) {
					best = lyricsOut{Source: "server", Synced: synced, Lines: lines}
				}
			}
		}
	}
	st, _ := s.store.GetSettings(ctx, u.ID)
	// "library" keeps the files' own lyrics even when they have no timings
	if st.LyricsOnline && s.cfg.LyricsURL != "" && (best.Source == "" || (!best.Synced && st.LyricsPrefer != "library")) {
		if got, ok := s.lrclib(ctx, so); ok && (got.Synced || best.Source == "") {
			best = got
		}
	}
	// admins may write online lyrics next to the song for every player
	best.Saveable = best.Source == "lrclib" && u.Role == "admin" && s.cfg.MusicDir != ""
	w.Header().Set("Cache-Control", "private, no-cache") // lyrics can be edited any time
	writeJSON(w, http.StatusOK, best)
}

func (s *Server) lrclib(ctx context.Context, so store.Song) (lyricsOut, bool) {
	key := fmt.Sprintf("lyrics:%s|%s|%d", store.MatchKey(so.Artist), store.MatchKey(so.Title), so.Duration/5)
	if raw, at, ok := s.store.CacheGet(ctx, key); ok {
		var out lyricsOut
		if json.Unmarshal([]byte(raw), &out) == nil {
			if out.Source != "" {
				return out, true
			}
			// a miss is retried after a week (lyrics get added all the time)
			if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < 7*24*time.Hour {
				return out, false
			}
		}
	}
	q := url.Values{"artist_name": {so.Artist}, "track_name": {so.Title}}
	if so.Album != "" {
		q.Set("album_name", so.Album)
	}
	if so.Duration > 0 {
		q.Set("duration", strconv.Itoa(so.Duration))
	}
	var hit struct {
		Instrumental bool   `json:"instrumental"`
		Plain        string `json:"plainLyrics"`
		Synced       string `json:"syncedLyrics"`
	}
	found := s.lrclibGet(ctx, "/api/get?"+q.Encode(), &hit)
	if !found {
		// the exact lookup wants the album and length to match; search is looser
		var hits []struct {
			ArtistName string  `json:"artistName"`
			TrackName  string  `json:"trackName"`
			Duration   float64 `json:"duration"`
			Plain      string  `json:"plainLyrics"`
			Synced     string  `json:"syncedLyrics"`
		}
		if s.lrclibGet(ctx, "/api/search?"+url.Values{"artist_name": {so.Artist}, "track_name": {so.Title}}.Encode(), &hits) {
			for _, h := range hits {
				if store.MatchKey(h.TrackName) != store.MatchKey(so.Title) || (so.Duration > 0 && h.Duration > 0 && abs(int(h.Duration)-so.Duration) > 4) {
					continue
				}
				hit.Plain, hit.Synced, found = h.Plain, h.Synced, true
				if h.Synced != "" {
					break
				}
			}
		}
	}
	out := lyricsOut{Lines: []lyricLine{}}
	switch {
	case found && hit.Synced != "":
		out.Lines, out.Synced = parseLRC(hit.Synced)
		out.Source = "lrclib"
	case found && hit.Plain != "":
		out.Lines, _ = parseLRC(hit.Plain)
		out.Source = "lrclib"
	case found && hit.Instrumental:
		out.Source = "lrclib"
		out.Lines = []lyricLine{{T: -1, Text: "♪ Instrumental ♪"}}
	}
	b, _ := json.Marshal(out)
	_ = s.store.CachePut(ctx, key, string(b))
	return out, out.Source != ""
}

func (s *Server) lrclibGet(ctx context.Context, path string, v any) bool {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.LyricsURL+path, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	res, err := s.http.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	return json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(v) == nil
}
