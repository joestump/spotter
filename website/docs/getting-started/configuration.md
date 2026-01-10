---
sidebar_position: 2
---

# Configuration

Spotter is configured using environment variables. You can set these in your shell or use a `.env` file.

## Required Configuration

### Navidrome

| Variable | Description |
| :--- | :--- |
| `SPOTTER_NAVIDROME_BASE_URL` | **Required.** The URL of your Navidrome instance (e.g., `https://music.example.com`) |

### OpenAI

| Variable | Description |
| :--- | :--- |
| `SPOTTER_OPENAI_API_KEY` | **Required.** OpenAI API key for AI-powered metadata enrichment |

## Server Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_SERVER_PORT` | The HTTP port the server listens on | `8080` |
| `SPOTTER_SERVER_HOST` | The host address to bind to | `0.0.0.0` |

## Security Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_SECURITY_ENCRYPTION_KEY` | 64-character hex key for encrypting OAuth tokens at rest | *None* |
| `SPOTTER_SECURITY_SECURE_COOKIES` | Set `Secure` flag on session cookies (requires HTTPS) | `true` |

:::caution Production Requirement
When deploying with HTTPS (recommended), keep `SPOTTER_SECURITY_SECURE_COOKIES=true` (the default). Only set to `false` for local development over HTTP.
:::

## Database Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_DATABASE_DRIVER` | Database driver (`sqlite3`, `postgres`, `mysql`) | `sqlite3` |
| `SPOTTER_DATABASE_SOURCE` | Connection string for the database | `file:spotter.db?cache=shared&_fk=1` |

## Sync Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_SYNC_INTERVAL` | How often to sync data from providers (Go duration format) | `5m` |

## Theme Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_THEME_AVAILABLE` | Comma-separated list of available DaisyUI theme names | `light,dark,cupcake` |
| `SPOTTER_THEME_DEFAULT` | Default theme for new users | `dark` |

## Provider Configuration

### Spotify (Optional)

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_SPOTIFY_CLIENT_ID` | Spotify Client ID for API access | *None* |
| `SPOTTER_SPOTIFY_CLIENT_SECRET` | Spotify Client Secret | *None* |
| `SPOTTER_SPOTIFY_REDIRECT_URL` | OAuth callback URL for Spotify | `http://127.0.0.1:8080/auth/spotify/callback` |

### Last.fm (Optional)

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_LASTFM_API_KEY` | Last.fm API Key | *None* |
| `SPOTTER_LASTFM_SHARED_SECRET` | Last.fm Shared Secret | *None* |
| `SPOTTER_LASTFM_REDIRECT_URL` | Callback URL for Last.fm auth | *None* |

### Lidarr (Optional)

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_LIDARR_BASE_URL` | Base URL to your Lidarr instance | *None* |
| `SPOTTER_LIDARR_API_KEY` | API Key for your Lidarr instance | *None* |

## Metadata Enrichment Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_METADATA_ENABLED` | Enable/disable metadata enrichment | `true` |
| `SPOTTER_METADATA_INTERVAL` | How often to run metadata enrichment | `1h` |
| `SPOTTER_METADATA_ORDER` | Comma-separated enricher priority order | `lidarr,musicbrainz,navidrome,spotify,lastfm,fanart,openai` |

### OpenAI Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_OPENAI_BASE_URL` | Base URL for OpenAI API (use for LiteLLM or compatible proxies) | `https://api.openai.com/v1` |
| `SPOTTER_OPENAI_MODEL` | Model to use for AI enrichment | `gpt-4o` |
| `SPOTTER_METADATA_AI_PROMPTS_DIRECTORY` | Directory containing prompt templates | `./data/prompts` |

### MusicBrainz Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_METADATA_MUSICBRAINZ_USER_AGENT` | User-Agent string for MusicBrainz API requests | `Spotter/1.0.0 (...)` |

### Fanart.tv Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_METADATA_FANART_API_KEY` | Fanart.tv personal API key | *None* |

### Image Download Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_METADATA_IMAGES_DOWNLOAD` | Whether to download images locally | `true` |
| `SPOTTER_METADATA_IMAGES_DIRECTORY` | Directory to store downloaded images | `./data/images` |
| `SPOTTER_METADATA_IMAGES_MAX_WIDTH` | Maximum image width (for resizing) | `1000` |
| `SPOTTER_METADATA_IMAGES_MAX_HEIGHT` | Maximum image height (for resizing) | `1000` |

