package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// PlaylistSyncConfig holds settings for syncing playlists to Navidrome.
type PlaylistSyncConfig struct {
	// SyncInterval is how often to sync playlists to Navidrome (e.g., "1h", "30m", "6h")
	SyncInterval string `mapstructure:"sync_interval"`
	// InitialDelay is how long to wait after startup before the first playlist
	// sync run (e.g., "1m", "30s"). "0s" skips the delay entirely.
	InitialDelay string `mapstructure:"initial_delay"`
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

// Governing: SPEC music-provider-integration REQ "ListenBrainz Provider" (REQ-PROV-046)
// ListenBrainzConfig holds ListenBrainz API settings. Unlike Last.fm,
// ListenBrainz uses a static per-user token (no API key/secret pair) that
// users paste from listenbrainz.org/settings; tokens are stored encrypted in
// the database (ADR-0006), so there is no instance-level token setting.
type ListenBrainzConfig struct {
	APIURL string `mapstructure:"api_url"` // Base URL for the ListenBrainz API (default: https://api.listenbrainz.org)
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
		Port string `mapstructure:"port"`
		Host string `mapstructure:"host"`
		// Governing: SPEC-0015 REQ "Email Content"
		// BaseURL is the externally reachable base URL of this Spotter
		// instance, used for links in outbound email (e.g. sync-failure
		// notifications). When empty, a best-effort fallback of
		// "http://{server.host}:{server.port}" is used — see SpotterBaseURL().
		BaseURL           string `mapstructure:"base_url"`
		ReadHeaderTimeout string `mapstructure:"read_header_timeout"` // Duration string for read header timeout (default: "10s")
		ReadTimeout       string `mapstructure:"read_timeout"`        // Duration string for read timeout (default: "30s")
		WriteTimeout      string `mapstructure:"write_timeout"`       // Duration string for write timeout (default: "60s")
		IdleTimeout       string `mapstructure:"idle_timeout"`        // Duration string for idle timeout (default: "120s")
		// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-TMO-001, REQ-TMO-005
		ShutdownTimeout string `mapstructure:"shutdown_timeout"` // Duration string for graceful shutdown budget (default: "30s")
		// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-SEM-002
		MaxConcurrentJobs int `mapstructure:"max_concurrent_jobs"` // Background job semaphore capacity (default: 10)
	} `mapstructure:"server"`
	Navidrome struct {
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
		// Governing: SPEC listen-playlist-sync REQ-SYNC-020
		// HistoryLookback bounds how far back the first history sync reaches when a
		// user has no existing listens (duration string, e.g. "720h").
		HistoryLookback string `mapstructure:"history_lookback"`
		// ProviderLookback overrides HistoryLookback per listen provider, keyed by
		// provider type (e.g. "lastfm"). A value of "0" or "unlimited" removes the
		// bound entirely so that provider syncs its full available history.
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
	LastFM       LastFMConfig       `mapstructure:"lastfm"`
	ListenBrainz ListenBrainzConfig `mapstructure:"listenbrainz"`
	OpenAI       struct {
		APIKey  string `mapstructure:"api_key"`  // OpenAI API key (required for AI enrichment)
		BaseURL string `mapstructure:"base_url"` // Base URL for API (for LiteLLM or compatible proxies)
		Model   string `mapstructure:"model"`    // Model to use for enrichment (e.g., gpt-4o, gpt-4-turbo)
	} `mapstructure:"openai"`
	SMTP          SMTPConfig          `mapstructure:"smtp"`
	Notifications NotificationsConfig `mapstructure:"notifications"`
	PlaylistSync  PlaylistSyncConfig  `mapstructure:"playlist_sync"`
	Vibes         VibesConfig         `mapstructure:"vibes"`
	Metadata      struct {
		Enabled      bool   `mapstructure:"enabled"`       // Enable/disable metadata enrichment
		Interval     string `mapstructure:"interval"`      // Sync interval (e.g., "1h", "30m")
		InitialDelay string `mapstructure:"initial_delay"` // Startup delay before first run (e.g., "30s"); "0s" skips the delay
		Order        string `mapstructure:"order"`         // Comma-separated enricher order (e.g., "musicbrainz,navidrome,spotify,lastfm,listenbrainz,fanart,openai")

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

// SpotterBaseURL returns the externally reachable base URL of this Spotter
// instance, used for links embedded in outbound email. It prefers the
// explicit server.base_url (SPOTTER_SERVER_BASE_URL) config value; when that
// is empty it derives a best-effort fallback of
// "http://{server.host}:{server.port}". Listen hosts such as "localhost" or
// "0.0.0.0" are used as-is — operators should set server.base_url when the
// instance is reached via a different external address.
// Governing: SPEC-0015 REQ "Email Content"
func (c *Config) SpotterBaseURL() string {
	if c.Server.BaseURL != "" {
		return strings.TrimRight(c.Server.BaseURL, "/")
	}
	return fmt.Sprintf("http://%s:%s", c.Server.Host, c.Server.Port)
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
func (c *Config) MetadataEnricherOrder() []string {
	if c.Metadata.Order == "" {
		// Governing: SPEC metadata-enrichment-pipeline REQ "ListenBrainz Enricher" (REQ-ENRICH-064)
		return []string{"musicbrainz", "lidarr", "navidrome", "spotify", "lastfm", "listenbrainz", "fanart", "openai"}
	}
	parts := strings.Split(c.Metadata.Order, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020
// HistoryLookbackDuration returns sync.history_lookback parsed as a duration.
// Falls back to 720h (30 days) when unset or invalid.
func (c *Config) HistoryLookbackDuration() time.Duration {
	const defaultLookback = 720 * time.Hour
	if c.Sync.HistoryLookback == "" {
		return defaultLookback
	}
	d, err := time.ParseDuration(c.Sync.HistoryLookback)
	if err != nil || d <= 0 {
		return defaultLookback
	}
	return d
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (per-provider override)
// HistoryLookbackFor returns the lookback for a specific listen provider.
// The per-provider map (sync.provider_lookback) wins over the global
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
	return c.HistoryLookbackDuration(), false
}

// Default startup delays for the background loops. Duration strings so the
// same values feed viper defaults and the accessor fallbacks below.
const (
	defaultMetadataInitialDelay     = "30s"
	defaultPlaylistSyncInitialDelay = "1m"
)

// parseInitialDelay parses an initial-delay duration string, falling back to
// the given default when the value is empty or invalid. Zero is a valid
// result and means "skip the startup delay". Load rejects invalid or
// negative values up front, so the fallback only matters for hand-built
// Config values (e.g., in tests).
func parseInitialDelay(value, fallback string) time.Duration {
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		d, _ = time.ParseDuration(fallback)
	}
	return d
}

// MetadataInitialDelay returns metadata.initial_delay parsed as a duration.
// A zero duration means the metadata enrichment loop starts without delay.
func (c *Config) MetadataInitialDelay() time.Duration {
	return parseInitialDelay(c.Metadata.InitialDelay, defaultMetadataInitialDelay)
}

// PlaylistSyncInitialDelay returns playlist_sync.initial_delay parsed as a
// duration. A zero duration means the playlist sync loop starts without delay.
func (c *Config) PlaylistSyncInitialDelay() time.Duration {
	return parseInitialDelay(c.PlaylistSync.InitialDelay, defaultPlaylistSyncInitialDelay)
}

// IsOpenAIEnabled returns true if OpenAI API key is configured.
func (c *Config) IsOpenAIEnabled() bool {
	return c.OpenAI.APIKey != ""
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

func Load() (*Config, error) {
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
	// Governing: SPEC-0015 REQ "Email Content" — external base URL for email links
	v.SetDefault("server.base_url", "")
	v.SetDefault("server.read_header_timeout", "10s")
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "60s")
	v.SetDefault("server.idle_timeout", "120s")
	// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-TMO-001, REQ-SEM-002
	v.SetDefault("server.shutdown_timeout", "30s")
	v.SetDefault("server.max_concurrent_jobs", 10)
	v.SetDefault("sync.interval", "5m")
	// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (default initial history lookback of 30 days)
	v.SetDefault("sync.history_lookback", "720h")
	v.SetDefault("theme.available", "light,dark,cupcake")
	v.SetDefault("theme.default", "dark")
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
	// Governing: SPEC music-provider-integration REQ "ListenBrainz Provider" (REQ-PROV-046)
	v.SetDefault("listenbrainz.api_url", "https://api.listenbrainz.org")

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
	// Governing: ADR-0009 (Viper configuration) — startup delay before first playlist sync run
	v.SetDefault("playlist_sync.initial_delay", defaultPlaylistSyncInitialDelay)
	v.SetDefault("playlist_sync.delete_on_unsync", false)
	// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-003, ADR-0014 (default fuzzy match threshold 0.7)
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
	// Governing: ADR-0009 (Viper configuration) — startup delay before first enrichment run
	v.SetDefault("metadata.initial_delay", defaultMetadataInitialDelay)
	// Governing: SPEC metadata-enrichment-pipeline REQ "ListenBrainz Enricher" (REQ-ENRICH-064)
	v.SetDefault("metadata.order", "musicbrainz,lidarr,navidrome,spotify,lastfm,listenbrainz,fanart,openai")
	v.SetDefault("metadata.musicbrainz.user_agent", "Spotter/1.0.0 (https://github.com/spotter)")
	v.SetDefault("metadata.fanart.api_key", "")
	v.SetDefault("metadata.images.download", true)
	v.SetDefault("metadata.images.directory", "./data/images")
	v.SetDefault("metadata.images.max_width", 1000)
	v.SetDefault("metadata.images.max_height", 1000)
	v.SetDefault("metadata.ai.prompts_directory", "./data/prompts")

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	// Governing: SPEC-0016 REQ "Driver Validation", ADR-0023
	validDrivers := map[string]bool{"sqlite3": true, "postgres": true, "mysql": true}
	if !validDrivers[cfg.Database.Driver] {
		return nil, fmt.Errorf("unsupported database driver %q: must be one of sqlite3, postgres, mysql", cfg.Database.Driver)
	}

	// Governing: SPEC-0016 REQ "Driver-Specific Default Source", ADR-0023
	const sqliteDefault = "file:spotter.db?cache=shared&_fk=1"
	if cfg.Database.Driver == "postgres" && (cfg.Database.Source == "" || cfg.Database.Source == sqliteDefault) {
		cfg.Database.Source = "host=localhost port=5432 dbname=spotter sslmode=disable"
	} else if cfg.Database.Driver == "mysql" && (cfg.Database.Source == "" || cfg.Database.Source == sqliteDefault) {
		cfg.Database.Source = "spotter:spotter@tcp(localhost:3306)/spotter?parseTime=true&charset=utf8mb4"
	}

	// Apply defaults for OpenAI config when env vars are empty strings
	// (Viper treats empty string env vars as "set", overriding defaults)
	if cfg.OpenAI.BaseURL == "" {
		cfg.OpenAI.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.OpenAI.Model == "" {
		cfg.OpenAI.Model = "gpt-4o"
	}

	// Apply defaults for initial delays when env vars are empty strings
	// (Viper treats empty string env vars as "set", overriding defaults)
	if cfg.Metadata.InitialDelay == "" {
		cfg.Metadata.InitialDelay = defaultMetadataInitialDelay
	}
	if cfg.PlaylistSync.InitialDelay == "" {
		cfg.PlaylistSync.InitialDelay = defaultPlaylistSyncInitialDelay
	}

	// Validate startup delays: must parse as durations and must not be
	// negative. Zero is allowed and skips the startup delay entirely.
	initialDelays := []struct {
		key   string
		value string
	}{
		{"metadata.initial_delay", cfg.Metadata.InitialDelay},
		{"playlist_sync.initial_delay", cfg.PlaylistSync.InitialDelay},
	}
	for _, delay := range initialDelays {
		d, err := time.ParseDuration(delay.value)
		if err != nil {
			return nil, fmt.Errorf("%s must be a valid duration string (e.g. \"30s\"): %w", delay.key, err)
		}
		if d < 0 {
			return nil, fmt.Errorf("%s must not be negative, got %q", delay.key, delay.value)
		}
	}

	if cfg.Navidrome.BaseURL == "" {
		return nil, fmt.Errorf("navidrome.base_url is required")
	}

	// Governing: SPEC-0016 (compose examples run without Lidarr), ADR-0023
	// Lidarr is an optional integration: the enricher reports itself
	// unavailable and the queue submitter stays disabled when it is not
	// configured. Only reject half-configured setups.
	if (cfg.Lidarr.BaseURL == "") != (cfg.Lidarr.APIKey == "") {
		return nil, fmt.Errorf("lidarr configuration is incomplete: set both lidarr.base_url and lidarr.api_key (SPOTTER_LIDARR_BASE_URL and SPOTTER_LIDARR_API_KEY) to enable Lidarr, or neither to run without it")
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
