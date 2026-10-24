package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-music/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-music/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-music/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with secrets masked.
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	lidarrMode := st.LidarrMode
	if lidarrMode == "" {
		lidarrMode = "core"
		if st.LidarrKey != "" || !s.core.Configured() {
			lidarrMode = "direct"
		}
	}
	return map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat,
		"quality": st.Quality, "format": st.Format, "replay_gain": st.ReplayGain, "crossfade": st.Crossfade,
		"scrobble": st.Scrobble, "scrobble_pct": st.ScrobblePct, "skip_ambient": st.SkipAmbient, "ambient_genres": st.AmbientGenres, "hide_genres": st.HideGenres,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_arrived": st.NotifyArrived, "notify_weekly": st.NotifyWeekly, "notify_configured": st.NotifyURL != "",
		"lidarr_mode": lidarrMode, "lidarr_url": st.LidarrURL, "lidarr_key": mask(st.LidarrKey),
		"maloja_url": st.MalojaURL, "maloja_default": s.cfg.MalojaURL, "listenbrainz_user": st.ListenBrainzUser, "listenbrainz_token": mask(st.ListenBrainzToken),
		"lyrics_online": st.LyricsOnline, "lyrics_available": s.cfg.LyricsURL != "",
		"lyrics_prefer": st.LyricsPrefer, "lyrics_size": st.LyricsSize, "lyrics_auto": st.LyricsAuto, "mixes_to_server": st.MixesToServer,
		"lyrics_writer": st.LyricsWriter, "lyrics_hour": st.LyricsHour, "lyrics_retry_days": st.LyricsRetryDays, "lyrics_plain": st.LyricsPlain,
		"lyrics_delay_ms": st.LyricsDelayMS, "lyrics_order": st.LyricsOrder, "lyrics_notify": st.LyricsNotify, "lyrics_rescan": st.LyricsRescan,
		"lyrics_library": s.cfg.MusicDir != "",
		"ai_provider":    st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL, "navidrome_url": s.cfg.NavidromeURL != "", "navidrome_public_url": s.cfg.NavidromePublicURL,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin and silently ignored for everyone else.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	var in store.Settings
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	oneOf := func(k string, dst *string, v string, allowed ...string) {
		if !keys[k] {
			return
		}
		for _, a := range allowed {
			if v == a {
				*dst = v
				return
			}
		}
	}
	intIn := func(k string, dst *int, v, lo, hi int) {
		if keys[k] && v >= lo && v <= hi {
			*dst = v
		}
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	text("date_format", &cur.DateFormat, in.DateFormat)
	text("time_format", &cur.TimeFormat, in.TimeFormat)
	oneOf("quality", &cur.Quality, in.Quality, "auto", "original", "320", "256", "192", "128", "96")
	oneOf("format", &cur.Format, in.Format, "mp3", "aac", "opus")
	oneOf("replay_gain", &cur.ReplayGain, in.ReplayGain, "off", "track", "album", "auto")
	intIn("crossfade", &cur.Crossfade, in.Crossfade, 0, 12)
	boolIn("scrobble", &cur.Scrobble, in.Scrobble)
	intIn("scrobble_pct", &cur.ScrobblePct, in.ScrobblePct, 10, 100)
	boolIn("skip_ambient", &cur.SkipAmbient, in.SkipAmbient)
	text("ambient_genres", &cur.AmbientGenres, in.AmbientGenres)
	text("hide_genres", &cur.HideGenres, in.HideGenres)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	boolIn("notify_arrived", &cur.NotifyArrived, in.NotifyArrived)
	boolIn("notify_weekly", &cur.NotifyWeekly, in.NotifyWeekly)
	oneOf("lidarr_mode", &cur.LidarrMode, in.LidarrMode, "core", "direct", "off")
	url("lidarr_url", &cur.LidarrURL, in.LidarrURL)
	secret("lidarr_key", &cur.LidarrKey, in.LidarrKey)
	url("maloja_url", &cur.MalojaURL, in.MalojaURL)
	text("listenbrainz_user", &cur.ListenBrainzUser, in.ListenBrainzUser)
	secret("listenbrainz_token", &cur.ListenBrainzToken, in.ListenBrainzToken)
	boolIn("lyrics_online", &cur.LyricsOnline, in.LyricsOnline)
	oneOf("lyrics_prefer", &cur.LyricsPrefer, in.LyricsPrefer, "synced", "library")
	oneOf("lyrics_size", &cur.LyricsSize, in.LyricsSize, "s", "m", "l")
	boolIn("lyrics_auto", &cur.LyricsAuto, in.LyricsAuto)
	mixesWas := cur.MixesToServer
	boolIn("mixes_to_server", &cur.MixesToServer, in.MixesToServer)
	// the lyrics library is box-wide: admins only, silently ignored for everyone else
	boxChanged := false
	if isAdmin {
		before := cur
		boolIn("lyrics_writer", &cur.LyricsWriter, in.LyricsWriter)
		intIn("lyrics_hour", &cur.LyricsHour, in.LyricsHour, 0, 23)
		intIn("lyrics_retry_days", &cur.LyricsRetryDays, in.LyricsRetryDays, 1, 365)
		oneOf("lyrics_plain", &cur.LyricsPlain, in.LyricsPlain, "tag", "lrc", "skip")
		intIn("lyrics_delay_ms", &cur.LyricsDelayMS, in.LyricsDelayMS, 0, 10000)
		oneOf("lyrics_order", &cur.LyricsOrder, in.LyricsOrder, "added", "path")
		boolIn("lyrics_notify", &cur.LyricsNotify, in.LyricsNotify)
		boolIn("lyrics_rescan", &cur.LyricsRescan, in.LyricsRescan)
		boxChanged = before != cur
	}
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			writeErr(w, http.StatusBadRequest, "AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			writeErr(w, http.StatusBadRequest, "unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	url("openai_base_url", &cur.OpenAIBaseURL, in.OpenAIBaseURL)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && (keys["title"] || boxChanged) {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	// genre choices change what mixes contain
	if keys["skip_ambient"] || keys["ambient_genres"] || keys["hide_genres"] {
		_ = s.store.CacheDelete(r.Context(), fmt.Sprintf("mixes:%d:", me.ID))
	}
	// mixes on the server: write them now (or take them away)
	if cur.MixesToServer != mixesWas {
		uid := me.ID
		_ = s.store.CacheDelete(r.Context(), fmt.Sprintf("mixpub:%d", uid))
		s.job(fmt.Sprintf("mixpub:%d", uid), func(ctx context.Context) {
			if err := s.publishMixes(ctx, uid); err != nil && !isNoMusic(err) {
				s.log.Warn("mixes to server", "user", uid, "err", err)
			}
		})
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	u := userFrom(r.Context())
	st, _ := s.store.GetSettings(r.Context(), u.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	switch in.Which {
	case "lidarr":
		var l *lidarr
		if in.Key != "" || in.URL != "" {
			l = &lidarr{base: urlOr(st.LidarrURL), key: keyOr(st.LidarrKey), via: "direct", http: &http.Client{Timeout: 15 * time.Second}}
		} else {
			var why string
			if l, why = s.lidarrFor(ctx, u, st); l == nil {
				err = fmt.Errorf("%s", why)
			}
		}
		if l != nil {
			var sys struct {
				Version string `json:"version"`
			}
			if err = l.do(ctx, http.MethodGet, "/system/status", nil, &sys); err == nil {
				var as []lidarrArtist
				_ = l.do(ctx, http.MethodGet, "/artist", nil, &as)
				how := "direct connection"
				if l.via == "core" {
					how = "through Pocket Core"
				}
				msg = fmt.Sprintf("Lidarr %s, %s, %d artists", sys.Version, how, len(as))
			}
		}
	case "maloja":
		base := urlOr(st.MalojaURL)
		var res struct {
			Amount int `json:"amount"`
		}
		if err = s.getJSON(ctx, base+"/apis/mlj_1/numscrobbles", nil, &res); err == nil {
			msg = fmt.Sprintf("Maloja answers: %d scrobbles to import", res.Amount)
		}
	case "listenbrainz":
		user := firstNonEmpty(strings.TrimSpace(in.URL), st.ListenBrainzUser)
		var res struct {
			Payload struct {
				Count int `json:"count"`
			} `json:"payload"`
		}
		if err = s.getJSON(ctx, firstNonEmpty(st.ListenBrainzURL, "https://api.listenbrainz.org")+"/1/user/"+user+"/listen-count", nil, &res); err == nil {
			msg = fmt.Sprintf("%s has %d listens on ListenBrainz", user, res.Payload.Count)
		}
	case "openai":
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	case "notify":
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Music", Body: "Notifications work. Arrived albums and your weekly recap will come here.", Priority: "info", Tags: []string{"musical_note"}})
		msg = "Test message sent via " + n.Format
	default:
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())), "navidrome_url": s.cfg.NavidromeURL,
	})
}

// handleBackup downloads the whole database, so it is a box operation for an
// admin only (everyone can export their own data as JSON).
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-music-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's own data: plays and smart
// playlists (the library itself lives on the music server).
type exportDoc struct {
	App      string                `json:"app"`
	Version  string                `json:"version"`
	Exported string                `json:"exported_at"`
	Listens  []store.Listen        `json:"listens"`
	Smart    []store.SmartPlaylist `json:"smart_playlists"`
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	doc := exportDoc{App: "pocket-music", Version: Version, Exported: store.Now()}
	doc.Listens, _ = s.store.Listens(ctx, uid, "", "", 1000000, "")
	doc.Smart, _ = s.store.ListSmart(ctx, uid)
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-music-export.json"`)
	writeJSON(w, http.StatusOK, doc)
}

// handleImportJSON adds an export to the caller's own data (plays already there are skipped).
func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	var doc exportDoc
	if err := decode(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx := r.Context()
	uid := actorID(ctx)
	counts := map[string]int{}
	n, err := s.store.AddListens(ctx, uid, doc.Listens)
	if err != nil {
		s.fail(w, err, "import plays")
		return
	}
	counts["listens"] = n
	have := map[string]bool{}
	mine, _ := s.store.ListSmart(ctx, uid)
	for _, p := range mine {
		have[strings.ToLower(p.Name)] = true
	}
	for _, p := range doc.Smart {
		if have[strings.ToLower(p.Name)] {
			continue
		}
		p.ID, p.PlaylistID = 0, ""
		if validSmart(&p) == nil && s.store.SaveSmart(ctx, uid, &p) == nil {
			counts["smart_playlists"]++
		}
	}
	linked, _ := s.store.RelinkListens(ctx, uid)
	counts["matched"] = linked
	writeJSON(w, http.StatusOK, counts)
}
