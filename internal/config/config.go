// Package config loads runtime configuration from environment variables.
//
// Every option has a sensible default so that `docker run` with a single
// volume works out of the box. See docs/configuration.md for the full list.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings.
type Config struct {
	Host           string
	Port           int
	BaseURL        string
	DataDir        string
	DBPath         string
	FilesDir       string
	LogLevel       string
	LogFormat      string // text | json
	TrustProxy     bool
	MetricsEnabled bool

	MaxUploadBytes int64

	AuthMode             string // local | proxy | none
	AuthProxyHeader      string
	AuthProxyEmailHeader string
	SessionTTL           time.Duration
	SecureCookies        bool
	AdminEmail           string
	AdminPassword        string
	AdminName            string

	// Sign in with an OpenID Connect provider. On a box with Pocket Core none
	// of these are needed: the app uses the client Core issues on registration.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCName         string // button label
	OIDCAutoCreate   bool   // unknown emails get a member account

	// The music server (Navidrome or any OpenSubsonic server). Each person
	// signs in with their own account; with Pocket Core the account can come
	// from Core instead.
	NavidromeURL       string
	NavidromePublicURL string // what a browser opens ("Open in Navidrome")
	// The owner's account, copied once into the owner's own connection.
	NavidromeUser     string
	NavidromePassword string

	// The owner's other integrations, copied once into the owner's own
	// settings; the values saved under Settings win.
	NotifyURL     string
	NotifyFormat  string // apprise | ntfy | json
	LidarrURL     string
	LidarrKey     string
	MalojaURL     string
	OpenAIKey     string
	OpenAIModel   string
	OpenAIBaseURL string

	// Synced lyrics from LRCLIB when the music server has none ("" = off).
	LyricsURL string
	// Where the music library is mounted read-write (the same folder the music
	// server reads): the lyrics library writes .lrc files next to the songs.
	MusicDir string
	// How often each person's library index is checked for a new scan.
	SyncEvery time.Duration

	// Outbound lookups identify as this.
	UserAgent string

	// Pocket Core: when CORE_URL is set the app registers itself on start,
	// publishes events and accepts the tokens Core hands to paired phones.
	CoreURL             string
	CoreRegistrationKey string
	CoreAppURL          string // how Core reaches this app; defaults to BASE_URL

	DemoData bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envBool(key string, def bool) bool {
	switch strings.ToLower(env(key, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(key string, def int) int {
	if v := env(key, ""); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := env(key, ""); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := env(key, ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	dataDir := env("DATA_DIR", "./data")
	c := &Config{
		Host:           env("HOST", "0.0.0.0"),
		Port:           envInt("PORT", 8080),
		BaseURL:        strings.TrimRight(env("BASE_URL", ""), "/"),
		DataDir:        dataDir,
		DBPath:         env("DB_PATH", dataDir+"/pocket-music.db"),
		FilesDir:       env("FILES_DIR", dataDir+"/files"),
		LogLevel:       strings.ToLower(env("LOG_LEVEL", "info")),
		LogFormat:      strings.ToLower(env("LOG_FORMAT", "text")),
		TrustProxy:     envBool("TRUST_PROXY", true),
		MetricsEnabled: envBool("METRICS_ENABLED", true),

		MaxUploadBytes: envInt64("MAX_UPLOAD_BYTES", 20<<20),

		AuthMode:             strings.ToLower(env("AUTH_MODE", "local")),
		AuthProxyHeader:      env("AUTH_PROXY_HEADER", "Remote-User"),
		AuthProxyEmailHeader: env("AUTH_PROXY_EMAIL_HEADER", "Remote-Email"),
		SessionTTL:           envDuration("SESSION_TTL", 720*time.Hour),
		SecureCookies:        envBool("SECURE_COOKIES", false),
		AdminEmail:           strings.ToLower(env("ADMIN_EMAIL", "")),
		AdminPassword:        env("ADMIN_PASSWORD", ""),
		AdminName:            env("ADMIN_NAME", "Admin"),
		OIDCIssuer:           strings.TrimRight(env("OIDC_ISSUER", ""), "/"),
		OIDCClientID:         env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret:     env("OIDC_CLIENT_SECRET", ""),
		OIDCName:             env("OIDC_NAME", "single sign-on"),
		OIDCAutoCreate:       envBool("OIDC_AUTO_CREATE", true),

		NavidromeURL:       strings.TrimRight(env("NAVIDROME_URL", ""), "/"),
		NavidromePublicURL: strings.TrimRight(env("NAVIDROME_PUBLIC_URL", ""), "/"),
		NavidromeUser:      env("NAVIDROME_USER", ""),
		NavidromePassword:  env("NAVIDROME_PASSWORD", ""),

		NotifyURL:     env("NOTIFY_URL", ""),
		NotifyFormat:  strings.ToLower(env("NOTIFY_FORMAT", "")),
		LidarrURL:     strings.TrimRight(env("LIDARR_URL", ""), "/"),
		LidarrKey:     env("LIDARR_API_KEY", ""),
		MalojaURL:     strings.TrimRight(env("MALOJA_URL", ""), "/"),
		OpenAIKey:     env("OPENAI_API_KEY", ""),
		OpenAIModel:   env("OPENAI_MODEL", ""),
		OpenAIBaseURL: strings.TrimRight(env("OPENAI_BASE_URL", ""), "/"),
		LyricsURL:     strings.TrimRight(env("LYRICS_URL", "https://lrclib.net"), "/"),
		MusicDir:      strings.TrimRight(env("MUSIC_DIR", ""), "/"),
		SyncEvery:     envDuration("SYNC_EVERY", 10*time.Minute),

		UserAgent: env("USER_AGENT", "PocketMusic/1.0 (+https://github.com/abjelosevic88/pocket-homelab-music)"),

		CoreURL:             strings.TrimRight(env("CORE_URL", ""), "/"),
		CoreRegistrationKey: env("CORE_REGISTRATION_KEY", ""),
		CoreAppURL:          strings.TrimRight(env("CORE_APP_URL", ""), "/"),

		DemoData: envBool("DEMO_DATA", false),
	}
	if c.CoreAppURL == "" {
		c.CoreAppURL = c.BaseURL
	}
	switch c.AuthMode {
	case "local", "proxy", "none":
	default:
		return nil, fmt.Errorf("AUTH_MODE must be one of local, proxy, none (got %q)", c.AuthMode)
	}
	if c.Port <= 0 || c.Port > 65535 {
		return nil, fmt.Errorf("PORT out of range: %d", c.Port)
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || c.BaseURL == "") {
		return nil, fmt.Errorf("OIDC_ISSUER needs OIDC_CLIENT_ID and BASE_URL (the callback is BASE_URL/api/v1/auth/oidc/callback)")
	}
	if strings.EqualFold(c.LyricsURL, "off") {
		c.LyricsURL = ""
	}
	if c.SyncEvery < time.Minute {
		c.SyncEvery = time.Minute
	}
	return c, nil
}

// Addr returns host:port.
func (c *Config) Addr() string { return fmt.Sprintf("%s:%d", c.Host, c.Port) }
