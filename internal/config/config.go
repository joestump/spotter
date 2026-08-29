package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// PlaylistSyncConfig holds settings for syncing playlists to Navidrome.
type PlaylistSyncConfig struct {
	// SyncInterval is how often to sync playlists to Navidrome (e.g., "1h", "30m", "6h")
	SyncInterval string `mapstructure:"sync_interval"`
	// DeleteOnUnsync determines whether to delete Navidrome playlist when sync is disabled
	DeleteOnUnsync bool `mapstructure:"delete_on_unsync"`
	// MinMatchConfidence is the minimum confidence for track matching (0.0-1.0)
	MinMatchConfidence float64 `mapstructure:"min_match_confidence"`
	// IncludeUnmatchedTracks determines whether to include unmatched tracks as placeholders
	IncludeUnmatchedTracks bool `mapstructure:"include_unmatched_tracks"`
}

// VibesConfig holds settings for the Vibes mixtape generation system.
type VibesConfig struct {
	// DefaultMaxTracks is the default maximum number of tracks to generate in a mixtape
	DefaultMaxTracks int `mapstructure:"default_max_tracks"`
	// MinTracks is the minimum number of tracks for a valid mixtape
	MinTracks int `mapstructure:"min_tracks"`
	// MaxTracks is the maximum allowed tracks (hard limit)
	MaxTracks int `mapstructure:"max_tracks"`
	// HistoryDays is how many days of listening history to include in context
	HistoryDays int `mapstructure:"history_days"`
	// MaxHistoryTracks is the maximum number of history tracks to include in prompt
	MaxHistoryTracks int `mapstructure:"max_history_tracks"`
	// Model is the AI model to use for mixtape generation (overrides openai.model if set)
	Model string `mapstructure:"model"`
	// Temperature is the AI temperature setting for generation (0.0-2.0)
	Temperature float64 `mapstructure:"temperature"`
	// MaxTokens is the maximum tokens for the AI response
	MaxTokens int `mapstructure:"max_tokens"`
	// TimeoutSeconds is the timeout for AI generation requests
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
	// PromptsDirectory is the directory containing prompt templates
	PromptsDirectory string `mapstructure:"prompts_directory"`
	// MinMatchConfidence is the minimum confidence for matching AI-suggested tracks to library
	MinMatchConfidence float64 `mapstructure:"min_match_confidence"`
}

// LastFMConfig holds Last.fm API credentials and redirect URL.
type LastFMConfig struct {
	APIKey       string `mapstructure:"api_key"`
	SharedSecret string `mapstructure:"shared_secret"`
	RedirectURL  string `mapstructure:"redirect_url"`
}

// Governing: SPEC user-authentication REQ "Config LogValue Sanitization"
// LogValue redacts sensitive fields when logging LastFMConfig via slog.
func (c LastFMConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("api_key", "[REDACTED]"),
		slog.String("shared_secret", "[REDACTED]"),
	)
}

// Governing: SPEC-0015 REQ "SMTP Configuration", ADR-0026
// SMTPConfig holds SMTP server configuration for email notifications.
type SMTPConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	From     string `mapstructure:"from"`
	TLS      bool   `mapstructure:"tls"`
}

// Governing: SPEC-0015 REQ "SMTP Configuration", ADR-0026
// LogValue redacts sensitive fields when logging SMTPConfig via slog.
func (c SMTPConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", c.Host),
		slog.Int("port", c.Port),
		slog.String("username", "[REDACTED]"),
		slog.String("password", "[REDACTED]"),
		slog.String("from", c.From),
		slog.Bool("tls", c.TLS),
	)
}

// Governing: SPEC-0015 REQ "SMTP Configuration", ADR-0026
// NotificationsConfig holds notification behavior settings.
type NotificationsConfig struct {
	FailureCooldownDays int `mapstructure:"failure_cooldown_days"`
}

