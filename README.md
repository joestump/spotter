# Spotter

Spotter is an AI-powered playlist generator for Navidrome. It aggregates your listening history from various sources (Navidrome, Spotify, Last.fm) and uses that data to generate personalized playlists. AI-powered metadata enrichment generates intelligent summaries, biographies, and tags for your music library.

## Features

* **Unified Listening History**: Syncs recent listens from Navidrome, Spotify, and Last.fm into a single view with pagination.
* **Playlist Management**: View and sync playlists from all connected services.
* **Vibes Engine**: AI-powered mixtape generation with customizable DJ personas that curate playlists based on your listening history.
* **Navidrome Integration**: Log in using your existing Navidrome credentials.
* **External Service Support**: Connect your Spotify and Last.fm accounts to import history and improve recommendations.
* **Metadata Enrichment**: Automatically enriches artist, album, and track metadata from MusicBrainz, Fanart.tv, Spotify, Last.fm, and more.
* **AI-Powered Enrichment**: Optional OpenAI integration generates intelligent summaries, biographies, and tags for artists, albums, and tracks. AI-generated content is clearly marked in the UI.
* **Real-time Updates**: Server-Sent Events (SSE) push new listens and sync notifications to the UI automatically.
* **Retro-Themed UI**: Custom-designed themes featuring a warm 1970s music cabinet aesthetic (light mode) and an 1980s cyberpunk vibe (dark mode).
* **AI-Powered**: Customizable AI system prompts for personalized playlist generation.
* **Pluggable Architecture**: Easily extensible sync framework for adding more music providers.
* **Background Sync**: Configurable automatic synchronization of listening history and playlists.

## Getting Started

### Prerequisites

* Go 1.23+
* Node.js & npm (for Tailwind CSS generation)
* Make

### Installation

1. Clone the repository:

   ```bash
   git clone https://github.com/joestump/spotter.git
   cd spotter
   ```

2. Install dependencies:

   ```bash
   make deps
   ```

3. Build and run locally:

   ```bash
   make run
   ```

   The server will start at `http://localhost:8080`.

### Docker

You can also run Spotter using Docker:

```bash
make docker-build
docker run -p 8080:8080 --env-file .env spotter
```

## Configuration

Spotter is configured using environment variables. You can set these in your shell or use a `.env` file.

### Server Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_SERVER_PORT` | The HTTP port the server listens on. | `8080` |
| `SPOTTER_SERVER_HOST` | The host address to bind to. | `0.0.0.0` |

### Database Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_DATABASE_DRIVER` | Database driver (`sqlite3`, `postgres`, `mysql`). | `sqlite3` |
| `SPOTTER_DATABASE_SOURCE` | Connection string for the database. | `file:spotter.db?cache=shared&_fk=1` |

### Sync Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_SYNC_INTERVAL` | How often to sync data from providers (Go duration format). | `5m` |

### Playlist Sync Configuration

