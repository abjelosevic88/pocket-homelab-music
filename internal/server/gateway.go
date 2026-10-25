package server

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"

	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// ---- the Subsonic gateway (/rest/*) ----
//
// Any Subsonic app — Amperfy, play:Sub, Substreamer, Symfonium, Feishin, a
// car stereo — can use Pocket Music as its server: it signs in with the
// person's Pocket Music app password (Settings → Phone & car apps) or an
// API token (OpenSubsonic apiKey), and every call is passed on to that
// person's own music-server account. The app never learns the Navidrome
// password, and Pocket Music sees the plays: a scrobble lands in the
// person's history (and still reaches Navidrome), "now playing" shows up as
// a device. Everything else — browsing, streaming, playlists, stars — is the
// music server's own answer, byte for byte.

// gatewayAuthKeys are the parameters that carry the client's credentials.
var gatewayAuthKeys = map[string]bool{"u": true, "p": true, "t": true, "s": true, "apiKey": true}

// gatewayStreams are answered as raw bytes (byte ranges passed through).
var gatewayStreams = map[string]bool{"stream": true, "download": true, "getCoverArt": true, "getAvatar": true, "hls": true, "getCaptions": true}

var gatewayMethod = regexp.MustCompile(`^[A-Za-z0-9]{1,40}$`)

// gatewayChanges are calls that change the person's data: after the music
// server says ok, the index follows and the person's open pages are told.
var gatewayChanges = map[string]bool{"star": true, "unstar": true, "setRating": true, "createPlaylist": true, "updatePlaylist": true, "deletePlaylist": true}

var gatewayOK = regexp.MustCompile(`"status"\s*:\s*"ok"|status="ok"`)

// failures counts wrong passwords per user name for a short lockout.
type failures struct {
	mu sync.Mutex
	m  map[string][]time.Time
}

func (f *failures) blocked(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	recent := f.m[key][:0]
	for _, t := range f.m[key] {
		if time.Since(t) < 15*time.Minute {
			recent = append(recent, t)
		}
	}
	f.m[key] = recent
	return len(recent) >= 10
}

func (f *failures) add(key string) {
	f.mu.Lock()
	f.m[key] = append(f.m[key], time.Now())
	f.mu.Unlock()
}

var gatewayFailures = &failures{m: map[string][]time.Time{}}

