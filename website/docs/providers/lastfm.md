---
sidebar_position: 3
---

# Last.fm Provider

The Last.fm provider enables syncing your complete scrobble history with Spotter.

## Features

- **Unlimited historical data**: Access your full scrobbling history
- **No expiration**: API keys don't expire
- **Community metadata**: Access to tags, biographies, and more
- **MD5 authentication**: Simple, secure authentication flow

## Why Last.fm?

Unlike Spotify's 50-track limit, Last.fm provides access to your **entire scrobble history**. This makes it the recommended choice for comprehensive listening history tracking.

## Setup

### 1. Create Last.fm Application

1. Go to [Last.fm API Account Creation](https://www.last.fm/api/account/create)
2. Log in with your Last.fm account
3. Fill in the application form:
   - **Application name**: Spotter
   - **Application description**: Personal music tracking app
   - **Application homepage**: (optional)
   - **Callback URL**: `http://localhost:8080/auth/lastfm/callback`
4. Click **Submit**
5. Copy the **API Key** and **Shared Secret**

### 2. Configure Spotter

Add to your `.env` file:

```bash
SPOTTER_LASTFM_API_KEY=your_api_key_here
SPOTTER_LASTFM_SHARED_SECRET=your_shared_secret_here
SPOTTER_LASTFM_REDIRECT_URL=http://localhost:8080/auth/lastfm/callback
```

### 3. Connect Account

1. Open Spotter and go to **Preferences** > **Services**
2. Click **Connect Last.fm**
3. Authorize Spotter on Last.fm
4. You'll be redirected back to Spotter

## Configuration

| Variable | Description | Required |
| :--- | :--- | :--- |
| `SPOTTER_LASTFM_API_KEY` | Last.fm API Key | Yes |
| `SPOTTER_LASTFM_SHARED_SECRET` | Last.fm Shared Secret | Yes |
| `SPOTTER_LASTFM_REDIRECT_URL` | OAuth callback URL | Yes |

## Synced Data

### Listening History

- Full scrobble history (all time)
- Play timestamps
- Track metadata

### Additional Data

- Artist tags
- Album information
- Track tags

## Authentication

Last.fm uses MD5 signature-based authentication:

1. User authorizes via web interface
2. Spotter receives a session token
3. Session tokens don't expire
4. All API calls are signed with your shared secret

## Scrobbling to Last.fm

To send your Navidrome plays to Last.fm, consider using [multi-scrobbler](https://github.com/FoxxMD/multi-scrobbler):

```yaml
# Example multi-scrobbler configuration
sources:
  - name: navidrome
    type: subsonic
    url: http://navidrome:4533
    user: your_username
    password: your_password

targets:
  - name: lastfm
    type: lastfm
    apiKey: your_lastfm_api_key
    secret: your_lastfm_secret
```

This ensures all your plays are recorded in Last.fm and then synced to Spotter.

## Rate Limiting

Last.fm has generous rate limits:

- No strict per-second limits for most endpoints
- Recommended: Space requests by at least 200ms
- Bulk imports may need throttling

## Troubleshooting

### "Invalid API key"

1. Verify the API key is copied correctly (no extra spaces)
2. Check that the key is active on Last.fm
3. Try generating a new API key

### "Invalid session"

Sessions can become invalid if:
- You revoked access in Last.fm settings
- There was an authentication error

To fix:
1. Disconnect Last.fm from Preferences
2. Reconnect your account

### Missing Scrobbles

1. Verify scrobbles appear in your Last.fm profile
2. Check the sync interval setting
3. Trigger a manual sync from Tasks

### Historical Data Import

On first connection, Spotter imports your full scrobble history. This may take time for large histories:

- 10,000 scrobbles: ~2-3 minutes
- 100,000 scrobbles: ~20-30 minutes
- Progress is shown in the UI

## Production Deployment

For production:

1. Update `SPOTTER_LASTFM_REDIRECT_URL` to your production domain
2. Ensure HTTPS is configured
3. Update the callback URL in your Last.fm app settings