Spotter can sync playlists from external sources (Spotify, Last.fm) to your Navidrome library.

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_PLAYLIST_SYNC_SYNC_INTERVAL` | How often to sync enabled playlists to Navidrome (Go duration format). | `1h` |
| `SPOTTER_PLAYLIST_SYNC_DELETE_ON_UNSYNC` | Delete Navidrome playlist when sync is disabled. | `false` |
| `SPOTTER_PLAYLIST_SYNC_MIN_MATCH_CONFIDENCE` | Minimum confidence for fuzzy track matching (0.0-1.0). | `0.8` |
| `SPOTTER_PLAYLIST_SYNC_INCLUDE_UNMATCHED_TRACKS` | Include unmatched tracks as placeholders. | `false` |

#### How Playlist Syncing Works

1. Navigate to any non-Navidrome playlist (e.g., from Spotify or Last.fm)
2. Toggle "Sync to Navidrome" to enable syncing
3. Spotter will:
   * Match tracks from the source playlist to your Navidrome library
   * Create a playlist in Navidrome with the matched tracks
   * Periodically update the playlist when the source changes

#### Track Matching

Tracks are matched using the following strategies (in order of priority):

1. **ISRC Match**: If available, matches by International Standard Recording Code
2. **Exact Match**: Matches by exact track name and artist (case-insensitive)
3. **Fuzzy Match**: Matches similar track names with configurable confidence threshold

Tracks that cannot be matched to your Navidrome library will be skipped (or included as placeholders if configured).

#### Sync Status UI

The playlist detail page displays a dropdown button showing the current sync state. Detailed sync information (last synced timestamp, progress bar, and match statistics) is consolidated within the dropdown menu to provide a cleaner header experience. The UI uses a unified **5-state logic** defined in `sync_status.templ`:

| State | Label | Color | Condition |
| :--- | :--- | :--- | :--- |
| **Error** | "Sync Error" | Red | `SyncError != ""` |
| **Pending** | "Syncing..." | Blue | `SyncEnabled == true` AND `NavidromeID == ""` (initial sync in progress) |
| **Neutral** | "Not Synced" / "Sync Disabled" | Gray | `TotalTracks == 0` (nothing to sync) OR `SyncEnabled == false` |
| **Success** | "Fully Synced" | Green | `MatchedTracks == TotalTracks` AND `TotalTracks > 0` |
| **Warning** | "Partial Sync" | Orange | `MatchedTracks < TotalTracks` (includes 0 matches when sync has completed) |

**Important:** The Warning state includes the case where 0 tracks are matched but the sync process has completed (i.e., `NavidromeID` is set). This distinguishes between "sync in progress" (Pending/Blue) and "sync completed but nothing matched" (Warning/Orange).

When the dropdown menu is expanded, users can see:

* Last synced timestamp with timeago formatting
* A compact progress bar showing track match statistics
* Available sync actions (Sync Now, Rebuild, Disable)

The progress bar automatically polls for updates every 5 seconds **only** while in the "Pending" state. Polling stops once a final state (Success, Warning, or Error) is reached.

#### Sync Events

Playlist sync operations are logged to the `sync_events` table for auditing and debugging. Event types include:

* `playlist_sync_started` - Sync operation initiated
* `playlist_sync_completed` - Sync completed successfully (includes track match stats)
* `playlist_sync_failed` - Sync failed with error details
* `playlist_sync_removed` - Playlist was removed from Navidrome

#### Playlist Sync API

Spotter provides endpoints for managing playlist sync:

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/playlists/{id}/toggle-sync` | POST | Enables or disables sync to Navidrome |
| `/playlists/{id}/sync` | POST | Triggers an immediate async sync to Navidrome |
| `/playlists/{id}/rebuild-sync` | POST | Deletes Navidrome playlist and re-syncs from scratch |
| `/playlists/{id}/sync-status` | GET | Returns current sync status as JSON |
| `/playlists/{id}/sync-progress` | GET | Returns sync progress bar component (for HTMX polling) |
| `/playlists/{id}/debug-sync` | POST | Triggers synchronous sync and returns detailed results |

Example debug sync response:

```json
{
  "playlist_id": 5,
  "playlist_name": "Discover Weekly",
  "source": "spotify",
  "success": true,
  "navidrome_playlist_id": "abc123",
  "matched_track_count": 25,
  "total_track_count": 30,
  "duration_ms": 1250
}
```

#### UI Notifications

Toast notifications appear in the UI during sync operations:

* **Info**: "Syncing Playlist" - when sync starts
* **Success**: "Playlist Synced" - with track match count
* **Error**: "Playlist Sync Failed" - with error message
* **Warning**: "Rebuilding Playlist" - when rebuild starts
* **Info**: "Playlist Removed" - when unsynced from Navidrome

### Theme Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_THEME_AVAILABLE` | Comma-separated list of available DaisyUI theme names. | `light,dark,cupcake` |
| `SPOTTER_THEME_DEFAULT` | Default theme for new users. | `dark` |

