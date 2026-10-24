// Package server wires the HTTP API, auth and the embedded SPA.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-music/internal/config"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// Version is injected at build time.
var Version = "dev"

// Server holds dependencies.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	log      *slog.Logger
	webFS    fs.FS
	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64
	http     *http.Client // lyrics, Lidarr, Maloja, ListenBrainz

	oidcMu sync.Mutex
	oidc   *oidcProvider // for the issuer in use; rebuilt when it changes

	core    *core.Client
	coreMu  sync.Mutex
	coreOK  bool
	coreErr string

	syncMu  sync.Mutex
	syncing map[int64]bool // people whose index is being rebuilt

	hub *hub // live updates per person (now playing, devices, sync progress)

	jobsMu sync.Mutex
	jobs   map[string]bool // background jobs running (imports, smart refreshes)

	lyr lyricsJob      // the lyrics library run
	lix lyricsIndexJob // the lyrics index refresh
}

// New creates a server.
func New(cfg *config.Config, st *store.Store, log *slog.Logger, webFS fs.FS) *Server {
	return &Server{cfg: cfg, store: st, log: log, webFS: webFS, started: time.Now(), oidc: &oidcProvider{},
		http:    &http.Client{Timeout: 30 * time.Second},
		core:    core.New(cfg.CoreURL, core.Options{DB: st.DB, TokenFile: cfg.DataDir + "/core.token", UserAgent: cfg.UserAgent}),
		syncing: map[int64]bool{}, hub: newHub(), jobs: map[string]bool{}}
}

// Bootstrap seeds the first admin (if configured), the owner's connections
// from the environment and demo data, then starts the background work.
func (s *Server) Bootstrap(ctx context.Context) error {
	if err := s.core.Start(ctx); err != nil {
		return err
	}
	if err := s.loadSecretKey(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.cfg.FilesDir, 0o755); err != nil {
		return fmt.Errorf("files dir: %w", err)
	}
	s.store.FailRunningImports(ctx)
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n == 0 && s.cfg.AuthMode == "local" && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		if len(s.cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
		u := &store.User{Email: s.cfg.AdminEmail, Name: s.cfg.AdminName, PasswordHash: string(h), Role: "admin"}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return err
		}
		st, _ := s.store.GetSettings(ctx, 0)
		st.SetupComplete = true
		if err := s.store.SaveBoxSettings(ctx, st); err != nil {
			return err
		}
		s.log.Info("admin user created from environment", "email", u.Email)
	}
	if owner := s.store.OwnerID(ctx); owner > 0 {
		if err := s.ownerReady(ctx, owner); err != nil {
			return err
		}
	}
	_ = s.store.PurgeSessions(ctx)
	go s.janitor(ctx)
	if s.core.Configured() {
		go s.coreLoop(ctx)
	}
	return nil
}

// ownerReady runs once the box owner (the first admin) exists: the
// connections from the environment are the owner's — seeded into the owner's
// own settings only, never into anyone else's — and demo data lands in the
// owner's account.
func (s *Server) ownerReady(ctx context.Context, owner int64) error {
	st, err := s.store.GetSettings(ctx, owner)
	if err != nil {
		return err
	}
	changed := false
	// Each value is copied once: clearing it in Settings (to move over to
	// Pocket Core) must not bring it back at the next start.
	once := func(name string, empty bool, v string, apply func()) {
		key := fmt.Sprintf("seeded:%d:%s", owner, name)
		if _, _, done := s.store.CacheGet(ctx, key); done {
			return
		}
		if empty && v != "" {
			apply()
			changed = true
			empty = false
		}
		if !empty {
			_ = s.store.CachePut(ctx, key, "1")
		}
	}
	set := func(name string, dst *string, v string) {
		once(name, *dst == "", v, func() { *dst = v })
	}
	set("NotifyURL", &st.NotifyURL, s.cfg.NotifyURL)
	set("LidarrURL", &st.LidarrURL, s.cfg.LidarrURL)
	set("LidarrKey", &st.LidarrKey, s.cfg.LidarrKey)
	set("MalojaURL", &st.MalojaURL, s.cfg.MalojaURL)
	set("OpenAIKey", &st.OpenAIKey, s.cfg.OpenAIKey)
	set("OpenAIModel", &st.OpenAIModel, s.cfg.OpenAIModel)
	set("OpenAIBaseURL", &st.OpenAIBaseURL, s.cfg.OpenAIBaseURL)
	if s.cfg.NotifyFormat != "" && st.NotifyURL == s.cfg.NotifyURL && st.NotifyFormat != s.cfg.NotifyFormat {
		st.NotifyFormat = s.cfg.NotifyFormat
		changed = true
	}
	if changed {
		if err := s.store.SaveUserSettings(ctx, owner, st); err != nil {
			return err
		}
	}
	// the owner's music-server account from NAVIDROME_USER / NAVIDROME_PASSWORD
	if s.cfg.NavidromeUser != "" && s.cfg.NavidromePassword != "" {
		key := fmt.Sprintf("seeded:%d:navidrome", owner)
		if _, _, done := s.store.CacheGet(ctx, key); !done {
			if _, err := s.store.GetConnection(ctx, owner); errors.Is(err, store.ErrNotFound) {
				sealed, err := s.seal(s.cfg.NavidromePassword)
				if err != nil {
					return err
				}
				if err := s.store.SaveConnection(ctx, &store.Connection{UserID: owner, Kind: "direct", Username: s.cfg.NavidromeUser, Secret: sealed}); err != nil {
					return err
				}
				s.log.Info("music server account seeded from the environment", "user", s.cfg.NavidromeUser)
			}
			_ = s.store.CachePut(ctx, key, "1")
		}
	}
	if s.cfg.DemoData {
		if err := s.seedDemo(ctx, owner); err != nil {
			return err
		}
	}
	return nil
}