// gatewayError answers in the format the client asked for.
func gatewayError(w http.ResponseWriter, f string, code int, msg string) {
	if f == "json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{
			"status": "failed", "version": "1.16.1", "type": "pocket-music", "serverVersion": Version, "openSubsonic": true,
			"error": map[string]any{"code": code, "message": msg}}})
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(msg))
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><subsonic-response xmlns="http://subsonic.org/restapi" status="failed" version="1.16.1" type="pocket-music" serverVersion=%q openSubsonic="true"><error code="%d" message="%s"/></subsonic-response>`, Version, code, b.String())
}

// gatewayUser checks the client's credentials.
func (s *Server) gatewayUser(ctx context.Context, q url.Values) (*store.User, string, error) {
	if key := q.Get("apiKey"); key != "" {
		// A phone paired with Pocket Core signs in with its device token (Pocket Homelab).
		if s.core.Configured() && core.IsCoreToken(key) {
			u, err := s.coreUser(ctx, key)
			return u, "", err
		}
		u, err := s.store.GetUserByAPIToken(ctx, key)
		return u, "", err
	}
	name := strings.TrimSpace(q.Get("u"))
	if name == "" {
		return nil, "", errors.New("missing user name")
	}
	if gatewayFailures.blocked(strings.ToLower(name)) {
		return nil, name, errors.New("too many wrong passwords; wait 15 minutes")
	}
	login, err := s.store.SubsonicLoginByName(ctx, name)
	if err != nil {
		gatewayFailures.add(strings.ToLower(name))
		return nil, name, errors.New("wrong user name or password")
	}
	secret, err := s.open(login.Secret)
	if err != nil || secret == "" {
		return nil, name, errors.New("the app password can't be read: make a new one in Pocket Music → Settings → Phone & car apps")
	}
	ok := false
	switch {
	case q.Get("t") != "" && q.Get("s") != "":
		h := md5.Sum([]byte(secret + q.Get("s")))
		ok = subtle.ConstantTimeCompare([]byte(hex.EncodeToString(h[:])), []byte(strings.ToLower(q.Get("t")))) == 1
	case q.Get("p") != "":
		p := q.Get("p")
		if strings.HasPrefix(p, "enc:") {
			if b, err := hex.DecodeString(p[4:]); err == nil {
				p = string(b)
			}
		}
		ok = subtle.ConstantTimeCompare([]byte(p), []byte(secret)) == 1
	}
	if !ok {
		gatewayFailures.add(strings.ToLower(name))
		return nil, name, errors.New("wrong user name or password")
	}
	u, err := s.store.GetUser(ctx, login.UserID)
	return u, name, err
}

func (s *Server) handleGateway(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	q := r.Form
	f := q.Get("f")
	method := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/"), ".view")
	if !gatewayMethod.MatchString(method) {
		gatewayError(w, f, 0, "unknown method")
		return
	}
	ctx := r.Context()
	u, _, err := s.gatewayUser(ctx, q)
	if err != nil || u == nil {
		time.Sleep(300 * time.Millisecond)
		msg := "wrong user name or password"
		if err != nil {
			msg = err.Error()
		}
		gatewayError(w, f, 40, msg)
		return
	}
	mc, err := s.musicFor(ctx, u)
	if err != nil {
		gatewayError(w, f, 0, "Pocket Music has no music-server account for you: "+err.Error())
		return
	}
	client := trim(q.Get("c"), 60)
	if s.touchDue(u.ID) { // apps fetch covers by the hundred: one write a minute is plenty
		go s.store.TouchSubsonicLogin(context.Background(), u.ID, client)
	}

	switch method {
	case "scrobble":
		s.gatewayScrobble(ctx, u, mc, q, client)
	}

	// pass the call on with the person's own credentials
	out := url.Values{}
	for k, vs := range q {
		if !gatewayAuthKeys[k] {
			out[k] = vs
		}
	}
	// the app knows the person by their Pocket user name; the server by its own
	if name := out.Get("username"); name != "" && !strings.EqualFold(name, mc.c.User) {
		if l, err := s.store.GetSubsonicLogin(ctx, u.ID); err == nil && strings.EqualFold(l.Username, name) {
			out.Set("username", mc.c.User)
		}
	}
	salt := store.RandomToken(6)
	h := md5.Sum([]byte(mc.c.Password + salt))
	out.Set("u", mc.c.User)
	out.Set("t", hex.EncodeToString(h[:]))
	out.Set("s", salt)
	if out.Get("v") == "" {
		out.Set("v", "1.16.1")
	}
	if out.Get("c") == "" {
		out.Set("c", subsonic.ClientName)
	}
	target := mc.c.Base + "/rest/" + method
	var req *http.Request
	if gatewayStreams[method] {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, target+"?"+out.Encode(), nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(out.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		gatewayError(w, f, 0, err.Error())
		return
	}
	for _, k := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since", "Accept", "Accept-Encoding"} {
		if k == "Accept-Encoding" && gatewayChanges[method] {
			continue // the answer is read here: let the transport unzip it
		}
		if v := r.Header.Get(k); v != "" {
			req.Header.Set(k, v)
		}
	}
	res, err := mc.c.Stream.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			gatewayError(w, f, 0, "the music server did not answer")
		}
		return
	}
	defer res.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Content-Encoding", "Accept-Ranges", "Last-Modified", "ETag", "Cache-Control", "Content-Disposition", "X-Content-Duration", "Expires"} {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	if gatewayChanges[method] && res.StatusCode == http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		w.Header().Del("Content-Length")
		w.WriteHeader(res.StatusCode)
		_, _ = w.Write(body)
		if err == nil && gatewayOK.Match(body) {
			s.gatewayChanged(ctx, u, method, q)
		}
		return
	}
	w.WriteHeader(res.StatusCode)
	buf := make([]byte, 64<<10)
	fl, _ := w.(http.Flusher)
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil && gatewayStreams[method] {
				fl.Flush()
			}
		}
		if err != nil {
			if err != io.EOF {
				s.log.Debug("gateway copy", "method", method, "err", err)
			}
			return
		}
	}
}

// gatewayScrobble keeps what a Subsonic app reports: finished plays go into
// the person's history, "now playing" becomes a device.
func (s *Server) gatewayScrobble(ctx context.Context, u *store.User, mc *musicConn, q url.Values, client string) {
	ids := q["id"]
	times := q["time"]
	submission := q.Get("submission") != "false"
	device := firstNonEmpty(client, "Subsonic app")
	for i, id := range ids {
		songs, err := s.songsFor(ctx, u.ID, []string{id})
		if err != nil || len(songs) == 0 {
			continue
		}
		so := songs[0]
		if !submission {
			// the song as a one-song queue at 0 ms: "Play here" continues it from where the app should be now
			q, _ := json.Marshal([]string{so.ID})
			d := &store.Device{ID: "subsonic:" + strings.ToLower(device), Name: device, SongID: so.ID, Queue: string(q), Playing: true}
			if err := s.store.SaveDevice(ctx, u.ID, d); err == nil {
				if cur, err := s.store.GetDevice(ctx, u.ID, d.ID); err == nil {
					s.hub.send(u.ID, "device", s.viewDevice(ctx, u.ID, *cur))
				}
			}
			continue
		}
		at := time.Now().Add(-time.Duration(so.Duration) * time.Second)
		if i < len(times) {
			if ms, err := strconv.ParseInt(times[i], 10, 64); err == nil && ms > 0 {
				at = time.UnixMilli(ms)
			}
		}
		l := &store.Listen{SongID: so.ID, Title: so.Title, Artist: so.Artist, Album: so.Album, AlbumID: so.AlbumID, ArtistID: firstNonEmpty(so.ArtistID, so.AlbumArtistID),
			Genre: so.Genre, Duration: so.Duration, PlayedAt: store.Stamp(at), Source: "subsonic", Device: device}
		if added, err := s.store.AddListen(ctx, u.ID, l); err == nil && added {
			s.liveListens(u.ID, l)
		}
	}
	_ = mc
}

// gatewayChanged keeps the index in step with what an app changed on the
// music server, and tells the person's open pages.
func (s *Server) gatewayChanged(ctx context.Context, u *store.User, method string, q url.Values) {
	switch method {
	case "star", "unstar":
		on := method == "star"
		// a plain id may be a song, an album or an artist (ids don't collide)
		for _, id := range q["id"] {
			for _, kind := range []string{"song", "album", "artist"} {
				_ = s.store.SetStarred(ctx, u.ID, kind, id, on)
			}
		}
		for _, id := range q["albumId"] {
			_ = s.store.SetStarred(ctx, u.ID, "album", id, on)
		}
		for _, id := range q["artistId"] {
			_ = s.store.SetStarred(ctx, u.ID, "artist", id, on)
		}
		s.liveStars(u.ID, "song", q["id"], on)
		s.liveStars(u.ID, "album", q["albumId"], on)
		s.liveStars(u.ID, "artist", q["artistId"], on)
	case "setRating":
		rating, err := strconv.Atoi(q.Get("rating"))
		id := q.Get("id")
		if err != nil || rating < 0 || rating > 5 || id == "" {
			return
		}
		for _, kind := range []string{"song", "album", "artist"} {
			_ = s.store.SetRating(ctx, u.ID, kind, id, rating)
		}
		s.liveRatings(u.ID, "song", []string{id}, rating)
	case "createPlaylist":
		if id := q.Get("playlistId"); id != "" {
			s.livePlaylists(u.ID, "updated", []string{id}, 0)
		} else {
			s.livePlaylists(u.ID, "created", nil, 0)
		}
	case "updatePlaylist":
		s.livePlaylists(u.ID, "updated", nz(q["playlistId"]), 0)
	case "deletePlaylist":
		// a smart playlist's rules stay: its next refresh makes the playlist again
		s.livePlaylists(u.ID, "deleted", nz(q["id"]), 0)
	}
}

// ---- Settings → Phone & car apps ----

func (s *Server) gatewayInfo(ctx context.Context, u *store.User) map[string]any {
	out := map[string]any{"enabled": false, "server_url": firstNonEmpty(s.cfg.BaseURL, ""), "suggested": suggestUsername(u)}
	if l, err := s.store.GetSubsonicLogin(ctx, u.ID); err == nil {
		out["enabled"] = true
		out["username"] = l.Username
		out["created_at"] = l.CreatedAt
		out["last_used_at"] = l.LastUsedAt
		out["last_client"] = l.LastClient
	}
	return out
}

func suggestUsername(u *store.User) string {
	name := strings.ToLower(strings.Split(u.Email, "@")[0])
	name = regexp.MustCompile(`[^a-z0-9._-]`).ReplaceAllString(name, "")
	if name == "" {
		name = "user" + strconv.FormatInt(u.ID, 10)
	}
	return name
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{2,40}$`)