### Provider Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_NAVIDROME_BASE_URL` | **Required.** The URL of your Navidrome instance. | *None* |
| `SPOTTER_OPENAI_API_KEY` | **Required.** OpenAI API key for AI-powered metadata enrichment. | *None* |
| `SPOTTER_SPOTIFY_CLIENT_ID` | Spotify Client ID for API access. | *None* |
| `SPOTTER_SPOTIFY_CLIENT_SECRET` | Spotify Client Secret. | *None* |
| `SPOTTER_SPOTIFY_REDIRECT_URL` | OAuth callback URL for Spotify. | `http://127.0.0.1:8080/auth/spotify/callback` |
| `SPOTTER_LASTFM_API_KEY` | Last.fm API Key. | *None* |
| `SPOTTER_LASTFM_SHARED_SECRET` | Last.fm Shared Secret. | *None* |
| `SPOTTER_LASTFM_REDIRECT_URL` | Callback URL for Last.fm auth. | *None* |

### Metadata Enrichment Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_METADATA_ENABLED` | Enable/disable metadata enrichment. | `true` |
| `SPOTTER_METADATA_INTERVAL` | How often to run metadata enrichment (Go duration format). | `1h` |
| `SPOTTER_METADATA_ORDER` | Comma-separated enricher priority order. | `lidarr,musicbrainz,navidrome,spotify,lastfm,fanart,openai` |

#### Lidarr Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_LIDARR_BASE_URL` | Base URL to your Lidarr instance (e.g., `http://localhost:8686`). | *None* |
| `SPOTTER_LIDARR_API_KEY` | API Key for your Lidarr instance. | *None* |

#### OpenAI Configuration (AI Enrichment)

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_OPENAI_BASE_URL` | Base URL for OpenAI API (use for LiteLLM or compatible proxies). | `https://api.openai.com/v1` |
| `SPOTTER_OPENAI_MODEL` | Model to use for AI enrichment. | `gpt-4o` |
| `SPOTTER_METADATA_AI_PROMPTS_DIRECTORY` | Directory containing prompt templates. | `./data/prompts` |

#### MusicBrainz Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_METADATA_MUSICBRAINZ_USER_AGENT` | User-Agent string for MusicBrainz API requests (required by their API). | `Spotter/1.0.0 (https://github.com/joestump/spotter)` |

#### Fanart.tv Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_METADATA_FANART_API_KEY` | Fanart.tv personal API key for fetching artist/album artwork. | *None* |

#### Image Download Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_METADATA_IMAGES_DOWNLOAD` | Whether to download images locally. | `true` |
| `SPOTTER_METADATA_IMAGES_DIRECTORY` | Directory to store downloaded images. | `./data/images` |
| `SPOTTER_METADATA_IMAGES_MAX_WIDTH` | Maximum image width (for resizing). | `1000` |
| `SPOTTER_METADATA_IMAGES_MAX_HEIGHT` | Maximum image height (for resizing). | `1000` |

### Vibes Engine Configuration

