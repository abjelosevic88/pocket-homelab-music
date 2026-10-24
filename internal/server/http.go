package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// Router builds the chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	if s.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(s.logging)
	r.Use(middleware.Recoverer)

	r.Get("/pocket-ask.js", s.core.AskWidget("music"))
	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/api/health", s.handleHealth)
	if s.cfg.MetricsEnabled {
		r.Get("/metrics", s.handleMetrics)
	}

	// Subsonic apps sign in here with a Pocket Music app password (gateway.go)
	r.HandleFunc("/rest/*", s.handleGateway)

	r.Route("/api/v1", func(r chi.Router) {
		// long-lived: audio, covers and live events must not hit the request timeout
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/stream/{id}", s.handleStream)
			r.Get("/download/{id}", s.handleDownload)
			r.Get("/cover/{id}", s.handleCover)
			r.Get("/events", s.handleEvents)
		})

		r.Group(func(r chi.Router) {
			r.Use(middleware.Compress(5))
			r.Use(middleware.Timeout(120 * time.Second))
			r.Post("/auth/login", s.handleLogin)
			r.Post("/auth/setup", s.handleSetup)
			r.Get("/auth/status", s.handleAuthStatus)
			r.Get("/auth/oidc/start", s.handleOIDCStart)
			r.Get("/auth/oidc/callback", s.handleOIDCCallback)

			r.Group(func(r chi.Router) {
				r.Use(s.requireAuth)
				r.Post("/auth/logout", s.handleLogout)
				r.Get("/auth/me", s.handleMe)
				r.Put("/auth/me", s.handleUpdateMe)

				r.Get("/connection", s.handleConnection)
				r.Put("/connection", s.handleConnect)
				r.Get("/sync", s.handleSyncStatus)
				r.Post("/sync", s.handleSyncNow)

				r.Get("/home", s.handleHome)
				r.Get("/dashboard", s.handleDashboard)
				r.Get("/day/{date}", s.handleDay)

				r.Get("/albums", s.handleAlbums)
				r.Get("/albums/{id}", s.handleAlbum)
				r.Get("/artists", s.handleArtists)
				r.Get("/artists/{id}", s.handleArtist)
				r.Get("/artists/{id}/discography", s.handleDiscography)
				r.Get("/songs", s.handleSongs)
				r.Post("/songs/by-id", s.handleSongsByID)
				r.Get("/songs/{id}", s.handleSong)
				r.Get("/genres", s.handleGenres)
				r.Get("/search", s.handleSearch)
				r.Get("/lyrics/{id}", s.handleLyrics)
				r.Post("/lyrics/{id}/save", s.handleLyricsSave)
				r.Get("/lyrics-library", s.handleLyricsLibrary)
				r.Post("/lyrics-library/run", s.handleLyricsLibraryRun)
				r.Post("/lyrics-library/stop", s.handleLyricsLibraryStop)
				r.Get("/lyrics-index", s.handleLyricsIndex)
				r.Post("/lyrics-index/refresh", s.handleLyricsIndexRefresh)
				r.Post("/lyrics-index/fetch-missing", s.handleLyricsFetchMissing)
				r.Get("/lyrics-files/by-song/{songId}", s.handleLyricsFileBySong)
				r.Get("/lyrics-files/{id}", s.handleLyricsFile)
				r.Put("/lyrics-files/{id}", s.handleLyricsFileSave)
				r.Delete("/lyrics-files/{id}", s.handleLyricsFileDelete)
				r.Post("/lyrics-files/{id}/fetch", s.handleLyricsFileFetch)
				r.Get("/lyrics-files/{id}/candidates", s.handleLyricsCandidates)
				r.Post("/star", s.handleStar)
				r.Post("/rate", s.handleRate)

				r.Get("/playlists", s.handlePlaylists)
				r.Post("/playlists", s.handleCreatePlaylist)
				r.Get("/playlists/{id}", s.handlePlaylist)
				r.Put("/playlists/{id}", s.handleUpdatePlaylist)
				r.Delete("/playlists/{id}", s.handleDeletePlaylist)
				r.Get("/smart", s.handleSmartList)
				r.Post("/smart", s.handleSmartSave)
				r.Post("/smart/preview", s.handleSmartPreview)
				r.Get("/smart/{id}", s.handleSmartGet)
				r.Put("/smart/{id}", s.handleSmartSave)
				r.Post("/smart/{id}/refresh", s.handleSmartRefresh)
				r.Delete("/smart/{id}", s.handleSmartDelete)

				r.Get("/mixes", s.handleMixes)
				r.Get("/mixes/{key}", s.handleMix)
				r.Post("/mix", s.handleMakeMix)
				r.Get("/radio", s.handleRadio)
				r.Get("/shuffle", s.handleShuffle)

				r.Post("/listens", s.handleAddListen)
				r.Delete("/listens/{id}", s.handleDeleteListen)
				r.Get("/history", s.handleHistory)
				r.Get("/history/export", s.handleExportListens)
				r.Get("/stats", s.handleStats)
				r.Get("/recap", s.handleRecap)
				r.Get("/imports", s.handleImports)
				r.Post("/imports", s.handleStartImport)

				r.Post("/now", s.handleNow)
				r.Get("/devices", s.handleDevices)
				r.Get("/devices/{id}/queue", s.handleDeviceQueue)
				r.Post("/devices/{id}/command", s.handleDeviceCommand)
				r.Delete("/devices/{id}", s.handleDeleteDevice)
				r.Get("/server/now-playing", s.handleServerNowPlaying)

				r.Get("/subsonic", s.handleGatewayInfo)
				r.Post("/subsonic/password", s.handleGatewayPassword)
				r.Delete("/subsonic", s.handleGatewayDelete)

				r.Get("/lidarr/status", s.handleLidarrStatus)
				r.Get("/lidarr/search", s.handleLidarrSearch)
				r.Get("/releases", s.handleReleases)
				r.Get("/requests", s.handleRequests)
				r.Post("/requests", s.handleRequestAlbum)
				r.Delete("/requests/{id}", s.handleDeleteRequest)

				r.Get("/ai/status", s.handleAIStatus)
				r.Get("/ai/models", s.handleAIModels)
				r.Post("/integrations/test", s.handleIntegrationTest)

				r.Get("/users", s.handleListUsers)
				r.Post("/users", s.handleCreateUser)
				r.Put("/users/{id}", s.handleUpdateUser)
				r.Delete("/users/{id}", s.handleDeleteUser)

				r.Get("/settings", s.handleGetSettings)
				r.Put("/settings", s.handlePutSettings)
				r.Get("/settings/system", s.handleSystemInfo)

				r.Get("/tokens", s.handleListTokens)
				r.Post("/tokens", s.handleCreateToken)
				r.Delete("/tokens/{id}", s.handleDeleteToken)

				r.Get("/backup", s.handleBackup)
				r.Get("/export.json", s.handleExportJSON)
				r.Post("/import", s.handleImportJSON)
			})
		})
	})

	r.NotFound(s.handleSPA)
	return r
}

