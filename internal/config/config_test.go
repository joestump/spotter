package config

import (
	"testing"

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
