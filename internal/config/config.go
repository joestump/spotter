package config

import (
	"fmt"
	"strings"

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

type Config struct {
	Security struct {
		EncryptionKey string `mapstructure:"encryption_key"` // 32-byte hex key for AES-256 encryption
	} `mapstructure:"security"`
	Database struct {
		Driver string `mapstructure:"driver"`
		Source string `mapstructure:"source"`
	} `mapstructure:"database"`
	Server struct {
		Port string `mapstructure:"port"`
		Host string `mapstructure:"host"`
	} `mapstructure:"server"`
	Navidrome struct {
		BaseURL string `mapstructure:"base_url"`
	} `mapstructure:"navidrome"`
	Lidarr struct {
		BaseURL string `mapstructure:"base_url"`
		APIKey  string `mapstructure:"api_key"`
	} `mapstructure:"lidarr"`
	Sync struct {
		Interval string `mapstructure:"interval"`
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
	LastFM struct {
		APIKey       string `mapstructure:"api_key"`
		SharedSecret string `mapstructure:"shared_secret"`
		RedirectURL  string `mapstructure:"redirect_url"`
	} `mapstructure:"lastfm"`
	OpenAI struct {
		APIKey  string `mapstructure:"api_key"`  // OpenAI API key (required for AI enrichment)
		BaseURL string `mapstructure:"base_url"` // Base URL for API (for LiteLLM or compatible proxies)
		Model   string `mapstructure:"model"`    // Model to use for enrichment (e.g., gpt-4o, gpt-4-turbo)
	} `mapstructure:"openai"`
	PlaylistSync PlaylistSyncConfig `mapstructure:"playlist_sync"`
	Vibes        VibesConfig        `mapstructure:"vibes"`
	Metadata     struct {
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
func (c *Config) MetadataEnricherOrder() []string {
	if c.Metadata.Order == "" {
		return []string{"lidarr", "musicbrainz", "navidrome", "spotify", "lastfm", "fanart", "openai"}
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
	v.SetDefault("security.encryption_key", "") // Must be set via environment variable
	v.SetDefault("server.port", "8080")
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("sync.interval", "5m")
	v.SetDefault("theme.available", "light,dark,cupcake")
	v.SetDefault("theme.default", "dark")
	v.SetDefault("database.driver", "sqlite3")
	v.SetDefault("database.source", "file:spotter.db?cache=shared&_fk=1")

	// Set defaults for keys to ensure Viper picks up environment variables
	v.SetDefault("navidrome.base_url", "")
	v.SetDefault("lidarr.base_url", "")
	v.SetDefault("lidarr.api_key", "")
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

	// Playlist sync defaults
	v.SetDefault("playlist_sync.sync_interval", "1h")
	v.SetDefault("playlist_sync.delete_on_unsync", false)
	v.SetDefault("playlist_sync.min_match_confidence", 0.8)
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
	v.SetDefault("vibes.min_match_confidence", 0.7) // Lower than playlist sync for more matches

	// Metadata enrichment defaults
	v.SetDefault("metadata.enabled", true)
	v.SetDefault("metadata.interval", "1h")
	v.SetDefault("metadata.order", "lidarr,musicbrainz,navidrome,spotify,lastfm,fanart,openai")
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

	if cfg.Lidarr.BaseURL == "" {
		return nil, fmt.Errorf("lidarr.base_url is required")
	}
	if cfg.Lidarr.APIKey == "" {
		return nil, fmt.Errorf("lidarr.api_key is required")
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

	return &cfg, nil
}
