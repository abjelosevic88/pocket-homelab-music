// Package ytdlp runs yt-dlp: it keeps its own copy up to date (sites change
// often), reads what a link holds and downloads the audio of videos with progress.
package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const releaseURL = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp"

// Runner finds and runs yt-dlp.
type Runner struct {
	fixed   string // YTDLP: a yt-dlp chosen by the admin (never updated by us)
	own     string // our own copy: DATA_DIR/bin/yt-dlp
	bundled string // the copy in the image, used to start our own
	ua      string

	mu       sync.Mutex
	version  string
	lastErr  string
	updated  time.Time
	updating bool
}

// New returns a runner. fixed = "" keeps an own copy in dataDir/bin.
func New(fixed, dataDir, userAgent string) *Runner {
	return &Runner{fixed: fixed, own: filepath.Join(dataDir, "bin", "yt-dlp"), bundled: "/usr/local/share/pocket-music/yt-dlp", ua: userAgent}
}

// Path returns the yt-dlp to run, fetching our own copy when there is none.
func (r *Runner) Path(ctx context.Context) (string, error) {
	if r.fixed != "" {
		return r.fixed, nil
	}
	if fi, err := os.Stat(r.own); err == nil && fi.Size() > 0 {
		return r.own, nil
	}
	if err := os.MkdirAll(filepath.Dir(r.own), 0o755); err != nil {
		return "", err
	}
	if b, err := os.ReadFile(r.bundled); err == nil && len(b) > 0 {
		if err := os.WriteFile(r.own, b, 0o755); err != nil {
			return "", err
		}
		return r.own, nil
	}
	if err := r.fetch(ctx); err != nil {
		if p, lerr := exec.LookPath("yt-dlp"); lerr == nil {
			return p, nil
		}
		return "", fmt.Errorf("yt-dlp could not be fetched: %w", err)
	}
	return r.own, nil
}

func (r *Runner) fetch(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	req.Header.Set("User-Agent", r.ua)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	tmp := r.own + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, r.own)
}

// Status is what Settings shows about yt-dlp.
type Status struct {
	Version   string `json:"version"`
	Path      string `json:"path"`
	Own       bool   `json:"own"` // our copy, updated daily
	UpdatedAt string `json:"updated_at"`
	Error     string `json:"error"`
	Updating  bool   `json:"updating"`
}

// Status reports the version (asking yt-dlp once).
func (r *Runner) Status(ctx context.Context) Status {
	r.mu.Lock()
	v, e, up, busy := r.version, r.lastErr, r.updated, r.updating
	r.mu.Unlock()
	p, err := r.Path(ctx)
	if err != nil {
		return Status{Error: err.Error(), Own: r.fixed == ""}
	}
	if v == "" {
		v = r.readVersion(ctx, p)
	}
	st := Status{Version: v, Path: p, Own: r.fixed == "", Error: e, Updating: busy}
	if !up.IsZero() {
		st.UpdatedAt = up.UTC().Format(time.RFC3339)
	}
	return st
}

func (r *Runner) readVersion(ctx context.Context, p string) string {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "--version").Output()
	if err != nil {
		r.mu.Lock()
		r.lastErr = "yt-dlp does not start: " + err.Error()
		r.mu.Unlock()
		return ""
	}
	v := strings.TrimSpace(string(out))
	r.mu.Lock()
	r.version = v
	r.mu.Unlock()
	return v
}

// Update brings our own copy to the newest release (yt-dlp -U).
func (r *Runner) Update(ctx context.Context) (string, error) {
	if r.fixed != "" {
		return "", errors.New("this yt-dlp is managed outside the app (YTDLP)")
	}
	r.mu.Lock()
	if r.updating {
		r.mu.Unlock()
		return "", errors.New("already updating")
	}
	r.updating = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.updating = false
		r.mu.Unlock()
	}()
	p, err := r.Path(ctx)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "-U").CombinedOutput()
	r.mu.Lock()
	r.updated = time.Now()
	r.version = ""
	r.lastErr = ""
	if err != nil {
		r.lastErr = "update failed: " + lastLine(string(out))
	}
	r.mu.Unlock()
	if err != nil {
		// a broken copy: fetch a fresh one
		if ferr := r.fetch(ctx); ferr != nil {
			return "", fmt.Errorf("%s", lastLine(string(out)))
		}
	}
	return r.readVersion(ctx, p), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// ---------- reading a link ----------