// Governing: ADR-0019 (structured metrics), ADR-0010 (slog), SPEC observability REQ "FMT-001", REQ "FMT-002"
type Config struct {
	Log struct {
		Format string `mapstructure:"format"` // Log format: "json" or "text" (default: "text")
	} `mapstructure:"log"`
	Security struct {
		EncryptionKey string `mapstructure:"encryption_key"`  // 32-byte hex key for AES-256 encryption
		SecureCookies bool   `mapstructure:"secure_cookies"`  // Set Secure flag on cookies (requires HTTPS)
		JWTSecret     string `mapstructure:"jwt_secret"`      // 32+ character secret for JWT signing
		AuthRateLimit int    `mapstructure:"auth_rate_limit"` // Login attempts per minute per IP (default: 10)
	} `mapstructure:"security"`
	Database struct {
		Driver string `mapstructure:"driver"`
		Source string `mapstructure:"source"`
	} `mapstructure:"database"`
	Server struct {
		Port              string `mapstructure:"port"`
		Host              string `mapstructure:"host"`
		BaseURL           string `mapstructure:"base_url"`            // Governing: ADR-0026, SPEC-0015 (public base URL used in sync-failure email links)
		ReadHeaderTimeout string `mapstructure:"read_header_timeout"` // Duration string for read header timeout (default: "10s")
		ReadTimeout       string `mapstructure:"read_timeout"`        // Duration string for read timeout (default: "30s")
		WriteTimeout      string `mapstructure:"write_timeout"`       // Duration string for write timeout (default: "60s")
		IdleTimeout       string `mapstructure:"idle_timeout"`        // Duration string for idle timeout (default: "120s")
	} `mapstructure:"server"`
	// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-TMO-005 (configurable shutdown timeout)
	// ShutdownTimeout is the graceful shutdown budget (SPOTTER_SHUTDOWN_TIMEOUT, default "30s").
	// CONSTRAINT: this key is deliberately top-level (not namespaced under server.*)
	// because the env var name SPOTTER_SHUTDOWN_TIMEOUT predates config consolidation
	// (SPEC graceful-shutdown REQ-TMO-005 names it) and must keep working; with the
	// dot-to-underscore replacer, only a top-level "shutdown_timeout" key maps to it.
	ShutdownTimeout string `mapstructure:"shutdown_timeout"`
	// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-SEM-002 (configurable semaphore capacity)
	// MaxConcurrentJobs bounds concurrent per-user background jobs (SPOTTER_MAX_CONCURRENT_JOBS, default 10).
	// CONSTRAINT: top-level for the same reason as ShutdownTimeout — the published
	// env var name SPOTTER_MAX_CONCURRENT_JOBS (SPEC graceful-shutdown REQ-SEM-002)
	// must remain stable.
	MaxConcurrentJobs int `mapstructure:"max_concurrent_jobs"`
	Navidrome         struct {
		BaseURL string `mapstructure:"base_url"`
	} `mapstructure:"navidrome"`
	// Governing: SPEC-0017 REQ "Configuration", ADR-0029
	Lidarr struct {
		BaseURL        string `mapstructure:"base_url"`
		APIKey         string `mapstructure:"api_key"`
		QueueMax       int    `mapstructure:"queue_max"`       // Maximum Lidarr queue depth before backpressure (default: 50)
		SubmitInterval string `mapstructure:"submit_interval"` // How often to wake the submitter (default: "3m")
	} `mapstructure:"lidarr"`
	Sync struct {
		Interval string `mapstructure:"interval"`
		// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (configurable initial history lookback)
		// HistoryLookback is the initial history window for users with no prior listens (default: "720h" = 30 days).
		HistoryLookback string `mapstructure:"history_lookback"`
		// ProviderLookback overrides HistoryLookback per listen provider, keyed by
		// provider type (e.g. "lastfm"). A value of "0" or "unlimited" removes the
		// bound entirely so that provider syncs its full available history.
		// YAML-only: viper env overrides cannot populate nested maps.
		ProviderLookback map[string]string `mapstructure:"provider_lookback"`
	} `mapstructure:"sync"`
	Theme struct {
		Available string `mapstructure:"available"` // Comma-separated list of DaisyUI theme names
		Default   string `mapstructure:"default"`   // Default theme name
	} `mapstructure:"theme"`
	Spotify struct {
		ClientID     string `mapstructure:"client_id"`
		ClientSecret string `mapstructure:"client_secret"`
		RedirectURL  string `mapstructure:"redirect_url"`
	} `mapstructure:"spotify"`
	LastFM LastFMConfig `mapstructure:"lastfm"`
	OpenAI struct {
		APIKey  string `mapstructure:"api_key"`  // OpenAI API key (required for AI enrichment)
		BaseURL string `mapstructure:"base_url"` // Base URL for API (for LiteLLM or compatible proxies)
		Model   string `mapstructure:"model"`    // Model to use for enrichment (e.g., gpt-4o, gpt-4-turbo)
	} `mapstructure:"openai"`
	SMTP          SMTPConfig          `mapstructure:"smtp"`
	Notifications NotificationsConfig `mapstructure:"notifications"`
	PlaylistSync  PlaylistSyncConfig  `mapstructure:"playlist_sync"`
	Vibes         VibesConfig         `mapstructure:"vibes"`
	Metadata      struct {
		Enabled  bool   `mapstructure:"enabled"`  // Enable/disable metadata enrichment
		Interval string `mapstructure:"interval"` // Sync interval (e.g., "1h", "30m")
		Order    string `mapstructure:"order"`    // Comma-separated enricher order (e.g., "musicbrainz,navidrome,spotify,lastfm,fanart,openai")

		MusicBrainz struct {
			UserAgent string `mapstructure:"user_agent"` // Required by MusicBrainz API - should include app name and contact
		} `mapstructure:"musicbrainz"`

		Fanart struct {
			APIKey string `mapstructure:"api_key"` // Fanart.tv personal API key
		} `mapstructure:"fanart"`

		Images struct {
			Download  bool   `mapstructure:"download"`   // Whether to download images locally
			Directory string `mapstructure:"directory"`  // Directory to store downloaded images
			MaxWidth  int    `mapstructure:"max_width"`  // Maximum image width (for resizing)
			MaxHeight int    `mapstructure:"max_height"` // Maximum image height (for resizing)
		} `mapstructure:"images"`

		AI struct {
			PromptsDirectory string `mapstructure:"prompts_directory"` // Directory containing prompt templates
		} `mapstructure:"ai"`
	} `mapstructure:"metadata"`
}