The Vibes Engine enables AI-powered mixtape generation. Create DJ personas with unique personalities that curate playlists based on your listening history and preferences.

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_VIBES_DEFAULT_MAX_TRACKS` | Default maximum tracks per mixtape. | `25` |
| `SPOTTER_VIBES_MIN_TRACKS` | Minimum tracks for a valid mixtape. | `5` |
| `SPOTTER_VIBES_MAX_TRACKS` | Maximum allowed tracks (hard limit). | `100` |
| `SPOTTER_VIBES_HISTORY_DAYS` | Days of listening history to include in context. | `30` |
| `SPOTTER_VIBES_MAX_HISTORY_TRACKS` | Maximum history tracks to include in prompt. | `50` |
| `SPOTTER_VIBES_MODEL` | AI model for mixtape generation (overrides `openai.model`). | *Falls back to `openai.model`* |
| `SPOTTER_VIBES_TEMPERATURE` | AI temperature for generation (0.0-2.0). | `0.8` |
| `SPOTTER_VIBES_MAX_TOKENS` | Maximum tokens for AI response. | `4000` |
| `SPOTTER_VIBES_TIMEOUT_SECONDS` | Timeout for AI generation requests. | `120` |
| `SPOTTER_VIBES_PROMPTS_DIRECTORY` | Directory for vibes prompt templates. | *Falls back to metadata prompts directory* |
| `SPOTTER_VIBES_MIN_MATCH_CONFIDENCE` | Minimum confidence for track matching. | `0.7` |

#### How the Vibes Engine Works

1. **DJ Personas**: Create DJs with unique personalities, genre preferences, and artists to include/exclude.
2. **Mixtapes**: Create mixtapes assigned to a DJ with optional scheduling (daily, weekly, monthly regeneration).
3. **Generation**: The AI uses the DJ's persona, your listening history, and available library tracks to curate a personalized mixtape.
4. **Track Matching**: AI-suggested tracks are fuzzy-matched against your Navidrome library to ensure playability.
5. **Sync to Navidrome**: Optionally sync generated mixtapes as playlists to Navidrome.

#### Prompt Templates

The Vibes Engine uses prompt templates stored in `data/prompts/generate_mixtape.tmpl`. You can customize this template to adjust how the AI generates mixtapes without recompiling.

#### Seed Data

Mixtapes can be seeded with:

* **Artist**: Generate a mixtape inspired by a specific artist
* **Album**: Generate a mixtape based on an album's vibe
* **Tracks**: Generate a mixtape starting from specific seed tracks

#### Playlist Enhancement (Enhance Vibes)

The "Enhance Vibes" feature allows you to use DJ personas to improve existing playlists:

1. **Reordering**: The DJ analyzes your playlist's tracks and reorders them for better flow (energy builds, mood transitions, tempo progression).
2. **Track Additions**: The DJ suggests new tracks from your library that complement the existing selection.
3. **Guarantee**: Your original tracks are **never removed** - only reordered and augmented.

**Enhancement Modes:**

| Mode | Description |
| :--- | :--- |
| **One-time Enhance** | Apply changes directly to Navidrome. The playlist remains manually editable afterward. |
| **Convert to Mixtape** | The DJ takes over the playlist. It becomes a Mixtape that can be regenerated and scheduled. |

**Prompt Template:**

The enhancement uses `data/prompts/enhance_playlist.tmpl` which can be customized to adjust how the AI enhances playlists.

### Similar Artists (AI-Powered Discovery)

The Similar Artists feature uses AI to find artists in your library that are musically related to a given artist. This helps you discover connections within your own music collection.

#### How It Works

1. **AI Analysis**: When you click "Find Similar Artists" on an artist's page, the system sends the artist's metadata (genres, tags, biography) along with a list of all artists in your library to OpenAI.
2. **Library-Only Recommendations**: The AI is instructed to ONLY recommend artists that exist in your library—no hallucinated artists.
3. **Confidence Scoring**: Each recommendation includes a confidence score (0.0-1.0) indicating how similar the artists are.
4. **Explanations**: The AI provides a brief explanation for why each artist is similar.

#### Similar Artist Features

* **Provider Tracking**: Each similarity recommendation is tagged with its source (e.g., "OpenAI", "LastFM") so you know where it came from.
* **Visual Indicators**: Similar artist tiles show an AI badge and confidence score.
* **Hover Explanations**: Hover over a similar artist tile to see why they're recommended.
* **Refresh on Demand**: Re-run the similarity analysis anytime to get fresh recommendations.

#### Prompt Template

The similarity analysis uses the prompt template at `data/prompts/enrich_artist.txt`. You can customize this template to adjust how the AI finds similar artists.

#### Create Mixtape from Artist

From any artist's page, you can create a mixtape inspired by that artist:

1. Click the **AI** dropdown menu on the artist page
2. Select **Create Mixtape**
3. Choose a DJ persona to curate the mixtape
4. Optionally customize the name and track count
5. Click **Create Mixtape** to start generation

The mixtape will be seeded with the artist, and the DJ's personality will influence track selection from your library.

## Service Documentation

Each provider and enricher has its own comprehensive README with detailed setup instructions, configuration options, and troubleshooting guides.

### Providers (Listening History Sources)

Providers sync your listening history from external music services:

* **[Last.fm Provider](internal/providers/lastfm/README.md)** - Scrobble history, full listening history
  * MD5 authentication flow
  * No API key expiration
  * Unlimited historical data
* **[Spotify Provider](internal/providers/spotify/README.md)** - Recently played tracks (last 50), playlists
  * OAuth2 authentication
  * Automatic token refresh
  * 50-track history limitation
* **[Navidrome Provider](internal/providers/navidrome/)** - Primary music server integration
  * Subsonic API
  * Library management
  * Playlist syncing

### Enrichers (Metadata Enhancement)

Enrichers enhance your music metadata from various sources:

* **[MusicBrainz Enricher](internal/enrichers/musicbrainz/README.md)** - Open music database (no API key required)
  * Artist, album, track metadata
  * MusicBrainz IDs (MBIDs)
  * Cover Art Archive integration
  * Rate limit: 1 request/second
* **[Spotify Enricher](internal/enrichers/spotify/README.md)** - Audio features and metadata
  * BPM, key, energy, danceability
  * Popularity scores
  * High-resolution artwork
  * Requires user authentication
* **[Fanart.tv Enricher](internal/enrichers/fanart/README.md)** - High-quality images
  * HD artist logos
  * Album artwork
  * Backgrounds and banners
  * Requires API key
* **[Last.fm Enricher](internal/enrichers/lastfm/)** - Community metadata
  * Artist biographies
  * Tags and genres
  * Album information
* **[Navidrome Enricher](internal/enrichers/navidrome/)** - Local server metadata
  * Subsonic API integration
  * Cover art URLs
  * Library information
* **[Lidarr Enricher](internal/enrichers/lidarr/)** - Music collection manager
  * Automated organization
  * Quality profiles
  * Release tracking
* **[OpenAI Enricher](internal/enrichers/openai/)** - AI-powered enrichment
  * Intelligent summaries
  * Generated biographies
  * Smart tagging

### Quick Setup Guide

For detailed setup instructions for each service, see the individual README files linked above. Each README includes:

* Step-by-step API key acquisition
* Configuration examples
* Authentication flows
* Rate limits and limitations
* Troubleshooting guides
* Example usage code

## Quick Start: Obtaining API Keys

For quick reference, here's a brief overview of how to obtain API keys for each service. **For detailed instructions, see the individual service READMEs linked above.**

### Spotify (Optional)

Spotify integration enables syncing your recent listening history and enriching metadata with Spotify's extensive database.

1. Go to the [Spotify Developer Dashboard](https://developer.spotify.com/dashboard)
2. Log in with your Spotify account
3. Click **Create App**
4. Fill in the app details:
   * **App name**: Spotter (or any name you prefer)
   * **App description**: Personal music tracking app
   * **Redirect URI**: `http://localhost:8080/auth/spotify/callback` (or your production URL)
   * Check the **Web API** checkbox