## Vibes Engine Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_VIBES_DEFAULT_MAX_TRACKS` | Default maximum tracks per mixtape | `25` |
| `SPOTTER_VIBES_MIN_TRACKS` | Minimum tracks for a valid mixtape | `5` |
| `SPOTTER_VIBES_MAX_TRACKS` | Maximum allowed tracks (hard limit) | `100` |
| `SPOTTER_VIBES_HISTORY_DAYS` | Days of listening history to include in context | `30` |
| `SPOTTER_VIBES_MAX_HISTORY_TRACKS` | Maximum history tracks to include in prompt | `50` |
| `SPOTTER_VIBES_MODEL` | AI model for mixtape generation | *Falls back to `openai.model`* |
| `SPOTTER_VIBES_TEMPERATURE` | AI temperature for generation (0.0-2.0) | `0.8` |
| `SPOTTER_VIBES_MAX_TOKENS` | Maximum tokens for AI response | `4000` |
| `SPOTTER_VIBES_TIMEOUT_SECONDS` | Timeout for AI generation requests | `120` |
| `SPOTTER_VIBES_MIN_MATCH_CONFIDENCE` | Minimum confidence for track matching | `0.7` |

## Playlist Sync Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_PLAYLIST_SYNC_SYNC_INTERVAL` | How often to sync enabled playlists to Navidrome | `1h` |
| `SPOTTER_PLAYLIST_SYNC_DELETE_ON_UNSYNC` | Delete Navidrome playlist when sync is disabled | `false` |
| `SPOTTER_PLAYLIST_SYNC_MIN_MATCH_CONFIDENCE` | Minimum confidence for fuzzy track matching (0.0-1.0) | `0.8` |
| `SPOTTER_PLAYLIST_SYNC_INCLUDE_UNMATCHED_TRACKS` | Include unmatched tracks as placeholders | `false` |

## Example `.env` File

```bash
# ===================
# Required
# ===================
SPOTTER_NAVIDROME_BASE_URL=https://music.example.com
SPOTTER_OPENAI_API_KEY=sk-your-openai-api-key

# ===================
# Server Configuration
# ===================
SPOTTER_SERVER_PORT=8080
SPOTTER_SERVER_HOST=0.0.0.0

# ===================
# Security Configuration
# ===================
# Generate with: openssl rand -hex 32
SPOTTER_SECURITY_ENCRYPTION_KEY=your_64_char_hex_key_here
# Set to false for local HTTP development
SPOTTER_SECURITY_SECURE_COOKIES=true

# ===================
# Database Configuration
# ===================
SPOTTER_DATABASE_DRIVER=sqlite3
SPOTTER_DATABASE_SOURCE=file:spotter.db?cache=shared&_fk=1

# ===================
# Sync Configuration
# ===================
SPOTTER_SYNC_INTERVAL=5m

# ===================
# Theme Configuration
# ===================
SPOTTER_THEME_AVAILABLE=light,dark,cupcake
SPOTTER_THEME_DEFAULT=dark

# ===================
# Spotify Integration (Optional)
# ===================
SPOTTER_SPOTIFY_CLIENT_ID=your_spotify_client_id
SPOTTER_SPOTIFY_CLIENT_SECRET=your_spotify_client_secret
SPOTTER_SPOTIFY_REDIRECT_URL=http://localhost:8080/auth/spotify/callback

# ===================
# Last.fm Integration (Optional)
# ===================
SPOTTER_LASTFM_API_KEY=your_lastfm_api_key
SPOTTER_LASTFM_SHARED_SECRET=your_lastfm_shared_secret
SPOTTER_LASTFM_REDIRECT_URL=http://localhost:8080/auth/lastfm/callback

# ===================
# Metadata Enrichment
# ===================
SPOTTER_METADATA_ENABLED=true
SPOTTER_METADATA_INTERVAL=1h
SPOTTER_METADATA_ORDER=lidarr,musicbrainz,navidrome,spotify,lastfm,fanart,openai

# MusicBrainz (User-Agent required, no API key needed)
SPOTTER_METADATA_MUSICBRAINZ_USER_AGENT=Spotter/1.0.0 (https://github.com/joestump/spotter)

# Fanart.tv (Optional - for high-quality artwork)
SPOTTER_METADATA_FANART_API_KEY=your_fanart_api_key

# Image Download Settings
SPOTTER_METADATA_IMAGES_DOWNLOAD=true
SPOTTER_METADATA_IMAGES_DIRECTORY=./data/images
SPOTTER_METADATA_IMAGES_MAX_WIDTH=1000
SPOTTER_METADATA_IMAGES_MAX_HEIGHT=1000

# ===================
# OpenAI / AI Enrichment
# ===================
SPOTTER_OPENAI_BASE_URL=https://api.openai.com/v1
SPOTTER_OPENAI_MODEL=gpt-4o
SPOTTER_METADATA_AI_PROMPTS_DIRECTORY=./data/prompts
```