// forgetUser drops in-memory state of a deleted account.
func (s *Server) forgetUser(id int64) {
	s.hub.drop(id)
}

// janitor runs the background work: every SYNC_EVERY each connected person's
// library index is checked against the server's last scan; every 15 minutes
// album requests are followed in Lidarr; once a day smart playlists refresh.
func (s *Server) janitor(ctx context.Context) {
	every := s.cfg.SyncEvery
	if every <= 0 {
		every = 10 * time.Minute
	}
	first := time.NewTimer(15 * time.Second)
	sync := time.NewTicker(every)
	slow := time.NewTicker(15 * time.Minute)
	defer sync.Stop()
	defer slow.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
			s.syncAll(ctx)
			s.followRequests(ctx)
			s.refreshDueSmart(ctx)
			s.scheduledLyricsIndex(ctx)
		case <-sync.C:
			s.syncAll(ctx)
		case <-slow.C:
			s.followRequests(ctx)
			s.refreshDueSmart(ctx)
			s.weeklyRecaps(ctx)
			s.scheduledLyrics(ctx)
			s.scheduledLyricsIndex(ctx)
			s.publishMixesDaily(ctx)
		}
	}
}

// job runs fn in the background unless a job with that key is running.
func (s *Server) job(key string, fn func(ctx context.Context)) bool {
	s.jobsMu.Lock()
	if s.jobs[key] {
		s.jobsMu.Unlock()
		return false
	}
	s.jobs[key] = true
	s.jobsMu.Unlock()
	go func() {
		defer func() {
			s.jobsMu.Lock()
			delete(s.jobs, key)
			s.jobsMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		fn(ctx)
	}()
	return true
}

// ---- Pocket Core ----

// coreLoop registers with Core on start (retrying until it answers) and
// re-registers every 30 minutes so version and last-seen stay fresh.
func (s *Server) coreLoop(ctx context.Context) {
	wait := 5 * time.Second
	for {
		err := s.registerWithCore(ctx)
		s.coreMu.Lock()
		s.coreOK, s.coreErr = err == nil, ""
		if err != nil {
			s.coreErr = err.Error()
		}
		s.coreMu.Unlock()
		if err != nil {
			s.log.Warn("pocket core: registration failed, retrying", "err", err, "in", wait.String())
		} else {
			wait = 30 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err != nil && wait < 5*time.Minute {
			wait *= 2
		}
	}
}

// eventTopics are what Music publishes on the Core bus.
var eventTopics = []string{"music.loved", "music.playlist_created", "music.album_requested", "music.album_arrived", "music.mix_made"}

func (s *Server) registerWithCore(ctx context.Context) error {
	m := core.Manifest{
		Slug: "music", Name: "Pocket Music",
		Description: "Your music library, played anywhere: Navidrome underneath, with listening history and stats, daily mixes and radio, smart playlists every Subsonic player sees, synced lyrics, \"make me a mix\" with AI, and albums you are missing fetched through Lidarr.",
		Icon:        "/favicon.svg", Version: Version,
		URL: s.cfg.CoreAppURL, PublicURL: s.cfg.BaseURL, HealthPath: "/healthz", EnvPrefix: "MUSIC",
		Publishes: eventTopics,
		Offers:    linkOffers,
		Uses:      []core.Use{core.InferenceUse()},
		Services: []core.ServiceUse{
			{Type: "navidrome", Reason: "Your music library: Pocket Music plays it with your own Navidrome account"},
			{Type: "lidarr", Reason: "Discographies, missing albums and album requests"},
			{Type: "maloja", Reason: "Import your listening history"},
		},
	}
	if s.cfg.BaseURL != "" {
		// ask Core for a relying-party client so "Sign in with Pocket Core" needs no configuration
		m.OIDC = &core.OIDCRequest{RedirectURIs: []string{s.oidcCallbackURL()}}
	}
	reg, err := s.core.Register(ctx, s.cfg.CoreRegistrationKey, m)
	if err != nil {
		return err
	}
	if reg.Token != "" {
		s.log.Info("pocket core: registered, app token stored", "core", s.cfg.CoreURL)
	} else {
		s.log.Debug("pocket core: re-registered", "core", s.cfg.CoreURL)
	}
	return nil
}

// coreStatus summarises the Pocket Core link for the About page.
func (s *Server) coreStatus() map[string]any {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return map[string]any{"configured": s.core.Configured(), "url": s.cfg.CoreURL, "registered": s.coreOK, "error": s.coreErr}
}

// coreConnected is "configured AND registered": only then are Core-only choices live.
func (s *Server) coreConnected() bool {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return s.core.Configured() && s.coreOK
}

// publish puts an event on the Core bus for one person (no-op without Core).
func (s *Server) publish(ctx context.Context, u *store.User, topic, subject string, payload map[string]any) {
	if !s.core.Configured() || u == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["user_email"] = u.Email
	if err := s.core.Publish(ctx, topic, subject, payload); err != nil {
		s.log.Debug("pocket core: publish failed", "topic", topic, "err", err)
	}
}
