package server

// "From a link": paste a YouTube (or Bandcamp, SoundCloud…) link, yt-dlp
// fetches the audio as it is (no re-encoding) and files it the way the rest
// of the library is filed — <Artist>/<YYYY - Album>/NN. Title.ext with a
// square cover.jpg, tags and the picture embedded — then the music server is
// asked to scan. Long ambient videos are the main use: by default they go
// into the Ambient folder, one video = one album by the channel.

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
	"github.com/abjelosevic88/pocket-homelab-music/internal/ytdlp"
)

//go:embed linktag.py
var linkTagScript string

// linkState is the download worker: one download at a time.
type linkState struct {
	wake    chan struct{}
	mu      sync.Mutex
	running int64
	cancel  context.CancelFunc
	lastLiv time.Time
	prog    linkProgress // of the running download
}

// linkProgress is how far the running download is; kept in memory, sent live.
type linkProgress struct {
	Fraction float64 `json:"fraction"` // of the whole download, 0..1; -1 = can't tell (the server scanning)
	Phase    string  `json:"phase"`    // prepare | download | convert | scan
	Speed    float64 `json:"speed"`    // bytes per second while downloading
	ETA      float64 `json:"eta"`      // seconds left of this track
	Track    int     `json:"track"`
	Tracks   int     `json:"tracks"`
}

// linkView is a download as the list shows it.
type linkView struct {
	store.LinkDownload
	Progress *linkProgress `json:"progress,omitempty"`
}

var linkAudioExt = map[string]bool{".opus": true, ".m4a": true, ".mp3": true, ".ogg": true, ".flac": true, ".wav": true, ".aac": true}

// Where a download can go: the library root, or the Ambient folder (a genre
// folder whose children are artists, like the rest of the library has it).
var linkRoots = []struct{ Key, Label, Sub, Genre string }{
	{"ambient", "Ambient", "Ambient", "Ambient"},
	{"music", "Music", "", ""},
}

func (s *Server) linkReady() (bool, string) {
	if ok, why := s.musicDirWritable(); !ok {
		return false, why
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return false, "ffmpeg is not installed next to the app (the Docker image has it)"
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return false, "python3 is not installed next to the app (the Docker image has it)"
	}
	return true, ""
}

func (s *Server) wakeLinks() {
	select {
	case s.lnk.wake <- struct{}{}:
	default:
	}
}

