package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // decoded to check a picture's size
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// Large artist pictures for the apps' artist banners. The music server's own are often small
// (300–700 px wide, stretched over a 3000 px banner); these come from fanart.tv — wide 1920×1080
// band photos, with FANART_API_KEY and the artist's MusicBrainz ID — or else Deezer (square, up
// to 1200 px, no key). Each is fetched once, the first time an artist is opened or when a sync
// finds a new artist, and kept under DATA_DIR/artist-pictures for everybody on the box.

var (
	fanartAPI = "https://webservice.fanart.tv/v3/music"
	deezerAPI = "https://api.deezer.com"
	mbidRe    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

const (
	// Ask again this long after finding nothing (Deezer and fanart.tv keep adding photos).
	artistPicRetry = 14 * 24 * time.Hour
	// Smaller than this is no better than what the music server has.
	artistPicMinWidth = 800
	// Between lookups when going through a library, to be polite to the free APIs.
	artistPicPace = 1200 * time.Millisecond
)

func (s *Server) artistPicsOn() bool { return s.cfg.ArtistPictures }

func artistPicKey(a *store.Artist) string {
	if mbidRe.MatchString(a.MBID) {
		return "mb-" + a.MBID
	}
	h := sha256.Sum256([]byte(fold(a.Name)))
	return "nm-" + hex.EncodeToString(h[:12])
}

func (s *Server) artistPicDir() string { return filepath.Join(s.cfg.DataDir, "artist-pictures") }

// cachedArtistPicture says whether a's picture is on disk (path) or was looked for lately and not found (missing).
func (s *Server) cachedArtistPicture(a *store.Artist) (path string, missing bool) {
	key := artistPicKey(a)
	p := filepath.Join(s.artistPicDir(), key)
	if _, err := os.Stat(p); err == nil {
		return p, false
	}
	if st, err := os.Stat(p + ".none"); err == nil && time.Since(st.ModTime()) < artistPicRetry {
		return "", true
	}
	return "", false
}

// largeArtistPicture returns the file of a large picture of a, looking for one the first time.
func (s *Server) largeArtistPicture(ctx context.Context, a *store.Artist) (string, bool) {
	if p, missing := s.cachedArtistPicture(a); p != "" || missing {
		return p, p != ""
	}
	if !s.artistPicsOn() {
		return "", false
	}
	key := artistPicKey(a)

	// One lookup per artist at a time: a banner opened during a sync's run waits for it.
	s.picMu.Lock()
	if ch, busy := s.picBusy[key]; busy {
		s.picMu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return "", false
		}
		p, _ := s.cachedArtistPicture(a)
		return p, p != ""
	}
	ch := make(chan struct{})
	s.picBusy[key] = ch
	s.picMu.Unlock()
	defer func() {
		s.picMu.Lock()
		delete(s.picBusy, key)
		s.picMu.Unlock()
		close(ch)
	}()

	p := filepath.Join(s.artistPicDir(), key)
	if err := os.MkdirAll(s.artistPicDir(), 0o755); err != nil {
		s.log.Warn("artist pictures: folder", "err", err)
		return "", false
	}
	sources, sure := s.artistPicSources(ctx, a)
	for _, src := range sources {
		if err := s.saveArtistPicture(ctx, src, p); err == nil {
			_ = os.Remove(p + ".none")
			return p, true
		} else if ctx.Err() != nil {
			return "", false
		}
	}
	// Remember a miss only when every source answered: a rate limit or a timeout is asked again next time.
	if ctx.Err() == nil && sure {
		_ = os.WriteFile(p+".none", nil, 0o644)
	}
	return "", false
}

// artistPicSources lists picture URLs, best first: fanart.tv backgrounds, its thumbs, then Deezer.
// sure is false when a source couldn't be asked (an error, not an answer).
func (s *Server) artistPicSources(ctx context.Context, a *store.Artist) (out []string, sure bool) {
	sure = true
	if s.cfg.FanartAPIKey != "" && mbidRe.MatchString(a.MBID) {
		pics, ok := s.fanartPictures(ctx, a.MBID)
		out, sure = append(out, pics...), ok
	}
	u, ok := s.deezerPicture(ctx, a.Name)
	if u != "" {
		out = append(out, u)
	}
	return out, sure && ok
}