// ---------- helpers ----------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.requests.Add(1)
		if ww.Status() >= 500 {
			s.errors.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String(), "ip", r.RemoteAddr)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func (s *Server) fail(w http.ResponseWriter, err error, context string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, store.ErrConflict), errors.Is(err, core.ErrOperationConflict):
		writeErr(w, http.StatusConflict, "already exists")
		return
	}
	s.log.Error(context, "err", err)
	writeErr(w, http.StatusInternalServerError, context+": "+err.Error())
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	return dec.Decode(v)
}

// decodeKeys decodes JSON into v and also reports which top-level keys were
// present, so PUT handlers can tell "not sent" from "sent empty".
func decodeKeys(r *http.Request, v any) (map[string]bool, error) {
	b, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(raw))
	for k := range raw {
		keys[k] = true
	}
	return keys, json.Unmarshal(b, v)
}

func idParam(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

func validDate(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// ---------- health / metrics ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"core_outbox": s.coreOutboxHealth(r.Context()), "status": "ok", "version": Version, "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := s.store.Counts(ctx)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP pocket_music_info Build info.\n# TYPE pocket_music_info gauge\npocket_music_info{version=%q} 1\n", Version)
	fmt.Fprintf(w, "# HELP pocket_music_uptime_seconds Process uptime.\n# TYPE pocket_music_uptime_seconds gauge\npocket_music_uptime_seconds %d\n", int(time.Since(s.started).Seconds()))
	fmt.Fprintf(w, "# HELP pocket_music_http_requests_total HTTP requests served.\n# TYPE pocket_music_http_requests_total counter\npocket_music_http_requests_total %d\n", s.requests.Load())
	fmt.Fprintf(w, "# HELP pocket_music_http_errors_total HTTP 5xx responses.\n# TYPE pocket_music_http_errors_total counter\npocket_music_http_errors_total %d\n", s.errors.Load())
	fmt.Fprintf(w, "# HELP pocket_music_rows Rows per table.\n# TYPE pocket_music_rows gauge\n")
	for k, v := range counts {
		fmt.Fprintf(w, "pocket_music_rows{table=%q} %d\n", k, v)
	}
}

// ---------- static / SPA ----------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.Error(w, "web UI not built (run `make web`)", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.webFS.Open(path); err == nil {
		f.Close()
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(s.webFS).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	r.URL.Path = "/"
	http.FileServerFS(s.webFS).ServeHTTP(w, r)
}