// AvailableThemes returns the list of available themes parsed from the comma-separated config.
func (c *Config) AvailableThemes() []string {
	if c.Theme.Available == "" {
		return []string{"dark"}
	}
	themes := strings.Split(c.Theme.Available, ",")
	result := make([]string, 0, len(themes))
	for _, t := range themes {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}

// MetadataEnricherOrder returns the list of enrichers in the configured order.
// Falls back to default order if not configured.
// Governing: ADR-0015 (pluggable enricher registry), ADR-0009 (Lidarr optional —
// the lidarr enricher is filtered out when Lidarr is unconfigured).
func (c *Config) MetadataEnricherOrder() []string {
	var parts []string
	if c.Metadata.Order == "" {
		parts = []string{"musicbrainz", "lidarr", "navidrome", "spotify", "lastfm", "fanart", "openai"}
	} else {
		parts = strings.Split(c.Metadata.Order, ",")
	}
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p == "" {
			continue
		}
		if p == "lidarr" && !c.IsLidarrEnabled() {
			continue
		}
		result = append(result, p)
	}
	if len(result) == 0 && len(parts) > 0 {
		// Filtering unconfigured enrichers emptied the order entirely — metadata
		// enrichment will be a silent no-op, which is almost certainly not intended.
		slog.Warn("metadata enricher order is empty after filtering unconfigured enrichers; metadata enrichment will do nothing",
			"configured_order", c.Metadata.Order)
	}
	return result
}

