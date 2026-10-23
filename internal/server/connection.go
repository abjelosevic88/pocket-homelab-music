package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// ---- the music server, per person ----
//
// Every person plays their library with their OWN account on the music
// server, so playlists, stars, ratings and play counts stay theirs. Two ways
// in: an account they type in here (the password is sealed with
// DATA_DIR/secret.key), or the Navidrome account Pocket Core holds for them
// (nothing stored here; Core is asked at most once a minute, so a link
// switched off in Core applies at once). With neither, the library stays
// empty and Settings → Library explains what to do.

// musicConn is how one request reaches the person's library.
type musicConn struct {
	c      *subsonic.Client
	via    string // direct | core
	public string // what a browser opens ("Open in Navidrome")
	key    string // server URL + account: the index belongs to exactly this
}

// errNoMusic means the person has no working way to the music server.
type errNoMusic struct{ msg string }

func (e *errNoMusic) Error() string { return e.msg }

func isNoMusic(err error) bool {
	var e *errNoMusic
	return errors.As(err, &e)
}

// serverBase is the address used for a direct account.
func (s *Server) serverBase(c *store.Connection) string {
	if c != nil && c.ServerURL != "" {
		return c.ServerURL
	}
	return s.cfg.NavidromeURL
}

func validServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return errors.New("the music server address must be http(s)://host[:port]")
	}
	return nil
}

func coreReason(err error, service string) string {
	var le *core.LinkError
	if errors.As(err, &le) {
		switch le.Reason {
		case "off_person":
			return "you switched Pocket Music's access to " + service + " off in Pocket Core (Settings → Apps acting for me)"
		case "off_admin":
			return "Pocket Music's access to " + service + " is switched off in Pocket Core (Links)"
		case "no_account":
			return "Pocket Core has no " + service + " account for you yet: add your login under Settings → My logins in Pocket Core, or enter it here"
		case "not_in_audience":
			return "you do not have access to " + service + " in Pocket Core"
		case "target_missing":
			return service + " is not set up in Pocket Core"
		case "not_declared":
			return "Pocket Core has not seen this Pocket Music version yet; it re-registers within a minute of starting"
		case "unknown_user":
			return "your email is not a person in Pocket Core"
		}
		return le.Message
	}
	return "Pocket Core did not answer: " + err.Error()
}

// musicFor connects to the person's music server account.
func (s *Server) musicFor(ctx context.Context, u *store.User) (*musicConn, error) {
	if u == nil {
		return nil, &errNoMusic{"nobody signed in"}
	}
	c, err := s.store.GetConnection(ctx, u.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if c != nil && c.Kind == "off" {
		return nil, &errNoMusic{"your library is switched off in Settings → Library"}
	}
	if c != nil && c.Kind == "direct" {
		base := s.serverBase(c)
		if base == "" {
			return nil, &errNoMusic{"no music server address: set NAVIDROME_URL on the server or enter one in Settings → Library"}
		}
		pw, err := s.open(c.Secret)
		if err != nil {
			return nil, &errNoMusic{err.Error()}
		}
		public := s.cfg.NavidromePublicURL
		if c.ServerURL != "" {
			public = c.ServerURL
		}
		return &musicConn{c: subsonic.New(base, c.Username, pw), via: "direct", public: firstNonEmpty(public, base), key: base + "|" + c.Username}, nil
	}
	// kind core, or nothing chosen yet: Pocket Core when the box has one
	if !s.core.Configured() {
		return nil, &errNoMusic{"connect your music server account in Settings → Library"}
	}
	cred, err := s.core.ServiceFor(ctx, u.Email, "navidrome")
	if err != nil {
		return nil, &errNoMusic{coreReason(err, "Navidrome")}
	}
	user := firstNonEmpty(cred.Fields["USER"], cred.Fields["USERNAME"])
	pw := firstNonEmpty(cred.Key, cred.Fields["PASSWORD"])
	if cred.URL == "" || user == "" || pw == "" {
		return nil, &errNoMusic{"Pocket Core has Navidrome without a username and password for you: add them under Settings → My logins in Pocket Core"}
	}
	base := cred.URL
	// Core may hand out the public address; inside the box the direct one is faster and needs no TLS
	if s.cfg.NavidromeURL != "" && cred.PublicURL != "" && strings.EqualFold(strings.TrimRight(cred.URL, "/"), strings.TrimRight(cred.PublicURL, "/")) {
		base = s.cfg.NavidromeURL
	}
	return &musicConn{c: subsonic.New(base, user, pw), via: "core", public: firstNonEmpty(cred.PublicURL, s.cfg.NavidromePublicURL, cred.URL), key: strings.TrimRight(cred.URL, "/") + "|" + user}, nil
}

// music is musicFor for the signed-in person, writing the error itself.
func (s *Server) music(w http.ResponseWriter, r *http.Request) *musicConn {
	mc, err := s.musicFor(r.Context(), userFrom(r.Context()))
	if err != nil {
		if isNoMusic(err) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "not_connected"})
			return nil
		}
		s.fail(w, err, "music server")
		return nil
	}
	return mc
}