// linkWorker runs queued downloads one after another (started with the app;
// a download cut off by a restart carries on with the tracks still missing).
func (s *Server) linkWorker(ctx context.Context) {
	for {
		if d, err := s.store.NextLinkDownload(ctx); err == nil {
			s.runLink(ctx, d)
			if ctx.Err() != nil {
				return
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.lnk.wake:
		case <-time.After(5 * time.Minute):
		}
	}
}

// updateYTDLP keeps our own yt-dlp at most a day old (sites change often).
func (s *Server) updateYTDLP(ctx context.Context) {
	if s.cfg.MusicDir == "" || s.cfg.YTDLP != "" {
		return
	}
	if _, at, ok := s.store.CacheGet(ctx, "ytdlp:updated"); ok {
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil && time.Since(t) < 24*time.Hour {
			return
		}
	}
	v, err := s.yt.Update(ctx)
	if err != nil {
		s.log.Warn("yt-dlp update failed", "err", err)
	} else {
		s.log.Info("yt-dlp is up to date", "version", v)
	}
	_ = s.store.CachePut(ctx, "ytdlp:updated", v)
}

func (s *Server) liveLink(d *store.LinkDownload, force bool) {
	if !force {
		s.lnk.mu.Lock()
		if time.Since(s.lnk.lastLiv) < time.Second {
			s.lnk.mu.Unlock()
			return
		}
		s.lnk.lastLiv = time.Now()
		s.lnk.mu.Unlock()
	}
	v := map[string]any{"id": d.ID, "state": d.State, "detail": d.Detail}
	if p := s.linkProgressOf(d.ID); p != nil {
		v["progress"] = p
	}
	s.hub.send(d.UserID, "link_downloads", v)
}

func (s *Server) linkProgressOf(id int64) *linkProgress {
	s.lnk.mu.Lock()
	defer s.lnk.mu.Unlock()
	if s.lnk.running != id || s.lnk.prog.Phase == "" {
		return nil
	}
	p := s.lnk.prog
	return &p
}

func (s *Server) setLinkProgress(d *store.LinkDownload, p linkProgress, force bool) {
	s.lnk.mu.Lock()
	s.lnk.prog = p
	s.lnk.mu.Unlock()
	s.liveLink(d, force)
}

func (s *Server) runLink(ctx context.Context, d *store.LinkDownload) {
	jctx, cancel := context.WithCancel(ctx)
	s.lnk.mu.Lock()
	s.lnk.running, s.lnk.cancel = d.ID, cancel
	s.lnk.mu.Unlock()
	defer func() {
		cancel()
		s.lnk.mu.Lock()
		s.lnk.running, s.lnk.cancel, s.lnk.prog = 0, nil, linkProgress{}
		s.lnk.mu.Unlock()
	}()
	save := func(force bool) {
		if err := s.store.SaveLinkDownload(context.WithoutCancel(ctx), d); err != nil {
			s.log.Warn("link download save", "id", d.ID, "err", err)
		}
		s.liveLink(d, force)
	}
	fail := func(msg string) {
		d.State, d.Error, d.Detail = "failed", msg, ""
		save(true)
		s.log.Warn("link download failed", "id", d.ID, "url", d.URL, "err", msg)
	}
	if ok, why := s.linkReady(); !ok {
		fail(why)
		return
	}
	dir, ok := s.inMusicDir(d.Folder)
	if !ok || d.Folder == "" {
		fail("the album folder is outside the music folder")
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("could not make the album folder: " + err.Error())
		return
	}
	temp := filepath.Join(s.cfg.DataDir, "tmp", "link-"+strconv.FormatInt(d.ID, 10))
	defer os.RemoveAll(temp)

	if d.State != "scanning" {
		cover := findImage(dir, "cover")
		got, skipped := 0, 0
		var lastErr string
		for i := range d.Tracks {
			t := &d.Tracks[i]
			if t.File != "" {
				if p, ok := s.inMusicDir(t.File); ok && fileExists(p) {
					got++
					continue
				}
				t.File = ""
			}
			d.State, d.Error = "downloading", ""
			d.Detail = ""
			if len(d.Tracks) > 1 {
				d.Detail = fmt.Sprintf("track %d of %d", i+1, len(d.Tracks))
			}
			n := float64(len(d.Tracks))
			prog := linkProgress{Fraction: float64(i) / n, Phase: "prepare", Track: i + 1, Tracks: len(d.Tracks)}
			s.lnk.mu.Lock()
			s.lnk.prog = prog
			s.lnk.mu.Unlock()
			save(true)
			name := fmt.Sprintf("%02d. %s", t.Number, sanitizeName(t.Title))
			res, err := s.yt.Download(jctx, ytdlp.Job{URL: t.URL, Dir: dir, TempDir: temp, Name: name}, func(p ytdlp.Progress) {
				if p.Status == "finished" {
					// downloaded; yt-dlp now turns it into an audio file (minutes for a long video)
					prog.Phase, prog.Speed, prog.ETA, prog.Fraction = "convert", 0, 0, (float64(i)+0.97)/n
					s.setLinkProgress(d, prog, true)
					return
				}
				total := float64(p.Total)
				if total <= 0 {
					total = p.Estimate
				}
				if total > 0 {
					f := min(float64(p.Downloaded)/total, 1)
					first := prog.Phase == "prepare"
					prog.Phase, prog.Fraction, prog.Speed, prog.ETA = "download", (float64(i)+0.95*f)/n, p.Speed, p.ETA
					s.setLinkProgress(d, prog, first)
				}
			})
			if err != nil {
				if jctx.Err() != nil {
					return // stopped, or the app is shutting down (picked up again on start)
				}
				lastErr = err.Error()
				skipped++
				s.log.Warn("link download track", "id", d.ID, "url", t.URL, "err", err)
				continue
			}
			prog.Phase = "convert"
			s.setLinkProgress(d, prog, true)
			pic := ""
			if res.Thumb != "" {
				sq := strings.TrimSuffix(res.Thumb, ".jpg") + ".square.jpg"
				if squareCover(jctx, res.Thumb, sq) == nil {
					pic = sq
				}
			}
			if cover == "" && pic != "" {
				if copyFile(pic, filepath.Join(dir, "cover.jpg")) == nil {
					cover = filepath.Join(dir, "cover.jpg")
				}
			}
			embed := pic
			if embed == "" {
				embed = cover
			}
			if err := tagAudio(jctx, res.File, linkTags(d, t), embed); err != nil {
				s.log.Warn("link download tags", "file", res.File, "err", err)
			}
			for _, f := range []string{res.Thumb, pic} {
				if f != "" {
					_ = os.Remove(f)
				}
			}
			t.File = s.relToMusicDir(res.File)
			got++
			save(true)
		}
		if got == 0 {
			if lastErr == "" {
				lastErr = "nothing could be downloaded"
			}
			fail(lastErr)
			removeIfEmpty(dir)
			return
		}
		s.artistPicture(jctx, d, filepath.Dir(dir))
		if skipped > 0 {
			d.Error = fmt.Sprintf("%d of %d tracks could not be downloaded (%s)", skipped, len(d.Tracks), lastErr)
		}
		d.State, d.Detail = "scanning", "your music server is reading the new files"
		save(true)
	}
	s.setLinkProgress(d, linkProgress{Fraction: -1, Phase: "scan"}, true)

	// let the music server read the folder now, then bring the index up to date
	u, err := s.store.GetUser(ctx, d.UserID)
	if err == nil {
		if mc, err := s.musicFor(jctx, u); err == nil {
			if err := mc.c.StartScan(jctx); err != nil {
				s.log.Warn("link download: scan", "err", err)
			}
			deadline := time.Now().Add(15 * time.Minute)
			for time.Now().Before(deadline) {
				sleepCtx(jctx, 5*time.Second)
				if jctx.Err() != nil {
					return
				}
				if st, err := mc.c.ScanStatus(jctx); err != nil || !st.Scanning {
					break
				}
			}
			if err := s.syncUser(jctx, d.UserID, false); err != nil {
				s.log.Warn("link download: library sync", "err", err)
			}
			_ = s.store.DB.QueryRowContext(jctx, `SELECT id FROM albums WHERE user_id = ? AND lower(name) = lower(?) ORDER BY lower(artist) = lower(?) DESC, added_at DESC LIMIT 1`,
				d.UserID, d.Album, d.Artist).Scan(&d.AlbumID)
			if d.Playlist != "" {
				s.linkToPlaylist(jctx, d, mc)
			}
		}
	}
	got := 0
	for _, t := range d.Tracks {
		if t.File != "" {
			got++
		}
	}
	d.State, d.Detail = "done", fmt.Sprintf("%d track%s", got, plural(got))
	if d.Playlist != "" && d.Error == "" {
		d.Detail += " · added to " + firstNonEmpty(d.PlaylistNm, "the playlist")
	}
	save(true)
	st, _ := s.store.GetSettings(ctx, d.UserID)
	if u != nil {
		s.publish(ctx, u, "music.album_arrived", d.Artist+" — "+d.Album, map[string]any{"artist": d.Artist, "album": d.Album, "year": d.Year})
	}
	if st.NotifyArrived {
		_ = s.notifyUser(ctx, st, "New in your library", fmt.Sprintf("%s — %s is ready to play.", d.Artist, d.Album), "cd")
	}
	go s.syncAll(context.WithoutCancel(ctx)) // everyone else's index too
}

// linkToPlaylist adds the downloaded tracks (as the library index now knows
// them) to the chosen playlist, in track order.
func (s *Server) linkToPlaylist(ctx context.Context, d *store.LinkDownload, mc *musicConn) {
	var ids []string
	for _, t := range d.Tracks {
		if t.File == "" {
			continue
		}
		var id string
		err := s.store.DB.QueryRowContext(ctx, `SELECT id FROM songs INDEXED BY songs_match WHERE user_id = ? AND lower(artist) = lower(?) AND lower(album) = lower(?) AND lower(title) = lower(?)
			ORDER BY track = ? DESC, added_at DESC LIMIT 1`, d.UserID, d.Artist, d.Album, t.Title, t.Number).Scan(&id)
		if err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		d.Error = "not added to the playlist: your music server has not listed the new tracks yet"
		return
	}
	if err := mc.c.UpdatePlaylist(ctx, d.Playlist, subsonic.PlaylistUpdate{Add: ids}); err != nil {
		d.Error = "not added to the playlist: " + err.Error()
		return
	}
	s.livePlaylists(d.UserID, "updated", []string{d.Playlist}, 0)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func linkTags(d *store.LinkDownload, t *store.LinkTrack) map[string]string {
	m := map[string]string{
		"title": t.Title, "artist": d.Artist, "albumartist": d.Artist, "album": d.Album,
		"tracknumber": strconv.Itoa(t.Number), "genre": d.Genre, "comment": t.URL,
	}
	if d.Year > 0 {
		m["date"] = strconv.Itoa(d.Year)
	}
	return m
}

// tagAudio writes the tags (replacing whatever the site put in) and embeds
// the picture; mutagen does it, the same library yt-dlp uses for pictures.
func tagAudio(ctx context.Context, file string, tags map[string]string, picture string) error {
	arg, _ := json.Marshal(map[string]any{"file": file, "tags": tags, "picture": picture})
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "python3", "-I", "-c", linkTagScript, string(arg)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// squareCover crops a video picture (16:9) to the middle square, at most 1200 px.
func squareCover(ctx context.Context, in, out string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	b, err := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-i", in,
		"-vf", `crop=w='min(iw\,ih)':h='min(iw\,ih)',scale=w='min(1200\,iw)':h=-2`, "-frames:v", "1", "-q:v", "2", out).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// artistPicture gives a new artist folder the channel's picture — only when
// the artist IS the channel (a name typed in by hand is someone else).
func (s *Server) artistPicture(ctx context.Context, d *store.LinkDownload, artistDir string) {
	if d.ChannelURL == "" || !strings.EqualFold(fold(d.Artist), fold(d.Channel)) || findImage(artistDir, "artist") != "" {
		return
	}
	pic := s.yt.ChannelPicture(ctx, d.ChannelURL, ytdlp.Options{})
	if pic == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pic, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	resp, err := s.http.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	tmp := filepath.Join(s.cfg.DataDir, "tmp", fmt.Sprintf("avatar-%d", d.ID))
	_ = os.MkdirAll(filepath.Dir(tmp), 0o755)
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, 20<<20))
	f.Close()
	defer os.Remove(tmp)
	if err != nil {
		return
	}
	b, err := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-i", tmp, "-frames:v", "1", "-q:v", "2", filepath.Join(artistDir, "artist.jpg")).CombinedOutput()
	if err != nil {
		s.log.Warn("link download: artist picture", "err", err, "out", strings.TrimSpace(string(b)))
	}
}

// ---------- names and folders ----------

// sanitizeName makes a tag value safe as a file or folder name without
// mangling it (the library's own rule: slashes become dashes).
func sanitizeName(s string) string {
	s = strings.NewReplacer("/", "-", "\\", "-").Replace(s)
	s = strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimRight(s, ". ")
	if r := []rune(s); len(r) > 150 {
		s = strings.TrimRight(string(r[:150]), ". ")
	}
	return s
}

func fold(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func findImage(dir, stem string) string {
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if p := filepath.Join(dir, stem+ext); fileExists(p) {
			return p
		}
	}
	return ""
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o644)
}

