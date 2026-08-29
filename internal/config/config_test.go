package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	// Required config
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "8080", cfg.Server.Port)
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, "5m", cfg.Sync.Interval)
	assert.Equal(t, "sqlite3", cfg.Database.Driver)
	assert.Equal(t, "file:spotter.db?cache=shared&_fk=1", cfg.Database.Source)
	assert.Equal(t, "http://127.0.0.1:8080/auth/spotify/callback", cfg.Spotify.RedirectURL)
	assert.Equal(t, "light,dark,cupcake", cfg.Theme.Available)
	assert.Equal(t, "dark", cfg.Theme.Default)
	assert.Equal(t, []string{"light", "dark", "cupcake"}, cfg.AvailableThemes())
	assert.Equal(t, "text", cfg.Log.Format)
	// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (default initial history lookback of 30 days)
	assert.Equal(t, "720h", cfg.Sync.HistoryLookback)
	assert.Equal(t, 720*time.Hour, cfg.HistoryLookbackDuration())
	// Governing: SPEC playlist-sync-navidrome REQ-PLSYNC-003, ADR-0014 (default fuzzy match threshold 0.7)
	assert.Equal(t, 0.7, cfg.PlaylistSync.MinMatchConfidence)
	// Startup delays for background loops
	assert.Equal(t, "30s", cfg.Metadata.InitialDelay)
	assert.Equal(t, 30*time.Second, cfg.MetadataInitialDelay())
	assert.Equal(t, "1m", cfg.PlaylistSync.InitialDelay)
	assert.Equal(t, 1*time.Minute, cfg.PlaylistSyncInitialDelay())
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (sync.history_lookback config key)
func TestHistoryLookbackConfig(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_SYNC_HISTORY_LOOKBACK", "48h")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "48h", cfg.Sync.HistoryLookback)
	assert.Equal(t, 48*time.Hour, cfg.HistoryLookbackDuration())
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (invalid lookback falls back to 720h)
func TestHistoryLookbackDuration_Fallback(t *testing.T) {
	var cfg Config
	assert.Equal(t, 720*time.Hour, cfg.HistoryLookbackDuration(), "empty value falls back to 720h")

	cfg.Sync.HistoryLookback = "not-a-duration"
	assert.Equal(t, 720*time.Hour, cfg.HistoryLookbackDuration(), "invalid value falls back to 720h")

	cfg.Sync.HistoryLookback = "-24h"
	assert.Equal(t, 720*time.Hour, cfg.HistoryLookbackDuration(), "non-positive value falls back to 720h")
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (per-provider lookback override)
func TestHistoryLookbackFor(t *testing.T) {
	var cfg Config
	cfg.Sync.HistoryLookback = "48h"

	lookback, unlimited := cfg.HistoryLookbackFor("spotify")
	assert.Equal(t, 48*time.Hour, lookback, "falls back to global lookback")
	assert.False(t, unlimited, "global fallback is never unlimited")

	cfg.Sync.ProviderLookback = map[string]string{
		"lastfm":  "unlimited",
		"spotify": "2160h",
		"weird":   "not-a-duration",
	}

	lookback, unlimited = cfg.HistoryLookbackFor("lastfm")
	assert.True(t, unlimited, "unlimited keyword removes the bound")

	lookback, unlimited = cfg.HistoryLookbackFor("listenbrainz")
	assert.Equal(t, 48*time.Hour, lookback, "providers without an entry use the global lookback")
	assert.False(t, unlimited, "providers without an entry are never unlimited")

	lookback, _ = cfg.HistoryLookbackFor("spotify")
	assert.Equal(t, 2160*time.Hour, lookback, "per-provider duration overrides global")

	lookback, unlimited = cfg.HistoryLookbackFor("weird")
	assert.Equal(t, 48*time.Hour, lookback, "invalid per-provider value falls back to global")
	assert.False(t, unlimited)

	_, unlimited = cfg.HistoryLookbackFor("zero")
	assert.False(t, unlimited)
	cfg.Sync.ProviderLookback["zero"] = "0"
	_, unlimited = cfg.HistoryLookbackFor("zero")
	assert.True(t, unlimited, "0 means unlimited")
}

func TestLogFormatConfig(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_LOG_FORMAT", "json")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "json", cfg.Log.Format)
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("SPOTTER_SERVER_PORT", "9090")
	t.Setenv("SPOTTER_SERVER_HOST", "127.0.0.1")
	t.Setenv("SPOTTER_DATABASE_DRIVER", "postgres")
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "https://navidrome.example.com")
	t.Setenv("SPOTTER_SPOTIFY_REDIRECT_URL", "https://example.com/callback")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "9090", cfg.Server.Port)
	assert.Equal(t, "127.0.0.1", cfg.Server.Host)
	assert.Equal(t, "postgres", cfg.Database.Driver)
	assert.Equal(t, "https://navidrome.example.com", cfg.Navidrome.BaseURL)
	assert.Equal(t, "https://example.com/callback", cfg.Spotify.RedirectURL)
}

