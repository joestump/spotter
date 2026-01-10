# Lidarr Enricher

## Overview

The Lidarr enricher integrates with Lidarr, a music collection manager for Usenet and BitTorrent users. It enriches metadata from Lidarr's database and automatically adds artists to Lidarr for monitoring and downloading. Lidarr acts as an automated music library manager, similar to Sonarr for TV shows or Radarr for movies.

## Features

- **Implements**: `ArtistEnricher`, `AlbumEnricher`, `TrackEnricher`
- **Data provided**:

  - **Artists**: Lidarr ID, MusicBrainz ID, biography, genres, Lidarr status, monitoring status
  - **Albums**: Lidarr ID, MusicBrainz ID, year, album type, release date, availability status
  - **Tracks**: Lidarr ID, availability status, file presence, quality profile
  - **Artist Management**: Automatically adds artists to Lidarr for monitoring
  - **Status Tracking**: Reports if media is available, missing, or wanted
  - **Release Monitoring**: Tracks upcoming and past releases

## Configuration

### Required Settings

```yaml
lidarr:
  base_url: "<http://localhost:8686">
  api_key: "your-api-key"

```

**Environment Variables** (alternative):

```bash
LIDARR_BASE_URL=<http://localhost:8686>
LIDARR_API_KEY=your-api-key

```

### Configuration Notes

- **Base URL**: The URL where your Lidarr instance is accessible
- **API Key**: Required for all Lidarr API operations
- **No User Auth**: Global configuration, not per-user
- **Quality Profile**: Optional, defaults to first available profile
- **Root Folder**: Optional, defaults to first configured root folder
- **Monitoring**: Artists added with monitoring enabled by default

### Optional Settings

```yaml
lidarr:
  base_url: "<http://localhost:8686">
  api_key: "your-api-key"
  quality_profile_id: 1  # Optional: specific quality profile
  root_folder: "/music"  # Optional: specific root folder path
  monitor_new_items: true  # Default: true

```

## How to Get API Keys

1. **Open Lidarr Web Interface**

   - Navigate to: `<http://localhost:8686`>
   - Or your custom URL if not using default port

2. **Go to Settings**

   - Click on Settings (gear icon) in the left sidebar
   - If Settings isn't visible, click "Show Advanced" at the top

3. **Navigate to General**

   - Click on "General" tab in Settings

4. **Security Section**

   - Scroll down to the "Security" section
   - Find "API Key" field

5. **Copy API Key**

   - Click the "Show" button next to API Key
   - Copy the displayed key (long alphanumeric string)
   - Paste into your Spotter configuration

6. **Save Configuration**

   - Add to Spotter's config file
   - Restart Spotter if necessary

**Note**: API key is sensitive - treat it like a password. Anyone with this key has full access to your Lidarr instance.

## Lidarr Integration

### What is Lidarr?

Lidarr is an automated music collection manager:

- **Monitors**: Tracks artists and their releases
- **Searches**: Finds music on Usenet and torrent indexers
- **Downloads**: Automatically grabs new releases
- **Organizes**: Renames and moves files to proper locations
- **Upgrades**: Replaces lower quality files with better versions

### How Spotter Uses Lidarr

1. **Metadata Source**: Enriches artist/album/track info from Lidarr's database
2. **Auto-Add Artists**: Adds artists to Lidarr for monitoring when enriching
3. **Status Tracking**: Reports availability (available, missing, wanted)
4. **Release Monitoring**: Tracks which albums are monitored
5. **Quality Info**: Reports file quality and profiles

### Artist Management

When enriching an artist not in Lidarr:

1. Searches Lidarr's MusicBrainz cache
2. Adds artist to Lidarr automatically
3. Uses configured quality profile
4. Uses configured root folder
5. Enables monitoring by default

**Flow**:

```text
Artist Enrichment Request
  ↓
Artist in Lidarr? → Yes → Return Lidarr metadata
  ↓ No
Search MusicBrainz via Lidarr
  ↓
Found? → Yes → Add to Lidarr → Return metadata
  ↓ No
Return nil (not found)

```

### Quality Profiles

Quality profiles define acceptable quality levels:

**Common Profiles**:

- **Any**: Accept any quality
- **Lossless**: FLAC only
- **High Quality**: FLAC or 320kbps MP3
- **Standard**: 256kbps or better

**Configuration**:

- Set in Lidarr: Settings → Profiles → Quality Profiles
- Spotter uses first profile if not configured
- Override with `quality_profile_id` in config

### Root Folders

Root folders define where music is stored:

**Example Structure**:

```text
/music/
  ├── Artist Name/
  │   ├── Album Name (Year)/
  │   │   ├── 01 - Track Name.flac
  │   │   ├── 02 - Track Name.flac
  │   │   └── cover.jpg

```

**Configuration**:

- Set in Lidarr: Settings → Media Management → Root Folders
- Spotter uses first root folder if not configured
- Override with `root_folder` in config

### Album Types

Lidarr tracks different release types:

- **Album**: Full-length studio album
- **EP**: Extended play
- **Single**: Single track release
- **Compilation**: Greatest hits, compilations
- **Live**: Live recordings