func removeIfEmpty(dir string) {
	if es, err := os.ReadDir(dir); err == nil && len(es) == 0 {
		_ = os.Remove(dir)
	}
}

func subdirs(dir string) []string {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Slice(out, func(i, j int) bool { return fold(out[i]) < fold(out[j]) })
	return out
}

var yearPrefix = regexp.MustCompile(`^(\d{4}) - (.+)$`)
var leadingNum = regexp.MustCompile(`^(\d+)`)

// linkPlace is where a download lands.
type linkPlace struct {
	Root      string   `json:"root"`
	Folder    string   `json:"folder"` // relative to MUSIC_DIR
	ArtistNew bool     `json:"artist_new"`
	AlbumNew  bool     `json:"album_new"`
	NextTrack int      `json:"next_track"`
	Artists   []string `json:"artists"` // artist folders already there (for the picker)
	Albums    []string `json:"albums"`  // the artist's album folders
}

func (s *Server) rootDir(key string) (string, string, bool) {
	for _, r := range linkRoots {
		if r.Key == key {
			p, ok := s.inMusicDir(r.Sub)
			return p, r.Sub, ok
		}
	}
	return "", "", false
}

// place finds the artist and album folder (reusing existing ones whatever
// their capitalisation, and "YYYY - Album" folders by album name).
func (s *Server) place(root, artist, album string, year int) (*linkPlace, error) {
	rdir, rsub, ok := s.rootDir(root)
	if !ok {
		return nil, errors.New("unknown folder")
	}
	pl := &linkPlace{Root: root, NextTrack: 1, ArtistNew: true, AlbumNew: true, Artists: subdirs(rdir), Albums: []string{}}
	if pl.Artists == nil {
		pl.Artists = []string{}
	}
	artist, album = sanitizeName(artist), sanitizeName(album)
	if artist == "" || album == "" {
		return pl, nil
	}
	artistDir := artist
	for _, a := range pl.Artists {
		if fold(a) == fold(artist) {
			artistDir, pl.ArtistNew = a, false
			break
		}
	}
	albumDir := album
	if year > 0 {
		albumDir = fmt.Sprintf("%d - %s", year, album)
	}
	if !pl.ArtistNew {
		for _, a := range subdirs(filepath.Join(rdir, artistDir)) {
			pl.Albums = append(pl.Albums, a)
			name := a
			if m := yearPrefix.FindStringSubmatch(a); m != nil {
				name = m[2]
			}
			if pl.AlbumNew && (fold(name) == fold(album) || fold(a) == fold(album)) {
				albumDir, pl.AlbumNew = a, false
			}
		}
	}
	if !pl.AlbumNew {
		pl.NextTrack = nextTrack(filepath.Join(rdir, artistDir, albumDir))
	}
	pl.Folder = filepath.ToSlash(filepath.Join(rsub, artistDir, albumDir))
	return pl, nil
}

