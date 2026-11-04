package server

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/abjelosevic88/pocket-homelab-music/internal/navidrome"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
	"github.com/abjelosevic88/pocket-homelab-music/internal/subsonic"
)

// ---- The Navidrome that comes with Pocket Music (NAVIDROME_BUNDLED) ----
//
// The sample docker-compose.yml can start Navidrome next to Pocket Music (the
// "navidrome" profile, on in the sample .env and in Pocket Core's installer),
// with no port of its own, over the same music folder Pocket Music writes
// lyrics and link downloads into. On a fresh Navidrome, Pocket Music makes the
// first account (administrator "pocket-music", a random password sealed with
// secret.key); everyone who uses Pocket Music then gets their own Navidrome
// account the first time (connection kind "bundled"), so playlists, stars and
// play counts stay theirs. A Navidrome that already has accounts is left
// alone: people sign in to it under Settings → Library like to any other. That
// is how NAVIDROME_BUNDLED=auto (the sample's) tells the two apart, so the
// same compose file runs with the bundled Navidrome or pointed at your own.

const (
	bundledAdmin   = "pocket-music"
	kvBundledAdmin = "bundled:admin" // sealed password of the administrator
	kvBundledReady = "bundled:ready" // the administrator exists: this app runs the server's accounts
	kvBundledUser  = "bundled:user:" // + person id → Navidrome user name NUL sealed password
)

type bundledState struct {
	on    atomic.Bool
	mu    sync.Mutex
	ready bool
	state string // why it isn't ready, for Settings
	users sync.Mutex
}

// bundledOn: everyone gets an account on the Navidrome that came with Pocket Music.
func (s *Server) bundledOn() bool {
	return s.cfg.NavidromeBundled == "true" || s.bundled.on.Load()
}

func (s *Server) setBundled(ready bool, state string) {
	s.bundled.mu.Lock()
	s.bundled.ready, s.bundled.state = ready, state
	s.bundled.mu.Unlock()
}

func (s *Server) bundledReady() (bool, string) {
	s.bundled.mu.Lock()
	defer s.bundled.mu.Unlock()
	return s.bundled.ready, s.bundled.state
}

var (
	errBundledByHand = errors.New("this Navidrome already had accounts when Pocket Music started: sign in to it under Settings → Library")
	errNotBundled    = errors.New("not bundled")
)