5. Click **Save**
6. On your app's dashboard, click **Settings**
7. Copy the **Client ID** and **Client Secret**

```bash
SPOTTER_SPOTIFY_CLIENT_ID=your_client_id_here
SPOTTER_SPOTIFY_CLIENT_SECRET=your_client_secret_here
SPOTTER_SPOTIFY_REDIRECT_URL=http://localhost:8080/auth/spotify/callback
```

### Last.fm (Optional)

Last.fm integration enables syncing your scrobble history and enriching metadata with Last.fm's community-driven database.

1. Go to [Last.fm API Account Creation](https://www.last.fm/api/account/create)
2. Log in with your Last.fm account (or create one)
3. Fill in the application form:
   * **Application name**: Spotter
   * **Application description**: Personal music tracking app
   * **Application homepage**: (optional, can leave blank or use your URL)
   * **Callback URL**: `http://localhost:8080/auth/lastfm/callback` (or your production URL)
4. Click **Submit**
5. You'll receive an **API Key** and **Shared Secret**

```bash
SPOTTER_LASTFM_API_KEY=your_api_key_here
SPOTTER_LASTFM_SHARED_SECRET=your_shared_secret_here
SPOTTER_LASTFM_REDIRECT_URL=http://localhost:8080/auth/lastfm/callback
```

### MusicBrainz (No API Key Required)

MusicBrainz is a free, open music encyclopedia that provides metadata enrichment. No API key is required, but you **must** provide a proper User-Agent string that identifies your application.

According to [MusicBrainz API requirements](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting), your User-Agent should include:

* Application name
* Version
* Contact URL or email

```bash
SPOTTER_METADATA_MUSICBRAINZ_USER_AGENT="Spotter/1.0.0 (https://github.com/joestump/spotter)"
```

### Fanart.tv (Optional)

Fanart.tv provides high-quality artist images, album artwork, and other media artwork.

1. Go to [Fanart.tv](https://fanart.tv/)
2. Create an account or log in
3. Go to your [API page](https://fanart.tv/get-an-api-key/)
4. You'll see your **Personal API Key** (also called "api_key")

```bash
SPOTTER_METADATA_FANART_API_KEY=your_api_key_here
```

> **Note**: Fanart.tv has a free tier with rate limits. For personal use, this is typically sufficient.

### Lidarr (Required)

1. Open Lidarr.
2. Go to **Settings** -> **General**.
3. Copy the **API Key** from the Security section.

### OpenAI (Required - AI Enrichment)

OpenAI integration is **required** for Spotter's AI-powered metadata enrichment. It generates summaries, biographies, and intelligent tags for your music library.

1. Go to [OpenAI Platform](https://platform.openai.com/)
2. Sign up or log in to your account
3. Navigate to [API Keys](https://platform.openai.com/api-keys)
4. Click **Create new secret key**
5. Give it a name (e.g., "Spotter") and click **Create**
6. Copy the key immediately (it won't be shown again)

```bash
SPOTTER_OPENAI_API_KEY=sk-your-api-key-here
SPOTTER_OPENAI_MODEL=gpt-4o
```

**Using LiteLLM or Compatible Proxies:**

If you're using LiteLLM or another OpenAI-compatible API proxy, you can configure the base URL:

```bash
SPOTTER_OPENAI_API_KEY=your-proxy-api-key
SPOTTER_OPENAI_BASE_URL=https://your-litellm-instance.com/v1
SPOTTER_OPENAI_MODEL=claude-3-opus  # Or any model your proxy supports
```

**Customizing AI Prompts:**

Spotter uses Go templates for AI prompts. Default prompts are stored in `./data/prompts/`. You can customize these or point to your own directory:

```bash
SPOTTER_METADATA_AI_PROMPTS_DIRECTORY=/path/to/custom/prompts
```

> **For complete setup instructions, configuration options, and troubleshooting, see the [Service Documentation](#service-documentation) section above and individual component READMEs.**

Template files:

* `artist.tmpl` - Prompt for artist enrichment (generates biography, summary, and tags)
* `album.tmpl` - Prompt for album enrichment (generates summary and tags)
* `track.tmpl` - Prompt for track enrichment (generates summary and tags)

> **Note**: AI enrichment uses vision capabilities to analyze album/artist artwork when available. The enricher runs last in the pipeline to have access to all metadata from other enrichers.

### Example `.env` File

Here's a complete example `.env` file with all configuration options:

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

SPOTTER_LIDARR_BASE_URL=http://localhost:8686
SPOTTER_LIDARR_API_KEY=your_lidarr_api_key

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
# OpenAI / AI Enrichment (Configuration)
# ===================
SPOTTER_OPENAI_BASE_URL=https://api.openai.com/v1
SPOTTER_OPENAI_MODEL=gpt-4o
SPOTTER_METADATA_AI_PROMPTS_DIRECTORY=./data/prompts
```

## Development

### Running Tests

To run the unit tests:

```bash
make test
```

### Building CSS

If you make changes to the Tailwind classes in `.templ` files, regenerate the CSS:

```bash
make css
# Or watch for changes
npm run watch:css
```

### Architecture

* **Backend**: Go with `chi` router and Server-Sent Events (SSE) for real-time updates.
* **Database**: SQLite (via `ent` ORM) with automatic migrations.
* **Frontend**: Server-side rendering with `templ` + `HTMX` for interactivity and real-time updates.
* **Styling**: `DaisyUI` + `Tailwind CSS` with custom retro themes and `@iconify/tailwind` for icons.
* **Background Jobs**: Configurable periodic sync for all connected providers.
* **Real-time**: Event Bus + SSE for push notifications and live updates.
* **Metadata Enrichment**: Pluggable enricher system that aggregates data from multiple sources (MusicBrainz, Spotify, Last.fm, Fanart.tv, Navidrome).

## User Features

### Themes

Spotter features two custom-designed retro themes that create an immersive nostalgic experience:

#### 🎵 Light Theme - 1970s Music Cabinet

The light theme evokes the warm, cozy feeling of a vintage 1970s hi-fi system:

* **Color Palette**: Rich amber (#d97706), golden yellow (#f59e0b), and deep brown (#92400e) tones
* **Background**: Cream and beige tones (#fef3c7) reminiscent of wood veneer
* **Visual Effects**:
  * Subtle wood-grain texture overlays
  * Raised beveled borders on cards and buttons
  * Soft inset shadows suggesting depth
  * Warm text shadows for a soft, analog feel
* **Typography**: Bold, slightly spaced lettering with gentle shadows
* **Aesthetic**: Think vintage record players, warm living rooms, and analog warmth

#### ⚡ Dark Theme - 1980s Cyberpunk

The dark theme channels the neon-soaked, digital future imagined in 1980s cyberpunk:

* **Color Palette**: Neon cyan (#00d9ff), magenta (#ff00ff), and electric green (#00ff41)
* **Background**: Deep dark blue-black (#0f0f23) with navy accents
* **Visual Effects**:
  * Scan line overlay across the entire interface
  * Glowing neon borders on all interactive elements
  * Multiple layered box shadows creating depth and glow
  * Text shadows with neon glow effects
  * Icon glow filters for that electric feel
* **Typography**: Sharp, uppercase lettering with wider tracking and neon glow
* **Aesthetic**: Blade Runner meets Tron - dark, electric, and futuristic

**Theme Controls:**

* **Sidebar Toggle**: Temporarily switch themes (persisted in browser localStorage, does not affect database)
* **Preferences > General Tab**: Permanently set your theme preference (Light, Dark, or System)

### Preferences

* **Theme Selection**: Choose between Light, Dark, or System (auto) themes. Your preference is saved to the database.
* **AI System Prompt**: Customize the AI personality for playlist generation.
* **Pagination**: Configure how many items to display per page (10-100).

### Connected Services

* **Navidrome**: Automatically connected via your login credentials. View last sync time.
* **Spotify**: Optional OAuth integration. Connect/disconnect from Preferences. View last sync time.
* **Last.fm**: Optional OAuth integration. Connect/disconnect from Preferences. View last sync time and username.

### Background Tasks

From the Preferences > Tasks page, you can manually run or monitor:

* **Sync All Listens**: Pull recent listening history from all connected providers
* **Sync All Playlists**: Pull playlist data from all connected providers
* **Run Metadata Enricher**: Enrich artist, album, and track metadata from external sources
* **Sync All Artist Images**: Re-fetch artist images from all connected providers
* **Sync All Album Art**: Re-fetch album artwork from all connected providers
* **Reset All Data**: Delete all data and re-sync from scratch
* **Clear Caches & Cleanup**: Delete old events and perform maintenance tasks

### Real-time Features

* Live updates for new listens via SSE
* Toast notifications for sync events
* Automatic background synchronization every 5 minutes (configurable)
* No manual refresh buttons needed

## License

MIT