func nextTrack(dir string) int {
	es, err := os.ReadDir(dir)
	if err != nil {
		return 1
	}
	n := 0
	for _, e := range es {
		if !linkAudioExt[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		if m := leadingNum.FindString(e.Name()); m != "" {
			if v, _ := strconv.Atoi(m); v > n {
				n = v
			}
		} else {
			n++
		}
	}
	return n + 1
}

// ---------- suggestions ----------

var (
	titleCut   = regexp.MustCompile(`\s*(?:\||｜|//|⧸⧸|•|·)\s*`)
	titleFluff = regexp.MustCompile(`(?i)\s*[\(\[](?:official\s*(?:music\s*)?(?:video|audio|visuali[sz]er|lyric video)|lyrics?|hd|hq|4k|audio|visuali[sz]er|full album|\d+(?:\.\d+)?\s*(?:hours?|hrs?|h|minutes?|mins?)[^\)\]]*)[\)\]]`)
	descriptor = regexp.MustCompile(`(?i)\b(ambien|inspired|music|playlist|mix|hours?|sounds?|asmr|relax|sleep|study|soundscape|atmospher|dark ?ambient|lofi|lo-fi|ost|soundtrack)\b`)
	topicTail  = regexp.MustCompile(`(?i)\s*-\s*topic$`)
	vevoTail   = regexp.MustCompile(`VEVO$`)
	albumHead  = regexp.MustCompile(`(?i)^album\s*[-–:]\s*`)
)

func siteName(key string) string {
	switch k := strings.TrimSuffix(key, "Tab"); k {
	case "Youtube", "YoutubeMusicSearchURL":
		return "YouTube"
	case "Soundcloud":
		return "SoundCloud"
	default:
		return k
	}
}

func cleanChannel(c string) string {
	c = topicTail.ReplaceAllString(strings.TrimSpace(c), "")
	if c2 := vevoTail.ReplaceAllString(c, ""); c2 != "" {
		c = c2
	}
	return c
}

// cleanTitle turns a video title into a track title: the part before the
// first "|" / "//" separator, without "(Official Video)" or "(3 Hours)", and
// "Artist - Title" split when the artist is the channel. A trailing
// description ("… - Silent Hill inspired ambience music") is dropped too.
func cleanTitle(title, artist string) string {
	t := strings.TrimSpace(title)
	if parts := titleCut.Split(t, 2); len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
		t = parts[0]
	}
	t = titleFluff.ReplaceAllString(t, "")
	if i := strings.Index(t, " - "); i > 0 {
		left, right := strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+3:])
		switch {
		case artist != "" && fold(left) == fold(artist):
			t = right
		case artist != "" && fold(right) == fold(artist):
			t = left
		case descriptor.MatchString(right) && !descriptor.MatchString(left):
			t = left
		}
	}
	t = strings.TrimSpace(strings.Trim(strings.TrimSpace(t), "-–—:"))
	if t == "" {
		return strings.TrimSpace(title)
	}
	return t
}

