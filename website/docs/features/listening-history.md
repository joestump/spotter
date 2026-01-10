---
sidebar_position: 1
---

# Listening History

Spotter aggregates your listening history from multiple sources into a unified view.

## Supported Sources

- **Navidrome**: Primary music server, automatically synced via Subsonic API
- **Spotify**: Recently played tracks (limited to last 50 due to API constraints)
- **Last.fm**: Full scrobble history with unlimited historical data

## How It Works

1. **Connect Services**: Link your Spotify and Last.fm accounts from the Preferences page
2. **Automatic Sync**: Spotter periodically syncs listening history from all connected services
3. **Unified View**: View all your listens in chronological order, regardless of source
4. **Real-time Updates**: New listens are pushed to the UI via Server-Sent Events (SSE)

## Sync Configuration

Control how often Spotter syncs your listening history:

```bash
# Sync every 5 minutes (default)
SPOTTER_SYNC_INTERVAL=5m

# Sync every 15 minutes
SPOTTER_SYNC_INTERVAL=15m

# Sync every hour
SPOTTER_SYNC_INTERVAL=1h
```

## Manual Sync

You can trigger a manual sync from the Preferences > Tasks page:

1. Navigate to **Preferences** > **Tasks**
2. Click **Sync All Listens**
3. Wait for the sync to complete

## Data Deduplication

Spotter automatically deduplicates listens based on:

- Track name
- Artist name
- Timestamp (within a small window)

This prevents duplicate entries when the same track is recorded by multiple services.

## Limitations

### Spotify

Spotify's API only provides access to your 50 most recently played tracks. For comprehensive history tracking, we recommend also connecting Last.fm.

### Last.fm

Last.fm provides unlimited historical access but requires a separate account. Scrobbles from Navidrome can be sent to Last.fm using tools like [multi-scrobbler](https://github.com/FoxxMD/multi-scrobbler).

## Viewing History

The listening history is displayed on the main dashboard with:

- Album artwork
- Track name and artist
- Timestamp
- Source indicator (Navidrome, Spotify, or Last.fm)

Click on any track to view more details or navigate to the artist/album page.