// bundledLoop makes the administrator, waiting for the server to start first.
func (s *Server) bundledLoop(ctx context.Context) {
	if _, _, ok := s.store.CacheGet(ctx, kvBundledReady); ok {
		s.bundled.on.Store(true)
	}
	s.setBundled(false, "the Navidrome that comes with Pocket Music is starting")
	wait := 2 * time.Second
	for {
		err := s.setupBundled(ctx)
		switch {
		case err == nil:
			s.setBundled(true, "")
			return
		case errors.Is(err, errNotBundled):
			s.setBundled(false, "")
			s.log.Info("navidrome already has accounts; people sign in to it under Settings → Library", "url", s.cfg.NavidromeURL)
			return
		case errors.Is(err, errBundledByHand):
			s.setBundled(false, err.Error())
			s.log.Warn("bundled navidrome", "err", err)
			return
		}
		s.setBundled(false, "the Navidrome that comes with Pocket Music is not ready: "+err.Error())
		if wait >= 30*time.Second && s.bundledOn() {
			s.log.Warn("bundled navidrome not ready", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < time.Minute {
			wait *= 2
		}
	}
}

func (s *Server) setupBundled(ctx context.Context) error {
	nd := navidrome.New(s.cfg.NavidromeURL)
	pw := s.openKV(ctx, kvBundledAdmin)
	if pw != "" {
		err := nd.Login(ctx, bundledAdmin, pw)
		if err == nil {
			return s.bundledIsOurs(ctx)
		}
		var e *navidrome.Error
		if !errors.As(err, &e) || e.Status != 401 {
			return err // not reachable yet
		}
		// never made (the first try did not reach it): make it now with the same password
	} else {
		// kept before asking, so an answer lost on the way still signs in next time
		pw = store.RandomToken(24)
		if err := s.sealKV(ctx, kvBundledAdmin, pw); err != nil {
			return err
		}
	}
	err := nd.CreateAdmin(ctx, bundledAdmin, pw)
	if errors.Is(err, navidrome.ErrHasUsers) {
		_ = s.store.CacheDelete(ctx, kvBundledAdmin)
		if _, _, ours := s.store.CacheGet(ctx, kvBundledReady); ours {
			return fmt.Errorf("signing in as %s: Navidrome does not accept the password Pocket Music set", bundledAdmin)
		}
		if s.cfg.NavidromeBundled == "auto" {
			return errNotBundled
		}
		return errBundledByHand
	}
	if err != nil {
		return err
	}
	s.log.Info("bundled navidrome set up", "admin", bundledAdmin)
	return s.bundledIsOurs(ctx)
}

func (s *Server) bundledIsOurs(ctx context.Context) error {
	s.bundled.on.Store(true)
	return s.store.CachePut(ctx, kvBundledReady, "1")
}

func (s *Server) openKV(ctx context.Context, k string) string {
	v, _, ok := s.store.CacheGet(ctx, k)
	if !ok {
		return ""
	}
	plain, err := s.open(v)
	if err != nil {
		return ""
	}
	return plain
}

func (s *Server) sealKV(ctx context.Context, k, v string) error {
	sealed, err := s.seal(v)
	if err != nil {
		return err
	}
	return s.store.CachePut(ctx, k, sealed)
}

// musicBundled is the person's own account on the bundled Navidrome, made
// the first time they need it.
func (s *Server) musicBundled(ctx context.Context, u *store.User) (*musicConn, error) {
	if ok, state := s.bundledReady(); !ok {
		return nil, &errNoMusic{firstNonEmpty(state, "the Navidrome that comes with Pocket Music is starting")}
	}
	user, pw, err := s.bundledUser(ctx, u)
	if err != nil {
		return nil, &errNoMusic{"could not make your account on the Navidrome that comes with Pocket Music: " + err.Error()}
	}
	base := s.cfg.NavidromeURL
	return &musicConn{c: subsonic.New(base, user, pw), via: "bundled", public: s.cfg.NavidromePublicURL, key: base + "|" + user}, nil
}

func (s *Server) bundledUser(ctx context.Context, u *store.User) (string, string, error) {
	k := kvBundledUser + strconv.FormatInt(u.ID, 10)
	get := func() (string, string, bool) {
		v, _, ok := s.store.CacheGet(ctx, k)
		if !ok {
			return "", "", false
		}
		name, sealed, _ := strings.Cut(v, "\x00")
		pw, err := s.open(sealed)
		return name, pw, err == nil && pw != ""
	}
	if name, pw, ok := get(); ok {
		return name, pw, nil
	}
	s.bundled.users.Lock()
	defer s.bundled.users.Unlock()
	if name, pw, ok := get(); ok {
		return name, pw, nil
	}
	nd := navidrome.New(s.cfg.NavidromeURL)
	if err := nd.Login(ctx, bundledAdmin, s.openKV(ctx, kvBundledAdmin)); err != nil {
		return "", "", err
	}
	users, err := nd.Users(ctx)
	if err != nil {
		return "", "", err
	}
	taken := map[string]bool{bundledAdmin: true}
	for _, x := range users {
		taken[strings.ToLower(x.UserName)] = true
	}
	base := bundledLogin(u)
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s%d", base, i)
	}
	// Nobody types it: Pocket Music signs in for them (phone apps use the app logins
	// under Settings → Phone & car apps).
	pw := store.RandomToken(24)
	if err := nd.CreateUser(ctx, name, firstNonEmpty(strings.TrimSpace(u.Name), name), pw); err != nil {
		return "", "", err
	}
	sealed, err := s.seal(pw)
	if err != nil {
		return "", "", err
	}
	if err := s.store.CachePut(ctx, k, name+"\x00"+sealed); err != nil {
		return "", "", err
	}
	s.log.Info("bundled navidrome account made", "user", u.Email, "navidrome", name)
	return name, pw, nil
}

// bundledLogin is a Navidrome user name for the person: lower-case letters and digits.
func bundledLogin(u *store.User) string {
	src := strings.TrimSpace(u.Name)
	if src == "" {
		src, _, _ = strings.Cut(u.Email, "@")
	}
	var b strings.Builder
	for _, r := range strings.ToLower(src) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsLetter(r):
			if a := asciiFold(r); a != 0 {
				b.WriteRune(a)
			}
		}
	}
	if b.Len() == 0 {
		return "person" + strconv.FormatInt(u.ID, 10)
	}
	return b.String()
}

// asciiFold maps common accented letters to plain ones (č → c), else 0.
func asciiFold(r rune) rune {
	for plain, set := range map[rune]string{'a': "àáâãäåā", 'c': "çćč", 'd': "đď", 'e': "èéêëēěę", 'i': "ìíîïī", 'n': "ñń", 'o': "òóôõöøō", 's': "śšş", 'u': "ùúûüūů", 'y': "ýÿ", 'z': "źżž", 'l': "ł", 'r': "ř", 't': "ť"} {
		if strings.ContainsRune(set, r) {
			return plain
		}
	}
	return 0
}
