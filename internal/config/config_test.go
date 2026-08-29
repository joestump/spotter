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
	assert.Equal(t, "spotter,night,synthwave,dracula,dark,light", cfg.Theme.Available)
	assert.Equal(t, "spotter", cfg.Theme.Default)
	assert.Equal(t, []string{"spotter", "night", "synthwave", "dracula", "dark", "light"}, cfg.AvailableThemes())
	assert.Equal(t, "text", cfg.Log.Format)
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
	assert.Equal(t, []string{"spotter", "night", "synthwave", "dracula", "dark", "light"}, cfg.AvailableThemes())
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
// Lidarr is intentionally absent: it is optional (see TestLidarrOptional).
func setRequiredEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "http://localhost:4533")
	t.Setenv("SPOTTER_OPENAI_API_KEY", "sk-test-key")
	t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "test-jwt-secret-at-least-32-chars")
	// Explicitly clear optional Lidarr vars so values from the developer's shell
	// don't leak into tests.
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "")
}

// Governing: ADR-0009 (Viper configuration), SPEC-0014 (compose scenarios MUST start without Lidarr)
// Lidarr configuration is optional: Load() must succeed with no Lidarr env vars set.
func TestLidarrOptional(t *testing.T) {
	setRequiredEnvVars(t)

	cfg, err := Load()
	require.NoError(t, err)
	assert.False(t, cfg.IsLidarrEnabled())
}

// Governing: SPEC-0017 REQ "Background Submitter Goroutine" (submitter only starts if Lidarr is configured)
func TestLidarrEnabledWhenFullyConfigured(t *testing.T) {
	setRequiredEnvVars(t)
	t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
	t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")

	cfg, err := Load()
	require.NoError(t, err)
	assert.True(t, cfg.IsLidarrEnabled())
	assert.Equal(t, "http://localhost:8686", cfg.Lidarr.BaseURL)
	assert.Equal(t, "test-api-key", cfg.Lidarr.APIKey)
}

// Governing: ADR-0009 (fail fast with clear error messages)
// Setting only one of the two Lidarr values is a misconfiguration and must fail.
func TestLidarrPartialConfigRejected(t *testing.T) {
	t.Run("base_url without api_key", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")

		_, err := Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lidarr.base_url and lidarr.api_key must both be set")
	})

	t.Run("api_key without base_url", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")

		_, err := Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lidarr.base_url and lidarr.api_key must both be set")
	})
}

// Governing: ADR-0015 (pluggable enricher registry), ADR-0009 (Lidarr optional)
// The lidarr enricher is filtered out of the enricher order when Lidarr is unconfigured.
func TestMetadataEnricherOrderExcludesLidarrWhenUnconfigured(t *testing.T) {
	t.Run("default order", func(t *testing.T) {
		setRequiredEnvVars(t)

		cfg, err := Load()
		require.NoError(t, err)
		assert.NotContains(t, cfg.MetadataEnricherOrder(), "lidarr")
	})

	t.Run("explicit order", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_METADATA_ORDER", "lidarr,musicbrainz,openai")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, []string{"musicbrainz", "openai"}, cfg.MetadataEnricherOrder())
	})

	t.Run("included when lidarr configured", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_LIDARR_BASE_URL", "http://localhost:8686")
		t.Setenv("SPOTTER_LIDARR_API_KEY", "test-api-key")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Contains(t, cfg.MetadataEnricherOrder(), "lidarr")
	})

	t.Run("order of only lidarr yields empty order when unconfigured", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_METADATA_ORDER", "lidarr")

		cfg, err := Load()
		require.NoError(t, err)
		// Enrichment becomes a no-op; MetadataEnricherOrder logs a WARN for this.
		assert.Empty(t, cfg.MetadataEnricherOrder())
	})
}

// Governing: ADR-0009 (Viper configuration), ADR-0023 (multi-database support), SPEC key-rotation
// LoadDatabase must work standalone — with ONLY database env vars set — so
// `spotter-admin rotate-key` does not require full server configuration.
func TestLoadDatabase(t *testing.T) {
	// clearServerEnv blanks all full-server required vars to prove LoadDatabase
	// does not need them (and to shield against values leaking from the shell).
	clearServerEnv := func(t *testing.T) {
		t.Helper()
		t.Setenv("SPOTTER_NAVIDROME_BASE_URL", "")
		t.Setenv("SPOTTER_OPENAI_API_KEY", "")
		t.Setenv("SPOTTER_SECURITY_ENCRYPTION_KEY", "")
		t.Setenv("SPOTTER_SECURITY_JWT_SECRET", "")
		t.Setenv("SPOTTER_LIDARR_BASE_URL", "")
		t.Setenv("SPOTTER_LIDARR_API_KEY", "")
	}

	t.Run("standalone with no server config", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("SPOTTER_DATABASE_DRIVER", "sqlite3")
		t.Setenv("SPOTTER_DATABASE_SOURCE", "file:rotate.db?_fk=1")

		// Full Load must fail without server config...
		_, err := Load()
		require.Error(t, err)

		// ...but LoadDatabase succeeds with database vars alone.
		cfg, err := LoadDatabase()
		require.NoError(t, err)
		assert.Equal(t, "sqlite3", cfg.Database.Driver)
		assert.Equal(t, "file:rotate.db?_fk=1", cfg.Database.Source)
	})

	t.Run("defaults when nothing set", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("SPOTTER_DATABASE_DRIVER", "")
		t.Setenv("SPOTTER_DATABASE_SOURCE", "")

		cfg, err := LoadDatabase()
		require.NoError(t, err)
		assert.Equal(t, "sqlite3", cfg.Database.Driver)
		assert.Equal(t, "file:spotter.db?cache=shared&_fk=1", cfg.Database.Source)
	})

	t.Run("driver-specific default source", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("SPOTTER_DATABASE_DRIVER", "postgres")
		t.Setenv("SPOTTER_DATABASE_SOURCE", "")

		cfg, err := LoadDatabase()
		require.NoError(t, err)
		assert.Equal(t, "host=localhost port=5432 dbname=spotter sslmode=disable", cfg.Database.Source)
	})

	t.Run("invalid driver rejected", func(t *testing.T) {
		clearServerEnv(t)
		t.Setenv("SPOTTER_DATABASE_DRIVER", "cockroachdb")

		_, err := LoadDatabase()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported database driver")
	})
}

// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-TMO-005
// SPOTTER_SHUTDOWN_TIMEOUT must bind through Viper (moved out of raw os.Getenv).
func TestShutdownTimeout(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SHUTDOWN_TIMEOUT", "")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, cfg.GetShutdownTimeout())
	})

	t.Run("env override", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SHUTDOWN_TIMEOUT", "45s")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 45*time.Second, cfg.GetShutdownTimeout())
	})

	t.Run("invalid falls back to default", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SHUTDOWN_TIMEOUT", "not-a-duration")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, cfg.GetShutdownTimeout())
	})

	t.Run("negative falls back to default", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SHUTDOWN_TIMEOUT", "-5s")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, cfg.GetShutdownTimeout())
	})

	t.Run("zero falls back to default", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SHUTDOWN_TIMEOUT", "0s")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, cfg.GetShutdownTimeout())
	})
}

// Governing: ADR-0009 (Viper configuration), SPEC graceful-shutdown REQ-SEM-002
// SPOTTER_MAX_CONCURRENT_JOBS must bind through Viper (moved out of raw os.Getenv).
func TestMaxConcurrentJobs(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		setRequiredEnvVars(t)

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 10, cfg.GetMaxConcurrentJobs())
	})

	t.Run("env override", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_MAX_CONCURRENT_JOBS", "5")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 5, cfg.GetMaxConcurrentJobs())
	})

	t.Run("non-positive falls back to default", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_MAX_CONCURRENT_JOBS", "0")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 10, cfg.GetMaxConcurrentJobs())
	})

	t.Run("negative falls back to default", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_MAX_CONCURRENT_JOBS", "-3")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 10, cfg.GetMaxConcurrentJobs())
	})

	// Governing: ADR-0009 (fail fast with clear error messages)
	t.Run("non-numeric returns clear validation error", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_MAX_CONCURRENT_JOBS", "lots")

		_, err := Load()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max_concurrent_jobs must be an integer")
		assert.Contains(t, err.Error(), `"lots"`)
	})
}

// Governing: ADR-0026, SPEC-0015 (public base URL used in sync-failure email links)
func TestServerBaseURL(t *testing.T) {
	t.Run("default empty", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SERVER_BASE_URL", "")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, "", cfg.Server.BaseURL)
	})

	t.Run("env override", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SERVER_BASE_URL", "https://spotter.example.com")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, "https://spotter.example.com", cfg.Server.BaseURL)
	})

	t.Run("trailing slashes are trimmed", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SERVER_BASE_URL", "https://spotter.example.com//")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, "https://spotter.example.com", cfg.Server.BaseURL)
	})
}

// Governing: SPEC listen-playlist-sync REQ-SYNC-020 (configurable initial history lookback)
func TestSyncHistoryLookback(t *testing.T) {
	t.Run("default 720h", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SYNC_HISTORY_LOOKBACK", "")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 720*time.Hour, cfg.GetSyncHistoryLookback())
	})

	t.Run("env override", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SYNC_HISTORY_LOOKBACK", "240h")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, "240h", cfg.Sync.HistoryLookback)
		assert.Equal(t, 240*time.Hour, cfg.GetSyncHistoryLookback())
	})

	t.Run("invalid falls back to default", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SYNC_HISTORY_LOOKBACK", "one-fortnight")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 720*time.Hour, cfg.GetSyncHistoryLookback())
	})

	t.Run("negative falls back to default", func(t *testing.T) {
		setRequiredEnvVars(t)
		t.Setenv("SPOTTER_SYNC_HISTORY_LOOKBACK", "-240h")

		cfg, err := Load()
		require.NoError(t, err)
		assert.Equal(t, 720*time.Hour, cfg.GetSyncHistoryLookback())
	})
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
		"zero":    "0",
	}

	_, unlimited = cfg.HistoryLookbackFor("lastfm")
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
	assert.True(t, unlimited, "0 means unlimited")
}

// Governing: SPEC-0014 REQ "Test Coverage" (valid drivers, invalid driver rejection, default source per driver)
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