func TestLoadMissingNavidromeURL(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")

	_, err := Load()
	require.Error(t, err)
	assert.EqualError(t, err, "navidrome.base_url is required")
}

func TestLoadMissingOpenAIKey(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")

	_, err := Load()
	require.Error(t, err)
	assert.EqualError(t, err, "openai.api_key is required for AI enrichment")
}

func TestSyncIntervalConfig(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_SYNC_INTERVAL", "10m")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "10m", cfg.Sync.Interval)
}

func TestThemeConfig(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_THEME_AVAILABLE", "dark,light,cyberpunk,retro")
	t.Setenv("SPOTTER_THEME_DEFAULT", "cyberpunk")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "dark,light,cyberpunk,retro", cfg.Theme.Available)
	assert.Equal(t, "cyberpunk", cfg.Theme.Default)
	assert.Equal(t, []string{"dark", "light", "cyberpunk", "retro"}, cfg.AvailableThemes())
}

func TestAvailableThemesDefaultsWhenNotSet(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	// Don't set SPOTTER_THEME_AVAILABLE - should use defaults

	cfg, err := Load()
	require.NoError(t, err)

	// Should return default themes when not configured
	assert.Equal(t, []string{"light", "dark", "cupcake"}, cfg.AvailableThemes())
}

func TestAvailableThemesTrimsWhitespace(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_THEME_AVAILABLE", " dark , light , cupcake ")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, []string{"dark", "light", "cupcake"}, cfg.AvailableThemes())
}

func TestOpenAIDefaults(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	// Explicitly clear these to test defaults (overrides any env vars from user's shell)
	t.Setenv("SPOTTER_OPENAI_BASE_URL", "")
	t.Setenv("SPOTTER_OPENAI_MODEL", "")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "sk-test-key", cfg.OpenAI.APIKey)
	assert.Equal(t, "https://api.openai.com/v1", cfg.OpenAI.BaseURL)
	assert.Equal(t, "gpt-4o", cfg.OpenAI.Model)
	assert.True(t, cfg.IsOpenAIEnabled())
}

func TestOpenAIConfig(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key-12345")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_OPENAI_BASE_URL", "https://api.litellm.example.com/v1")
	t.Setenv("SPOTTER_OPENAI_MODEL", "gpt-4-turbo")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "sk-test-key-12345", cfg.OpenAI.APIKey)
	assert.Equal(t, "https://api.litellm.example.com/v1", cfg.OpenAI.BaseURL)
	assert.Equal(t, "gpt-4-turbo", cfg.OpenAI.Model)
	assert.True(t, cfg.IsOpenAIEnabled())
}

func TestIsOpenAIEnabled(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-valid-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")

	cfg, err := Load()
	require.NoError(t, err)

	assert.True(t, cfg.IsOpenAIEnabled())
}

func TestMetadataEnricherOrderIncludesOpenAI(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")

	cfg, err := Load()
	require.NoError(t, err)

	order := cfg.MetadataEnricherOrder()
	assert.Contains(t, order, "openai")
	// OpenAI should be last in the default order
	assert.Equal(t, "openai", order[len(order)-1])
}

func TestMetadataEnricherOrderCustom(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_METADATA_ORDER", "musicbrainz,spotify,openai")

	cfg, err := Load()
	require.NoError(t, err)

	order := cfg.MetadataEnricherOrder()
	assert.Equal(t, []string{"musicbrainz", "spotify", "openai"}, order)
}

func TestAIPromptsDirectoryDefault(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "./data/prompts", cfg.Metadata.AI.PromptsDirectory)
}

func TestAIPromptsDirectoryCustom(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_METADATA_AI_PROMPTS_DIRECTORY", "/custom/prompts")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "/custom/prompts", cfg.Metadata.AI.PromptsDirectory)
}

// setRequiredEnvVars sets all required environment variables for config loading.
func setRequiredEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
}

// Governing: SPEC-0016 REQ "Test Coverage" (valid drivers, invalid driver rejection, default source per driver)
func TestConfig_ValidDrivers(t *testing.T) {
	for _, driver := range []string{"sqlite3", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			setRequiredEnvVars(t)
			t.Setenv("SPOTTER_DATABASE_DRIVER", driver)

			cfg, err := Load()
			require.NoError(t, err)
			assert.Equal(t, driver, cfg.Database.Driver)
		})
	}
}

func TestConfig_InvalidDriver(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SPOTTER_DATABASE_DRIVER", "cockroachdb")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported database driver")
}

func TestConfig_PostgresDefaultSource(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SPOTTER_DATABASE_DRIVER", "postgres")
	// Do not set SPOTTER_DATABASE_SOURCE — should get postgres default

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "host=localhost port=5432 dbname=spotter sslmode=disable", cfg.Database.Source)
}

func TestConfig_MySQLDefaultSource(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SPOTTER_DATABASE_DRIVER", "mysql")
	// Do not set SPOTTER_DATABASE_SOURCE — should get mysql default

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "spotter:spotter@tcp(localhost:3306)/spotter?parseTime=true&charset=utf8mb4", cfg.Database.Source)
}

