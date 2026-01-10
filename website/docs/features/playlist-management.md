---
sidebar_position: 2
---

# Playlist Management

Spotter provides comprehensive playlist management with sync capabilities across services.

## Overview

- View playlists from Navidrome, Spotify, and Last.fm in one place
- Sync external playlists to your Navidrome library
- AI-powered playlist enhancement with the Vibes Engine

## Playlist Syncing

Sync playlists from Spotify or Last.fm to your Navidrome library:

1. Navigate to any non-Navidrome playlist
2. Toggle **Sync to Navidrome** to enable syncing
3. Spotter will:
   - Match tracks from the source playlist to your Navidrome library
   - Create a playlist in Navidrome with the matched tracks
   - Periodically update the playlist when the source changes

## Track Matching

Tracks are matched using these strategies (in order of priority):

1. **ISRC Match**: If available, matches by International Standard Recording Code
2. **Exact Match**: Matches by exact track name and artist (case-insensitive)
3. **Fuzzy Match**: Matches similar track names with configurable confidence threshold

### Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_PLAYLIST_SYNC_MIN_MATCH_CONFIDENCE` | Minimum confidence for fuzzy matching (0.0-1.0) | `0.8` |
| `SPOTTER_PLAYLIST_SYNC_INCLUDE_UNMATCHED_TRACKS` | Include unmatched tracks as placeholders | `false` |

## Sync Status

The playlist detail page shows sync status with a 5-state indicator:

| State | Label | Color | Condition |
| :--- | :--- | :--- | :--- |
| **Error** | "Sync Error" | Red | Sync encountered an error |
| **Pending** | "Syncing..." | Blue | Initial sync in progress |
| **Neutral** | "Not Synced" | Gray | Nothing to sync or sync disabled |
| **Success** | "Fully Synced" | Green | All tracks matched |
| **Warning** | "Partial Sync" | Orange | Some tracks couldn't be matched |

## Sync Actions

From the playlist dropdown menu:

- **Sync Now**: Trigger an immediate sync
- **Rebuild**: Delete Navidrome playlist and re-sync from scratch
- **Disable**: Stop syncing this playlist

## Configuration

```bash
# Sync interval for enabled playlists
SPOTTER_PLAYLIST_SYNC_SYNC_INTERVAL=1h

# Delete Navidrome playlist when sync is disabled
SPOTTER_PLAYLIST_SYNC_DELETE_ON_UNSYNC=false
```

## API Endpoints

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/playlists/{id}/toggle-sync` | POST | Enable/disable sync |
| `/playlists/{id}/sync` | POST | Trigger async sync |
| `/playlists/{id}/rebuild-sync` | POST | Delete and re-sync |
| `/playlists/{id}/sync-status` | GET | Get sync status as JSON |

## Sync Events

All sync operations are logged to the `sync_events` table:

- `playlist_sync_started` - Sync operation initiated
- `playlist_sync_completed` - Sync completed successfully
- `playlist_sync_failed` - Sync failed with error details
- `playlist_sync_removed` - Playlist was removed from Navidrome