// Thumb is one picture yt-dlp lists for a video or channel.
type Thumb struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Entry is one video of a playlist (flat: only what the list page says).
type Entry struct {
	ID       string  `json:"id"`
	URL      string  `json:"url"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
	Uploader string  `json:"uploader"`
	Channel  string  `json:"channel"`
	IEKey    string  `json:"ie_key"`
	Thumbs   []Thumb `json:"thumbnails"` // on YouTube: Youtube = a video, YoutubeTab = a playlist or channel
}

// Info is what a link holds. The music fields (artist, album, track…) are
// filled by sites that know them: YouTube Music, Bandcamp, SoundCloud.
type Info struct {
	Type         string   `json:"_type"` // video | playlist | url
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	URL          string   `json:"webpage_url"`
	Thumbnail    string   `json:"thumbnail"`
	Thumbnails   []Thumb  `json:"thumbnails"`
	Uploader     string   `json:"uploader"`
	Channel      string   `json:"channel"`
	ChannelURL   string   `json:"channel_url"`
	UploaderURL  string   `json:"uploader_url"`
	Duration     float64  `json:"duration"`
	UploadDate   string   `json:"upload_date"` // YYYYMMDD
	ReleaseYear  int      `json:"release_year"`
	Artist       string   `json:"artist"`
	Artists      []string `json:"artists"`
	AlbumArtist  string   `json:"album_artist"`
	Album        string   `json:"album"`
	Track        string   `json:"track"`
	TrackNumber  int      `json:"track_number"`
	Genre        string   `json:"genre"`
	Extractor    string   `json:"extractor"`
	ExtractorKey string   `json:"extractor_key"`
	Entries      []Entry  `json:"entries"`
}

// Picture is the biggest picture of the link (a flat playlist has only a list).
func (i *Info) Picture() string {
	if i.Thumbnail != "" {
		return i.Thumbnail
	}
	best, w := "", -1
	for _, t := range i.Thumbnails {
		if t.Width > w {
			best, w = t.URL, t.Width
		}
	}
	if best == "" && len(i.Entries) > 0 {
		for _, t := range i.Entries[0].Thumbs {
			if t.Width > w {
				best, w = t.URL, t.Width
			}
		}
	}
	return best
}

// IsList reports whether the link is a playlist or channel.
func (i *Info) IsList() bool { return i.Type == "playlist" || len(i.Entries) > 0 }

// Options are the extra switches of one run.
type Options struct {
	Cookies string // a cookies.txt
	Limit   int    // playlist items (0 = all)
}

func (o Options) args() []string {
	var a []string
	if o.Cookies != "" {
		a = append(a, "--cookies", o.Cookies)
	}
	return a
}

// jsArgs: YouTube wants a JavaScript runtime for its signatures; yt-dlp only
// looks for deno unless told, the image ships node.
func jsArgs() []string {
	if _, err := exec.LookPath("deno"); err == nil {
		return nil
	}
	if p, err := exec.LookPath("node"); err == nil {
		return []string{"--js-runtimes", "node:" + p}
	}
	return nil
}

// Inspect reads a link without downloading (playlists flat).
func (r *Runner) Inspect(ctx context.Context, url string, o Options) (*Info, error) {
	args := []string{"-J", "--flat-playlist"}
	if o.Limit > 0 {
		args = append(args, "--playlist-end", fmt.Sprint(o.Limit))
	}
	return r.readJSON(ctx, url, append(args, o.args()...))
}

// ChannelPicture is the channel's own picture (its avatar), for an artist
// folder that has none. "" when the site lists none.
func (r *Runner) ChannelPicture(ctx context.Context, channelURL string, o Options) string {
	if channelURL == "" {
		return ""
	}
	info, err := r.readJSON(ctx, channelURL, append([]string{"-J", "--flat-playlist", "--playlist-items", "0"}, o.args()...))
	if err != nil {
		return ""
	}
	// YouTube lists the avatar uncropped (often small) and at a few sizes
	// from the same picture: take the biggest of those
	avatar := ""
	for _, t := range info.Thumbnails {
		if t.ID == "avatar_uncropped" || (avatar == "" && t.ID == "avatar") {
			avatar = t.URL
		}
	}
	if avatar == "" {
		return ""
	}
	base, _, _ := strings.Cut(avatar, "=")
	best, width := avatar, 0
	for _, t := range info.Thumbnails {
		if b, _, _ := strings.Cut(t.URL, "="); b == base && t.Width > width {
			best, width = t.URL, t.Width
		}
	}
	return best
}

func (r *Runner) readJSON(ctx context.Context, url string, args []string) (*Info, error) {
	p, err := r.Path(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	args = append(append(args, "--no-warnings", "--no-colors", "--socket-timeout", "30"), jsArgs()...)
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, p, append(args, "--", url)...)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("the site took too long to answer")
		}
		return nil, cleanErr(errb.String(), err)
	}
	var info Info
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		return nil, fmt.Errorf("yt-dlp gave an answer that could not be read: %w", err)
	}
	if info.Type == "" {
		info.Type = "video"
	}
	if info.URL == "" {
		info.URL = url
	}
	for i := range info.Entries {
		if info.Entries[i].URL == "" {
			info.Entries[i].URL = info.Entries[i].ID
		}
	}
	return &info, nil
}

func cleanErr(stderr string, err error) error {
	for _, l := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(l, "ERROR:") {
			msg := strings.TrimSpace(strings.TrimPrefix(l, "ERROR:"))
			// "[youtube] abc: Video unavailable" — drop the extractor tag
			if strings.HasPrefix(msg, "[") {
				if i := strings.Index(msg, "]"); i > 0 {
					msg = strings.TrimSpace(msg[i+1:])
				}
			}
			switch {
			case strings.Contains(msg, "Unsupported URL"):
				return errors.New("this site is not supported, or the link is not a video or playlist page")
			case strings.Contains(msg, "HTTP Error 404"):
				return errors.New("the video is gone (HTTP 404)")
			case strings.Contains(strings.ToLower(msg), "sign in") || strings.Contains(msg, "cookies"):
				return fmt.Errorf("the site wants a sign-in for this video (%s)", oneLine(msg, 160))
			}
			return errors.New(oneLine(msg, 300))
		}
	}
	if l := lastLine(stderr); l != "" {
		return errors.New(oneLine(l, 300))
	}
	return err
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

// ---------- downloading ----------

// Job is the audio of one video to fetch.
type Job struct {
	URL     string
	Dir     string // where the finished file goes
	TempDir string // where pieces wait meanwhile
	Name    string // file name without extension (yt-dlp adds the audio's own)
	Options
}

// Progress is one report while downloading.
type Progress struct {
	Downloaded int64   `json:"downloaded_bytes"`
	Total      int64   `json:"total_bytes"`
	Estimate   float64 `json:"total_bytes_estimate"`
	Speed      float64 `json:"speed"`
	ETA        float64 `json:"eta"`
	Status     string  `json:"status"`
}

// Result is what one download left behind.
type Result struct {
	File  string // the audio
	Thumb string // its picture as .jpg ("" if the site had none)
}

func (j Job) args() []string {
	// the best audio as it is (opus or AAC on YouTube) — remuxed, never re-encoded
	a := []string{"--newline", "--no-colors", "--progress", "--no-playlist", "--no-mtime", "--socket-timeout", "30", "--retries", "5",
		"--fragment-retries", "10", "-f", "bestaudio/best", "-x", "--audio-quality", "0",
		"--write-thumbnail", "--convert-thumbnails", "jpg", "--no-write-playlist-metafiles",
		"-P", "home:" + j.Dir, "-P", "temp:" + j.TempDir, "-o", strings.ReplaceAll(j.Name, "%", "%%") + ".%(ext)s",
		"--progress-template", "download:PMUPROG %(progress.{downloaded_bytes,total_bytes,total_bytes_estimate,speed,eta,status})j",
		"--print", "after_move:PMUFILE %(filepath)s",
	}
	return append(append(a, jsArgs()...), j.Options.args()...)
}

// Download fetches one video's audio; progress is called as yt-dlp reports.
func (r *Runner) Download(ctx context.Context, j Job, onProgress func(Progress)) (*Result, error) {
	p, err := r.Path(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range []string{j.Dir, j.TempDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, p, append(j.args(), "--", j.URL)...)
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 15 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errb tailBuffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var file string
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "PMUPROG "):
			var pr Progress
			if json.Unmarshal([]byte(line[8:]), &pr) == nil && onProgress != nil {
				onProgress(pr)
			}
		case strings.HasPrefix(line, "PMUFILE "):
			file = strings.TrimSpace(line[8:])
		}
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, cleanErr(errb.String(), err)
	}
	if file == "" {
		return nil, errors.New("yt-dlp finished but did not say where the file is")
	}
	res := &Result{File: file}
	if t := strings.TrimSuffix(file, filepath.Ext(file)) + ".jpg"; fileExists(t) {
		res.Thumb = t
	}
	return res, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// tailBuffer keeps the last 64 KiB written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 64<<10 {
		t.buf = t.buf[len(t.buf)-64<<10:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