// Governing: SPEC-0016 (compose examples run without Lidarr), ADR-0023
// Lidarr is optional: config loads without any Lidarr settings so the
// shipped compose examples start cleanly.
func TestConfig_LidarrOptional(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Empty(t, cfg.Lidarr.BaseURL)
	assert.Empty(t, cfg.Lidarr.APIKey)
}

func TestConfig_LidarrIncompleteBaseURLOnly(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lidarr configuration is incomplete")
}

func TestConfig_LidarrIncompleteAPIKeyOnly(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lidarr configuration is incomplete")
}

func TestConfig_LidarrFullyConfigured(t *testing.T) {
	setRequiredEnvVars(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8686", cfg.Lidarr.BaseURL)
	assert.Equal(t, "test-api-key", cfg.Lidarr.APIKey)
}

// TestConfig_InitialDelays covers the configurable startup delays for the
// background metadata enrichment and playlist sync loops: defaults, env
// overrides, zero (skip the delay entirely), and rejection of negative or
// unparseable values.
func TestConfig_InitialDelays(t *testing.T) {
	tests := []struct {
		name              string
		metadataDelay     string // SPOTTER_METADATA_INITIAL_DELAY; "" leaves the env var unset
		playlistSyncDelay string // SPOTTER_PLAYLIST_SYNC_INITIAL_DELAY; "" leaves the env var unset
		wantMetadata      time.Duration
		wantPlaylistSync  time.Duration
		wantErr           string
	}{
		{
			name:             "defaults",
			wantMetadata:     30 * time.Second,
			wantPlaylistSync: 1 * time.Minute,
		},
		{
			name:              "env overrides",
			metadataDelay:     "2m",
			playlistSyncDelay: "45s",
			wantMetadata:      2 * time.Minute,
			wantPlaylistSync:  45 * time.Second,
		},
		{
			name:              "zero skips the delay",
			metadataDelay:     "0s",
			playlistSyncDelay: "0s",
			wantMetadata:      0,
			wantPlaylistSync:  0,
		},
		{
			name:          "negative metadata delay rejected",
			metadataDelay: "-30s",
			wantErr:       "metadata.initial_delay must not be negative",
		},
		{
			name:              "negative playlist sync delay rejected",
			playlistSyncDelay: "-1m",
			wantErr:           "playlist_sync.initial_delay must not be negative",
		},
		{
			name:          "unparseable metadata delay rejected",
			metadataDelay: "not-a-duration",
			wantErr:       "metadata.initial_delay must be a valid duration string",
		},
		{
			name:              "unparseable playlist sync delay rejected",
			playlistSyncDelay: "soon",
			wantErr:           "playlist_sync.initial_delay must be a valid duration string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnvVars(t)
			if tt.metadataDelay != "" {
				t.Setenv("SPOTTER_METADATA_INITIAL_DELAY", tt.metadataDelay)
			}
			if tt.playlistSyncDelay != "" {
				t.Setenv("SPOTTER_PLAYLIST_SYNC_INITIAL_DELAY", tt.playlistSyncDelay)
			}

			cfg, err := Load()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantMetadata, cfg.MetadataInitialDelay())
			assert.Equal(t, tt.wantPlaylistSync, cfg.PlaylistSyncInitialDelay())
		})
	}
}

// TestConfig_InitialDelaysEmptyEnvFallsBack: Viper treats an empty-string env
// var as "set", so Load applies the defaults instead of failing to parse "".
func TestConfig_InitialDelaysEmptyEnvFallsBack(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SPOTTER_METADATA_INITIAL_DELAY", "")
	t.Setenv("SPOTTER_PLAYLIST_SYNC_INITIAL_DELAY", "")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, cfg.MetadataInitialDelay())
	assert.Equal(t, 1*time.Minute, cfg.PlaylistSyncInitialDelay())
}

// Governing: SPEC-0015 REQ "Email Content" — email links point at the Spotter instance
func TestSpotterBaseURL_ExplicitConfig(t *testing.T) {
	cfg := &Config{}
	cfg.Server.BaseURL = "https://spotter.example.com/"
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = "8080"

	// Explicit base URL wins; trailing slash is trimmed.
	assert.Equal(t, "https://spotter.example.com", cfg.SpotterBaseURL())
}

// Governing: SPEC-0015 REQ "Email Content" — best-effort fallback from host/port
func TestSpotterBaseURL_Fallback(t *testing.T) {
	cfg := &Config{}
	cfg.Server.Host = "0.0.0.0"
	cfg.Server.Port = "8080"

	// Listen hosts like 0.0.0.0/localhost are used as-is in the fallback.
	assert.Equal(t, "http://0.0.0.0:8080", cfg.SpotterBaseURL())

	cfg.Server.Host = "localhost"
	assert.Equal(t, "http://localhost:8080", cfg.SpotterBaseURL())
}

func TestSpotterBaseURL_EnvVar(t *testing.T) {
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	t.Setenv("SPOTTER_SERVER_BASE_URL", "https://spotter.example.com")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "https://spotter.example.com", cfg.Server.BaseURL)
	assert.Equal(t, "https://spotter.example.com", cfg.SpotterBaseURL())
}