func (s *Server) fanartPictures(ctx context.Context, mbid string) ([]string, bool) {
	var res struct {
		Backgrounds []fanartImage `json:"artistbackground"`
		Thumbs      []fanartImage `json:"artistthumb"`
	}
	if err := s.lookupJSON(ctx, fanartAPI+"/"+mbid+"?api_key="+url.QueryEscape(s.cfg.FanartAPIKey), &res); err != nil {
		// fanart.tv answers 404 for an artist it has nothing of: that's an answer
		return nil, strings.HasPrefix(err.Error(), "404")
	}
	var out []string
	for _, list := range [][]fanartImage{res.Backgrounds, res.Thumbs} {
		sort.SliceStable(list, func(i, j int) bool { return list[i].likes() > list[j].likes() })
		for i, im := range list {
			if i < 2 && strings.HasPrefix(im.URL, "https://") {
				out = append(out, im.URL)
			}
		}
	}
	return out, true
}

type fanartImage struct {
	URL   string `json:"url"`
	Likes string `json:"likes"` // a number, as a string
}

func (f fanartImage) likes() int {
	n := 0
	fmt.Sscan(f.Likes, &n)
	return n
}

// deezerPicture finds the artist by name (the same name, most fans first) and asks for its largest picture.
func (s *Server) deezerPicture(ctx context.Context, name string) (string, bool) {
	var res struct {
		Data []struct {
			Name      string `json:"name"`
			PictureXL string `json:"picture_xl"`
			Fans      int    `json:"nb_fan"`
		} `json:"data"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	q := url.Values{"q": {`artist:"` + name + `"`}, "limit": {"10"}}
	// Deezer reports a rate limit (or any trouble) as 200 with {"error":{…}}
	if err := s.lookupJSON(ctx, deezerAPI+"/search/artist?"+q.Encode(), &res); err != nil || res.Error != nil {
		return "", false
	}
	best, fans := "", -1
	for _, d := range res.Data {
		// no photo: the picture has an empty hash (…/images/artist//1000x1000…)
		if !sameArtist(d.Name, name) || d.PictureXL == "" || strings.Contains(d.PictureXL, "/artist//") || d.Fans <= fans {
			continue
		}
		best, fans = d.PictureXL, d.Fans
	}
	// The CDN makes any size up to the original (1200 px for most): ask for more than 1000.
	return strings.Replace(best, "/1000x1000-", "/1500x1500-", 1), true
}

func sameArtist(a, b string) bool {
	norm := func(s string) string { return strings.TrimPrefix(fold(strings.ReplaceAll(s, "&", "and")), "the ") }
	return norm(a) == norm(b)
}

// lookupJSON is getJSON with a time limit: a slow API mustn't hold up a banner for long.
func (s *Server) lookupJSON(ctx context.Context, u string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return s.getJSON(ctx, u, nil, v)
}

// saveArtistPicture downloads a picture to path if it's an image at least artistPicMinWidth wide.
func (s *Server) saveArtistPicture(ctx context.Context, src, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pic-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, io.LimitReader(resp.Body, 20<<20))
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		tmp.Close()
		return err
	}
	cfg, _, err := image.DecodeConfig(tmp)
	tmp.Close()
	if err != nil {
		return err
	}
	if cfg.Width < artistPicMinWidth {
		return errors.New("too small")
	}
	return os.Rename(tmp.Name(), path)
}

// handleArtistPicture serves an artist's large picture, or 404 when there's none (the apps then use /cover/ar-<id>).
func (s *Server) handleArtistPicture(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !coverID.MatchString(id) {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	a, err := s.store.GetArtist(r.Context(), userFrom(r.Context()).ID, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no artist")
		return
	}
	p, ok := s.largeArtistPicture(r.Context(), a)
	if !ok {
		w.Header().Set("Cache-Control", "private, max-age=3600")
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	_, _ = f.Seek(0, io.SeekStart)
	w.Header().Set("Content-Type", http.DetectContentType(head[:n]))
	w.Header().Set("Cache-Control", "private, max-age=604800")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// fetchArtistPictures looks for pictures of the artists that have none yet, one at a time, in the
// background — after a sync, so a new artist has its banner before anybody opens it.
func (s *Server) fetchArtistPictures(artists []store.Artist) {
	if !s.artistPicsOn() {
		return
	}
	var todo []store.Artist
	for i := range artists {
		if p, missing := s.cachedArtistPicture(&artists[i]); p == "" && !missing {
			todo = append(todo, artists[i])
		}
	}
	if len(todo) == 0 {
		return
	}
	s.job("artist-pictures", func(ctx context.Context) {
		found := 0
		for i := range todo {
			if ctx.Err() != nil {
				return
			}
			if _, ok := s.largeArtistPicture(ctx, &todo[i]); ok {
				found++
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(artistPicPace):
			}
		}
		s.log.Info("artist pictures", "looked_for", len(todo), "found", found)
	})
}
