---
sidebar_position: 1
---

# Navidrome Provider

Navidrome is the primary music server integration for Spotter. All other features build on top of your Navidrome library.

## Overview

The Navidrome provider:

- Uses your existing Navidrome credentials for authentication
- Syncs your music library (artists, albums, tracks)
- Syncs playlists from Navidrome
- Provides the target for playlist syncing from other services
- Uses the Subsonic API for all operations

## Configuration

| Variable | Description | Required |
| :--- | :--- | :--- |
| `SPOTTER_NAVIDROME_BASE_URL` | URL of your Navidrome instance | Yes |

```bash
SPOTTER_NAVIDROME_BASE_URL=https://music.example.com
```

## Authentication

Spotter uses Navidrome's authentication system:

1. Open Spotter in your browser
2. Enter your Navidrome username and password
3. Spotter authenticates against Navidrome via the Subsonic API
4. A signed JWT token is issued and stored in a secure cookie
5. The token is valid for 24 hours

No separate API keys are required - your Navidrome credentials are sufficient.

:::info
Your Navidrome password is stored encrypted for background sync operations. See [Authentication](/docs/api/authentication) for details on session security.
:::

## Synced Data

### Library

- **Artists**: Name, images, metadata
- **Albums**: Title, artwork, release date
- **Tracks**: Title, duration, audio files

### Playlists

- All playlists from your Navidrome library
- Track listings and order
- Playlist metadata

### Listening History

- Recent plays from Navidrome
- Play counts and timestamps

## Subsonic API

Spotter uses the Subsonic API for communication with Navidrome. This is the same API used by mobile apps like DSub and Symfonium.

### Supported Operations

- User authentication
- Library browsing
- Playlist management
- Playback (for metadata)
- Scrobbling (optional)

## Troubleshooting

### Connection Failed

1. Verify `SPOTTER_NAVIDROME_BASE_URL` is correct
2. Ensure Navidrome is running and accessible
3. Check for HTTPS/certificate issues
4. Verify no firewall blocking

### Authentication Failed

1. Verify your Navidrome credentials
2. Check that the user has API access enabled
3. Try logging into Navidrome web UI directly

### Missing Library Items

1. Trigger a library rescan in Navidrome
2. Wait for Spotter's next sync cycle
3. Or manually sync from Preferences > Tasks

## Network Configuration

### Docker

If running Spotter in Docker and Navidrome is on the host:

```bash
# Docker Desktop (macOS/Windows)
SPOTTER_NAVIDROME_BASE_URL=http://host.docker.internal:4533

# Linux with host networking
SPOTTER_NAVIDROME_BASE_URL=http://localhost:4533
```

### Reverse Proxy

When using a reverse proxy (nginx, Traefik, etc.), ensure:

- The proxy passes all Subsonic API endpoints
- WebSocket connections are supported (for SSE)
- Proper host headers are forwarded