// musicErr writes a failed music-server call.
func (s *Server) musicErr(w http.ResponseWriter, err error) {
	switch {
	case subsonic.IsNotFound(err):
		writeErr(w, http.StatusNotFound, "not found")
	case subsonic.IsAuth(err):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "not_connected"})
	default:
		s.log.Warn("music server", "err", err)
		writeErr(w, http.StatusBadGateway, err.Error())
	}
}

// ---- Settings → Library ----

func (s *Server) handleConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	c, _ := s.store.GetConnection(ctx, u.ID)
	out := map[string]any{
		"server_url":      s.cfg.NavidromeURL != "",
		"core":            s.coreConnected(),
		"core_configured": s.core.Configured(),
		"sync":            s.store.Sync(ctx, u.ID),
		"syncing":         s.isSyncing(u.ID),
	}
	mode := "core"
	if c != nil {
		mode = c.Kind
		out["username"] = c.Username
		out["custom_url"] = c.ServerURL
	} else if !s.core.Configured() {
		mode = "direct"
	}
	out["mode"] = mode
	mc, err := s.musicFor(ctx, u)
	if err != nil {
		out["connected"] = false
		out["message"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	name, ver, err := mc.c.Ping(ctx)
	if err != nil {
		out["connected"] = false
		out["message"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["connected"] = true
	out["via"] = mc.via
	out["account"] = mc.c.User
	out["server_type"] = name
	out["server_version"] = ver
	out["public_url"] = mc.public
	writeJSON(w, http.StatusOK, out)
}

// handleConnect saves the person's own account (after checking it) or
// switches to Pocket Core / off.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	var in struct {
		Mode     string `json:"mode"` // direct | core | off
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	old, _ := s.store.GetConnection(ctx, u.ID)
	switch in.Mode {
	case "off", "core":
		if in.Mode == "core" && !s.core.Configured() {
			writeErr(w, http.StatusBadRequest, "there is no Pocket Core on this box")
			return
		}
		if err := s.store.SaveConnection(ctx, &store.Connection{UserID: u.ID, Kind: in.Mode}); err != nil {
			s.fail(w, err, "save connection")
			return
		}
	case "direct":
		in.URL = strings.TrimRight(strings.TrimSpace(in.URL), "/")
		in.Username = strings.TrimSpace(in.Username)
		if in.URL != "" {
			if err := validServerURL(in.URL); err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		base := firstNonEmpty(in.URL, s.cfg.NavidromeURL)
		if base == "" {
			writeErr(w, http.StatusBadRequest, "enter the music server address")
			return
		}
		pw := in.Password
		if pw == "" && old != nil && old.Kind == "direct" && old.Username == in.Username {
			pw, _ = s.open(old.Secret) // keep the saved password when only the address changes
		}
		if in.Username == "" || pw == "" {
			writeErr(w, http.StatusBadRequest, "enter your username and password")
			return
		}
		c := subsonic.New(base, in.Username, pw)
		typ, ver, err := c.Ping(ctx)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		sealed, err := s.seal(pw)
		if err != nil {
			s.fail(w, err, "seal password")
			return
		}
		if err := s.store.SaveConnection(ctx, &store.Connection{UserID: u.ID, Kind: "direct", ServerURL: in.URL, Username: in.Username, Secret: sealed, ServerType: typ, ServerVersion: ver}); err != nil {
			s.fail(w, err, "save connection")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "mode is direct, core or off")
		return
	}
	// a different account means a different library: rebuild the index
	if in.Mode == "off" {
		_ = s.store.ForgetLibrary(ctx, u.ID)
	} else {
		s.syncSoon(u.ID, true)
	}
	s.handleConnection(w, r)
}
