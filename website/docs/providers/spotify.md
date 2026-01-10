---
sidebar_position: 2
---

# Spotify Provider

The Spotify provider enables syncing your Spotify listening history and playlists with Spotter.

## Features

- Sync recently played tracks (last 50)
- Import Spotify playlists
- Sync playlists to Navidrome with track matching
- OAuth 2.0 authentication with automatic token refresh

## Limitations

:::caution Important Limitation
Spotify's API only provides access to your **50 most recently played tracks**. For comprehensive listening history, we recommend also connecting [Last.fm](/docs/providers/lastfm).
:::

## Setup

### 1. Create Spotify App

1. Go to the [Spotify Developer Dashboard](https://developer.spotify.com/dashboard)
2. Log in with your Spotify account
3. Click **Create App**
4. Fill in the app details:
   - **App name**: Spotter (or any name)
   - **App description**: Personal music tracking app
   - **Redirect URI**: `http://localhost:8080/auth/spotify/callback`
   - Check the **Web API** checkbox
5. Click **Save**
6. Click **Settings** on your app's dashboard
7. Copy the **Client ID** and **Client Secret**

### 2. Configure Spotter

Add these to your `.env` file:

```bash
SPOTTER_SPOTIFY_CLIENT_ID=your_client_id_here
SPOTTER_SPOTIFY_CLIENT_SECRET=your_client_secret_here
SPOTTER_SPOTIFY_REDIRECT_URL=http://localhost:8080/auth/spotify/callback
```

### 3. Connect Account

1. Open Spotter and go to **Preferences** > **Services**
2. Click **Connect Spotify**
3. Authorize Spotter in the Spotify popup
4. You'll be redirected back to Spotter

## Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_SPOTIFY_CLIENT_ID` | Spotify Client ID | *Required* |
| `SPOTTER_SPOTIFY_CLIENT_SECRET` | Spotify Client Secret | *Required* |
| `SPOTTER_SPOTIFY_REDIRECT_URL` | OAuth callback URL | `http://127.0.0.1:8080/auth/spotify/callback` |

## Synced Data

### Listening History

- Last 50 recently played tracks
- Play timestamps
- Track metadata

### Playlists

- All your Spotify playlists
- Track listings
- Playlist images and descriptions

## Playlist Syncing

Spotify playlists can be synced to your Navidrome library:

1. Go to any Spotify playlist in Spotter
2. Enable **Sync to Navidrome**
3. Spotter matches tracks to your Navidrome library
4. A new playlist is created in Navidrome
5. Updates automatically when the Spotify playlist changes

### Track Matching

Tracks are matched using:

1. **ISRC**: International Standard Recording Code (most reliable)
2. **Exact Match**: Artist + track name
3. **Fuzzy Match**: Similar names with confidence threshold

## Token Management

Spotter automatically handles OAuth tokens:

- **Access tokens** are refreshed automatically
- **Refresh tokens** are stored securely in the database
- **Disconnecting** removes all stored tokens

## Troubleshooting

### "Invalid redirect URI"

The redirect URI in your Spotify app must **exactly match** `SPOTTER_SPOTIFY_REDIRECT_URL`.

Common issues:
- Missing trailing slash
- HTTP vs HTTPS mismatch
- Different port number

### "Access token expired"

Spotter should automatically refresh tokens. If issues persist:

1. Disconnect Spotify from Preferences
2. Reconnect your account

### Missing Playlists

Only playlists you own or follow are synced. Collaborative playlists may have limited access depending on permissions.

### Rate Limiting

Spotify has rate limits. If you see 429 errors:

1. Wait a few minutes
2. Spotter will automatically retry
3. Consider increasing sync interval

## Production Deployment

For production use:

1. Update the redirect URI in your Spotify app settings
2. Update `SPOTTER_SPOTIFY_REDIRECT_URL` to match your production domain
3. Ensure HTTPS is configured