// IsOpenAIEnabled returns true if OpenAI API key is configured.
func (c *Config) IsOpenAIEnabled() bool {
	return c.OpenAI.APIKey != ""
}

// Governing: ADR-0009 (Viper configuration), SPEC-0017 REQ "Background Submitter Goroutine" (submitter only starts if Lidarr is configured)
// IsLidarrEnabled returns true if Lidarr is fully configured (base URL and API key).
// Lidarr is optional: when unconfigured, the Lidarr enricher and submitter are skipped.
func (c *Config) IsLidarrEnabled() bool {
	return c.Lidarr.BaseURL != "" && c.Lidarr.APIKey != ""
}

// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-TMO-001, REQ-TMO-005
// GetShutdownTimeout returns the graceful shutdown budget, falling back to 30s
// when unset, unparsable, or non-positive (a zero or negative budget would mean
// zero-grace shutdown).
func (c *Config) GetShutdownTimeout() time.Duration {
	if c.ShutdownTimeout != "" {
		if d, err := time.ParseDuration(c.ShutdownTimeout); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-SEM-002
// GetMaxConcurrentJobs returns the background job semaphore capacity, falling
// back to 10 when unset or non-positive.
func (c *Config) GetMaxConcurrentJobs() int {
	if c.MaxConcurrentJobs > 0 {
		return c.MaxConcurrentJobs
	}
	return 10
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (configurable initial history lookback)
// GetSyncHistoryLookback returns the initial history window for users with no
// prior listens, falling back to 720h (30 days) when unset, unparsable, or
// non-positive (a negative lookback would put the since watermark in the
// future and the first sync would fetch nothing).
func (c *Config) GetSyncHistoryLookback() time.Duration {
	if c.Sync.HistoryLookback != "" {
		if d, err := time.ParseDuration(c.Sync.HistoryLookback); err == nil && d > 0 {
			return d
		}
	}
	return 720 * time.Hour
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (per-provider lookback override)
// HistoryLookbackFor returns the first-sync lookback for a specific listen
// provider. The per-provider map (sync.provider_lookback) wins over the global
// sync.history_lookback. A per-provider value of "0" or "unlimited" reports
// unlimited=true, meaning the provider should sync its full available history.
func (c *Config) HistoryLookbackFor(provider string) (lookback time.Duration, unlimited bool) {
	if v, ok := c.Sync.ProviderLookback[provider]; ok {
		trimmed := strings.ToLower(strings.TrimSpace(v))
		if trimmed == "0" || trimmed == "unlimited" {
			return 0, true
		}
		if d, err := time.ParseDuration(trimmed); err == nil && d > 0 {
			return d, false
		}
	}
	return c.GetSyncHistoryLookback(), false
}

// GetVibesModel returns the model to use for vibes generation.
// Falls back to the general OpenAI model if not specifically configured.
func (c *Config) GetVibesModel() string {
	if c.Vibes.Model != "" {
		return c.Vibes.Model
	}
	if c.OpenAI.Model != "" {
		return c.OpenAI.Model
	}
	return "gpt-4o"
}

// GetVibesPromptsDirectory returns the directory for vibes prompt templates.
// Falls back to the metadata AI prompts directory, then to default.
func (c *Config) GetVibesPromptsDirectory() string {
	if c.Vibes.PromptsDirectory != "" {
		return c.Vibes.PromptsDirectory
	}
	if c.Metadata.AI.PromptsDirectory != "" {
		return c.Metadata.AI.PromptsDirectory
	}
	return "./data/prompts"
}

// GetEncryptionKeyBytes returns the encryption key as a 32-byte array.
// The key is stored as a 64-character hex string in config.
func (c *Config) GetEncryptionKeyBytes() ([]byte, error) {
	if len(c.Security.EncryptionKey) != 64 {
		return nil, fmt.Errorf("invalid encryption key length")
	}

	key := make([]byte, 32)
	for i := 0; i < 32; i++ {
		var b byte
		_, err := fmt.Sscanf(c.Security.EncryptionKey[i*2:i*2+2], "%02x", &b)
		if err != nil {
			return nil, fmt.Errorf("invalid hex in encryption key: %w", err)
		}
		key[i] = b
	}
	return key, nil
}

// newViper creates the Viper instance shared by Load and LoadDatabase:
// SPOTTER_* prefix, dot-to-underscore key replacer, automatic env binding,
// and every default registered (Viper only picks up env vars during Unmarshal
// for keys that have a registered default).
// Governing: ADR-0009 (Viper configuration)
func newViper() *viper.Viper {
	v := viper.New()

	v.SetEnvPrefix("SPOTTER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Defaults
	v.SetDefault("log.format", "text")            // Log format: "json" or "text"
	v.SetDefault("security.encryption_key", "")   // Must be set via environment variable
	v.SetDefault("security.secure_cookies", true) // Secure cookies by default (requires HTTPS)
	v.SetDefault("security.jwt_secret", "")       // Must be set via environment variable
	v.SetDefault("security.auth_rate_limit", 10)  // Login attempts per minute per IP
	v.SetDefault("server.port", "8080")
	v.SetDefault("server.host", "0.0.0.0")
	// Governing: ADR-0026, SPEC-0015 (public base URL used in sync-failure email links)
	v.SetDefault("server.base_url", "")
	v.SetDefault("server.read_header_timeout", "10s")
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "60s")
	v.SetDefault("server.idle_timeout", "120s")
	// Governing: SPEC graceful-shutdown REQ-TMO-005, REQ-SEM-002 (moved from raw os.Getenv per ADR-0009)
	v.SetDefault("shutdown_timeout", "30s")
	v.SetDefault("max_concurrent_jobs", 10)
	v.SetDefault("sync.interval", "5m")
	// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (configurable initial history lookback, 30 days)
	v.SetDefault("sync.history_lookback", "720h")
	v.SetDefault("theme.available", "spotter,night,synthwave,dracula,dark,light")
	v.SetDefault("theme.default", "spotter")
	v.SetDefault("database.driver", "sqlite3")
	v.SetDefault("database.source", "file:spotter.db?cache=shared&_fk=1")

	// Set defaults for keys to ensure Viper picks up environment variables
	v.SetDefault("navidrome.base_url", "")
	v.SetDefault("lidarr.base_url", "")
	v.SetDefault("lidarr.api_key", "")
	// Governing: SPEC-0017 REQ "Configuration", ADR-0029
	v.SetDefault("lidarr.queue_max", 50)
	v.SetDefault("lidarr.submit_interval", "3m")
	v.SetDefault("spotify.client_id", "")
	v.SetDefault("spotify.client_secret", "")
	v.SetDefault("spotify.redirect_url", "http://127.0.0.1:8080/auth/spotify/callback")
	v.SetDefault("lastfm.api_key", "")
	v.SetDefault("lastfm.shared_secret", "")
	v.SetDefault("lastfm.redirect_url", "http://127.0.0.1:8080/auth/lastfm/callback")

	// OpenAI defaults
	v.SetDefault("openai.api_key", "")
	v.SetDefault("openai.base_url", "https://api.openai.com/v1")
	v.SetDefault("openai.model", "gpt-4o")

	// Governing: SPEC-0015 REQ "SMTP Configuration", ADR-0026
	// SMTP defaults
	v.SetDefault("smtp.host", "")
	v.SetDefault("smtp.port", 587)
	v.SetDefault("smtp.username", "")
	v.SetDefault("smtp.password", "")
	v.SetDefault("smtp.from", "")
	v.SetDefault("smtp.tls", true)

	// Notification defaults
	v.SetDefault("notifications.failure_cooldown_days", 7)

	// Playlist sync defaults
	v.SetDefault("playlist_sync.sync_interval", "1h")
	v.SetDefault("playlist_sync.delete_on_unsync", false)
	// Governing: ADR-0014, SPEC playlist-sync-navidrome REQ-PLSYNC-003 (default 0.7; issue #330 aligned config with the ADR)
	v.SetDefault("playlist_sync.min_match_confidence", 0.7)
	v.SetDefault("playlist_sync.include_unmatched_tracks", false)

	// Vibes (mixtape generation) defaults
	v.SetDefault("vibes.default_max_tracks", 25)
	v.SetDefault("vibes.min_tracks", 5)
	v.SetDefault("vibes.max_tracks", 100)
	v.SetDefault("vibes.history_days", 30)
	v.SetDefault("vibes.max_history_tracks", 50)
	v.SetDefault("vibes.model", "")                 // Falls back to openai.model
	v.SetDefault("vibes.temperature", 0.8)          // Slightly creative
	v.SetDefault("vibes.max_tokens", 4000)          // Enough for track list + explanations
	v.SetDefault("vibes.timeout_seconds", 120)      // 2 minutes
	v.SetDefault("vibes.prompts_directory", "")     // Falls back to metadata.ai.prompts_directory
	v.SetDefault("vibes.min_match_confidence", 0.7) // Same as playlist sync default (ADR-0014)

	// Metadata enrichment defaults
	v.SetDefault("metadata.enabled", true)
	v.SetDefault("metadata.interval", "1h")
	v.SetDefault("metadata.order", "musicbrainz,lidarr,navidrome,spotify,lastfm,fanart,openai")
	v.SetDefault("metadata.musicbrainz.user_agent", "Spotter/1.0.0 (https://github.com/spotter)")
	v.SetDefault("metadata.fanart.api_key", "")
	v.SetDefault("metadata.images.download", true)
	v.SetDefault("metadata.images.directory", "./data/images")
	v.SetDefault("metadata.images.max_width", 1000)
	v.SetDefault("metadata.images.max_height", 1000)
	v.SetDefault("metadata.ai.prompts_directory", "./data/prompts")

	return v
}

// validateAndDefaultDatabase validates the database driver and applies the
// driver-specific default DSN when the source is unset (or still the sqlite
// default from Viper). Shared by Load and LoadDatabase.
// Governing: SPEC-0014 REQ "Driver Validation", REQ "Driver-Specific Default Source", ADR-0023
func validateAndDefaultDatabase(cfg *Config) error {
	validDrivers := map[string]bool{"sqlite3": true, "postgres": true, "mysql": true}
	if !validDrivers[cfg.Database.Driver] {
		return fmt.Errorf("unsupported database driver %q: must be one of sqlite3, postgres, mysql", cfg.Database.Driver)
	}

	const sqliteDefault = "file:spotter.db?cache=shared&_fk=1"
	if cfg.Database.Driver == "postgres" && (cfg.Database.Source == "" || cfg.Database.Source == sqliteDefault) {
		cfg.Database.Source = "host=localhost port=5432 dbname=spotter sslmode=disable"
	} else if cfg.Database.Driver == "mysql" && (cfg.Database.Source == "" || cfg.Database.Source == sqliteDefault) {
		cfg.Database.Source = "spotter:spotter@tcp(localhost:3306)/spotter?parseTime=true&charset=utf8mb4"
	}
	return nil
}

// LoadDatabase loads only the database-scoped configuration (driver + source)
// through the same Viper setup as Load — SPOTTER_DATABASE_DRIVER and
// SPOTTER_DATABASE_SOURCE with driver validation and driver-specific default
// DSNs — but skips full-server validation (Navidrome, OpenAI, security keys).
// Standalone tooling like `spotter-admin rotate-key` uses this so it can run
// with only database env vars set (SPEC key-rotation Scenario 1) while still
// honoring ADR-0009's "no hand-rolled os.Getenv" rule.
// Governing: ADR-0009 (Viper configuration), ADR-0023 (multi-database support), SPEC key-rotation
func LoadDatabase() (*Config, error) {
	v := newViper()

	var cfg Config
	cfg.Database.Driver = v.GetString("database.driver")
	cfg.Database.Source = v.GetString("database.source")

	// Viper treats empty-string env vars as "set", overriding defaults (see
	// ADR-0009 consequences); fall back to the sqlite defaults explicitly,
	// matching the pre-consolidation admin behavior.
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite3"
	}
	if cfg.Database.Source == "" && cfg.Database.Driver == "sqlite3" {
		cfg.Database.Source = "file:spotter.db?cache=shared&_fk=1"
	}

	if err := validateAndDefaultDatabase(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Load() (*Config, error) {
	v := newViper()

	// Governing: ADR-0009 (fail fast with clear error messages)
	// Pre-validate max_concurrent_jobs so a non-numeric SPOTTER_MAX_CONCURRENT_JOBS
	// produces a clear error instead of a raw mapstructure decode failure.
	if s := strings.TrimSpace(v.GetString("max_concurrent_jobs")); s != "" {
		if _, err := strconv.Atoi(s); err != nil {
			return nil, fmt.Errorf("max_concurrent_jobs must be an integer (set SPOTTER_MAX_CONCURRENT_JOBS), got %q", s)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// Governing: SPEC-0014 REQ "Driver Validation", REQ "Driver-Specific Default Source", ADR-0023
	if err := validateAndDefaultDatabase(&cfg); err != nil {
		return nil, err
	}

	// Governing: ADR-0026, SPEC-0015 (base URL is joined with paths in email links)
	// Normalize server.base_url: strip trailing slashes so consumers can safely
	// append absolute paths like /preferences/providers.
	cfg.Server.BaseURL = strings.TrimRight(cfg.Server.BaseURL, "/")

	// Apply defaults for OpenAI config when env vars are empty strings
	// (Viper treats empty string env vars as "set", overriding defaults)
	if cfg.OpenAI.BaseURL == "" {
		cfg.OpenAI.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.OpenAI.Model == "" {
		cfg.OpenAI.Model = "gpt-4o"
	}

	if cfg.Navidrome.BaseURL == "" {
		return nil, fmt.Errorf("navidrome.base_url is required")
	}

	// Governing: ADR-0009 (Viper configuration), SPEC-0014 (compose scenarios MUST start without Lidarr),
	// SPEC-0017 REQ "Background Submitter Goroutine" (submitter only starts if Lidarr is configured)
	// Lidarr is optional: validation only rejects partial configuration (one of the
	// two settings without the other), never the fully-unset case.
	if (cfg.Lidarr.BaseURL == "") != (cfg.Lidarr.APIKey == "") {
		return nil, fmt.Errorf("lidarr.base_url and lidarr.api_key must both be set to enable Lidarr (or both left unset to disable it)")
	}

	if cfg.OpenAI.APIKey == "" {
		return nil, fmt.Errorf("openai.api_key is required for AI enrichment")
	}

	// Validate encryption key
	if cfg.Security.EncryptionKey == "" {
		return nil, fmt.Errorf("security.encryption_key is required (set SPOTTER_SECURITY_ENCRYPTION_KEY)")
	}
	// Key must be 64 hex characters (32 bytes for AES-256)
	if len(cfg.Security.EncryptionKey) != 64 {
		return nil, fmt.Errorf("security.encryption_key must be 64 hexadecimal characters (32 bytes)")
	}
	// Validate hex format
	for _, c := range cfg.Security.EncryptionKey {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return nil, fmt.Errorf("security.encryption_key must contain only hexadecimal characters")
		}
	}

	// Validate JWT secret
	if cfg.Security.JWTSecret == "" {
		return nil, fmt.Errorf("security.jwt_secret is required (set SPOTTER_SECURITY_JWT_SECRET)")
	}
	if len(cfg.Security.JWTSecret) < 32 {
		return nil, fmt.Errorf("security.jwt_secret must be at least 32 characters")
	}

	return &cfg, nil
}