type linkSuggestTrack struct {
	URL      string  `json:"url"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
}

type linkSuggest struct {
	Kind       string             `json:"kind"` // track | playlist
	URL        string             `json:"url"`
	Source     string             `json:"source"` // the video's or playlist's own title
	Channel    string             `json:"channel"`
	ChannelURL string             `json:"channel_url"`
	Site       string             `json:"site"`
	Thumbnail  string             `json:"thumbnail"`
	Duration   float64            `json:"duration"`
	Artist     string             `json:"artist"`
	Album      string             `json:"album"`
	Year       int                `json:"year"`
	Genre      string             `json:"genre"`
	Tracks     []linkSuggestTrack `json:"tracks"`
	Before     []string           `json:"before"` // album folders this link was downloaded into already
}

func suggestFrom(info *ytdlp.Info) *linkSuggest {
	sg := &linkSuggest{URL: info.URL, Source: info.Title, Site: siteName(info.ExtractorKey), Thumbnail: info.Picture(),
		Duration: info.Duration, ChannelURL: firstNonEmpty(info.ChannelURL, info.UploaderURL), Tracks: []linkSuggestTrack{}}
	sg.Channel = cleanChannel(firstNonEmpty(info.Channel, info.Uploader))
	year := info.ReleaseYear
	if year == 0 && len(info.UploadDate) >= 4 {
		year, _ = strconv.Atoi(info.UploadDate[:4])
	}
	sg.Year = year
	artist := firstNonEmpty(info.AlbumArtist, info.Artist)
	if artist == "" && len(info.Artists) > 0 {
		artist = info.Artists[0]
	}
	if artist == "" {
		artist = sg.Channel
	}
	sg.Artist = cleanChannel(artist)
	sg.Genre = info.Genre
	if info.IsList() {
		sg.Kind = "playlist"
		// a playlist by one channel is that channel's album
		chans := map[string]int{}
		for _, e := range info.Entries {
			if c := cleanChannel(firstNonEmpty(e.Channel, e.Uploader)); c != "" {
				chans[c]++
			}
		}
		if len(chans) == 1 && (sg.Artist == "" || info.Artist == "") {
			for c := range chans {
				sg.Artist = c
			}
		}
		sg.Album = firstNonEmpty(info.Album, strings.TrimSpace(albumHead.ReplaceAllString(cleanTitle(info.Title, sg.Artist), "")))
		for _, e := range info.Entries {
			if e.IEKey == "YoutubeTab" {
				continue // a channel's playlists tab: those are lists, not tracks
			}
			u := e.URL
			if !strings.Contains(u, "://") && e.ID != "" && strings.HasPrefix(strings.ToLower(info.ExtractorKey), "youtube") {
				u = "https://www.youtube.com/watch?v=" + e.ID
			}
			sg.Tracks = append(sg.Tracks, linkSuggestTrack{URL: u, Title: cleanTitle(e.Title, sg.Artist), Duration: e.Duration})
			sg.Duration += e.Duration
		}
		return sg
	}
	sg.Kind = "track"
	title := info.Track
	if title == "" {
		title = cleanTitle(info.Title, sg.Artist)
	}
	sg.Album = firstNonEmpty(info.Album, title) // a single video is its own album
	sg.Tracks = append(sg.Tracks, linkSuggestTrack{URL: info.URL, Title: title, Duration: info.Duration})
	return sg
}

// ---------- HTTP ----------

func (s *Server) handleLinkDownloads(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	u := userFrom(r.Context())
	items, err := s.store.LinkDownloads(r.Context(), u.ID)
	if err != nil {
		s.fail(w, err, "link downloads")
		return
	}
	views := make([]linkView, 0, len(items))
	for _, d := range items {
		views = append(views, linkView{LinkDownload: d, Progress: s.linkProgressOf(d.ID)})
	}
	ok, why := s.linkReady()
	roots := []map[string]any{}
	for _, rt := range linkRoots {
		p, _, _ := s.rootDir(rt.Key)
		fi, err := os.Stat(p)
		roots = append(roots, map[string]any{"key": rt.Key, "label": rt.Label, "genre": rt.Genre, "exists": err == nil && fi.IsDir()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": ok, "detail": why, "roots": roots, "items": views})
}

func (s *Server) handleLinkInspect(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	link, err := checkLink(in.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ok, why := s.linkReady(); !ok {
		writeErr(w, http.StatusConflict, why)
		return
	}
	info, err := s.yt.Inspect(r.Context(), link, ytdlp.Options{Limit: 500})
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	sg := suggestFrom(info)
	if len(sg.Tracks) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "there are no videos behind this link (for a channel, open one of its playlists or videos)")
		return
	}
	sg.Before = []string{}
	rows, err := s.store.DB.QueryContext(r.Context(), `SELECT DISTINCT folder FROM link_downloads WHERE url = ? AND state = 'done'`, sg.URL)
	if err == nil {
		for rows.Next() {
			var f string
			if rows.Scan(&f) == nil {
				sg.Before = append(sg.Before, f)
			}
		}
		rows.Close()
	}
	writeJSON(w, http.StatusOK, sg)
}

func checkLink(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("paste a link that starts with https://")
	}
	return raw, nil
}

func (s *Server) handleLinkPlace(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	q := r.URL.Query()
	year, _ := strconv.Atoi(q.Get("year"))
	pl, err := s.place(q.Get("root"), q.Get("artist"), q.Get("album"), year)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pl)
}

func (s *Server) handleLinkCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		URL        string             `json:"url"`
		Root       string             `json:"root"`
		Artist     string             `json:"artist"`
		Album      string             `json:"album"`
		Year       int                `json:"year"`
		Genre      string             `json:"genre"`
		Channel    string             `json:"channel"`
		ChannelURL string             `json:"channel_url"`
		Thumbnail  string             `json:"thumbnail"`
		Tracks     []linkSuggestTrack `json:"tracks"`
		Playlist   string             `json:"playlist_id"`
		PlaylistNm string             `json:"playlist_name"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	link, err := checkLink(in.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Artist, in.Album, in.Genre = strings.TrimSpace(in.Artist), strings.TrimSpace(in.Album), strings.TrimSpace(in.Genre)
	if sanitizeName(in.Artist) == "" || sanitizeName(in.Album) == "" {
		writeErr(w, http.StatusBadRequest, "an artist and an album are needed")
		return
	}
	if len(in.Tracks) == 0 || len(in.Tracks) > 500 {
		writeErr(w, http.StatusBadRequest, "between 1 and 500 tracks")
		return
	}
	if in.Year < 0 || in.Year > 9999 {
		in.Year = 0
	}
	if ok, why := s.linkReady(); !ok {
		writeErr(w, http.StatusConflict, why)
		return
	}
	pl, err := s.place(in.Root, in.Artist, in.Album, in.Year)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := userFrom(r.Context())
	d := &store.LinkDownload{UserID: u.ID, URL: link, Artist: in.Artist, Album: in.Album, Year: in.Year, Genre: in.Genre,
		Folder: pl.Folder, CoverURL: in.Thumbnail, Channel: cleanChannel(in.Channel), ChannelURL: in.ChannelURL,
		Playlist: strings.TrimSpace(in.Playlist), PlaylistNm: strings.TrimSpace(in.PlaylistNm)}
	for i, t := range in.Tracks {
		tu, err := checkLink(t.URL)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("track %d: %v", i+1, err))
			return
		}
		title := strings.TrimSpace(t.Title)
		if sanitizeName(title) == "" {
			title = fmt.Sprintf("Track %d", i+1)
		}
		d.Tracks = append(d.Tracks, store.LinkTrack{URL: tu, Title: title, Number: pl.NextTrack + i})
	}
	if err := s.store.AddLinkDownload(r.Context(), d); err != nil {
		s.fail(w, err, "link download")
		return
	}
	s.liveLink(d, true)
	s.wakeLinks()
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleLinkDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	u := userFrom(r.Context())
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	d, err := s.store.GetLinkDownload(r.Context(), u.ID, id)
	if err != nil {
		s.fail(w, err, "link download")
		return
	}
	s.lnk.mu.Lock()
	if s.lnk.running == id && s.lnk.cancel != nil {
		s.lnk.cancel() // stop it; the tracks already in the folder stay
	}
	s.lnk.mu.Unlock()
	if err := s.store.DeleteLinkDownload(r.Context(), u.ID, id); err != nil {
		s.fail(w, err, "link download")
		return
	}
	d.State = "deleted"
	s.liveLink(d, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLinkClear(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	u := userFrom(r.Context())
	if err := s.store.ClearLinkDownloads(r.Context(), u.ID); err != nil {
		s.fail(w, err, "link downloads")
		return
	}
	s.hub.send(u.ID, "link_downloads", map[string]any{"id": 0, "state": "cleared"})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