func (s *Server) handleGatewayInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.gatewayInfo(r.Context(), userFrom(r.Context())))
}

// handleGatewayPassword makes (or renews) the person's app password and
// returns it once; a user name can be chosen at the same time.
func (s *Server) handleGatewayPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Username string `json:"username"`
	}
	_ = decode(r, &in)
	l, err := s.store.GetSubsonicLogin(ctx, u.ID)
	if err != nil {
		l = &store.SubsonicLogin{UserID: u.ID, Username: suggestUsername(u)}
	}
	if name := strings.TrimSpace(in.Username); name != "" {
		if !usernameRe.MatchString(name) {
			writeErr(w, http.StatusBadRequest, "a user name is 2–40 letters, digits, dots, dashes or underscores")
			return
		}
		l.Username = name
	}
	pw := strings.ToLower(store.RandomToken(4)) + "-" + strings.ToLower(store.RandomToken(4)) + "-" + strings.ToLower(store.RandomToken(4))
	if l.Secret, err = s.seal(pw); err != nil {
		s.fail(w, err, "seal")
		return
	}
	if err := s.store.SaveSubsonicLogin(ctx, l); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "that user name is taken: pick another")
			return
		}
		s.fail(w, err, "save login")
		return
	}
	out := s.gatewayInfo(ctx, u)
	out["password"] = pw
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGatewayDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSubsonicLogin(r.Context(), actorID(r.Context())); err != nil {
		s.fail(w, err, "delete login")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var gatewayTouched sync.Map // user id → time of the last "last used" write

func (s *Server) touchDue(uid int64) bool {
	now := time.Now()
	if v, ok := gatewayTouched.Load(uid); ok && now.Sub(v.(time.Time)) < time.Minute {
		return false
	}
	gatewayTouched.Store(uid, now)
	return true
}
