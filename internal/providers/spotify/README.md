# Spotify Provider

## Overview

The Spotify provider integrates with Spotify's Web API to sync listening history and manage playlists. It uses OAuth2 for authentication and supports automatic token refresh.

## Features

- **Implements**: `HistoryFetcher`, `PlaylistManager`, `Authenticator`
- **Key capabilities**:

  - Fetches recently played tracks (limited to 50 most recent)
  - Retrieves user's playlists
  - Creates and manages playlists
  - OAuth2 authentication with automatic token refresh
  - Access to detailed track metadata (audio features, ISRCs)

## Configuration

### Required Settings

```yaml
spotify:
  client_id: "your-client-id"
  client_secret: "your-client-secret"
  redirect_url: "<http://localhost:8080/auth/spotify/callback">

```

**Environment Variables** (alternative):

```bash
SPOTIFY_CLIENT_ID=your-client-id
SPOTIFY_CLIENT_SECRET=your-client-secret
SPOTIFY_REDIRECT_URL=<http://localhost:8080/auth/spotify/callback>

```

### Configuration Notes

- **Client ID**: Public identifier for your application
- **Client Secret**: Secret key used for OAuth2 token exchange
- **Redirect URL**: OAuth2 callback URL (must match Spotify dashboard)

### Required Scopes

The provider requests these OAuth2 scopes:

- `user-read-recently-played`: Access to listening history
- `playlist-read-private`: Read private playlists
- `playlist-read-collaborative`: Read collaborative playlists
- `playlist-modify-public`: Create/modify public playlists
- `playlist-modify-private`: Create/modify private playlists

## How to Get API Keys

1. **Go to Spotify Developer Dashboard**

   - Visit: <https://developer.spotify.com/dashboard>

2. **Log in with Spotify Account**

   - Use your Spotify account (free or premium)

3. **Create an App**

   - Click "Create app"
   - Fill in app details:
     - **App name**: `Spotter` (or your custom name)
     - **App description**: Brief description of your use case
     - **Redirect URI**: `<http://localhost:8080/auth/spotify/callback`>
   - Check the Developer Terms of Service box
   - Click "Create"

4. **Get Your Credentials**

   - On the app page, click "Settings"
   - Copy the **Client ID**
   - Click "View client secret" and copy the **Client Secret**

5. **Configure Redirect URIs**

   - In Settings, under "Redirect URIs"
   - Add your callback URL:
     - Development: `<http://localhost:8080/auth/spotify/callback`>
     - Production: `<https://yourdomain.com/auth/spotify/callback`>
   - Click "Add" then "Save"

6. **Add to Spotter Config**

   - Paste Client ID and Client Secret into your configuration file

## Authentication Flow

Spotify uses standard OAuth2 authorization code flow:

1. **User Initiates Connection**

   - User clicks "Connect Spotify" in Spotter preferences

2. **Redirect to Spotify**

   - Spotter redirects to Spotify's authorization URL
   - Includes: `client_id`, `redirect_uri`, `scope`, `state` (CSRF protection)

3. **User Authorizes**

   - User logs into Spotify (if not already logged in)
   - User reviews requested permissions
   - User clicks "Agree" to grant access

4. **Callback with Authorization Code**

   - Spotify redirects back with `code` parameter
   - Example: `<http://localhost:8080/auth/spotify/callback?code=AQD...&state=xyz`>

5. **Exchange Code for Tokens**

   - Spotter exchanges the code for access token and refresh token
   - POST request to `<https://accounts.spotify.com/api/token`>
   - Includes: `code`, `client_id`, `client_secret`, `redirect_uri`

6. **Store Tokens**

   - Access token (expires in 1 hour)
   - Refresh token (permanent, until revoked)
   - Expiry timestamp
   - User's Spotify username and display name

7. **Automatic Token Refresh**

   - When access token expires, Spotter automatically refreshes it
   - Uses refresh token to get new access token
   - Transparent to the user

## API Limitations

### Rate Limits

- **Standard**: No fixed rate limit, but excessive use may be throttled
- **Typical limit**: ~180 requests per minute
- **Response**: HTTP 429 (Too Many Requests) with `Retry-After` header
- Spotter handles rate limit errors gracefully

### Historical Data Limitations

- **Recently Played**: Only 50 most recent tracks (API limitation)
- **No full history**: Cannot access complete listening history
- **Time window**: Approximately last 24-48 hours of listening
- **Workaround**: Sync frequently to capture all plays

### Playlist Limitations

- **Maximum tracks**: 10,000 tracks per playlist
- **Creation**: Can create unlimited playlists
- **Modification**: Can only modify playlists owned by the user
- **Collaborative**: Can read but not modify collaborative playlists you don't own

### Data Availability

- **Audio features**: Available for most tracks (BPM, key, energy, etc.)
- **ISRCs**: Available for most tracks (international standard recording code)
- **Lyrics**: Not available via API
- **Private session**: Tracks played in private session are not returned

## Implementation Notes

### Token Management

#### Access Token

- Expires after 1 hour
- Automatically refreshed when needed
- Refresh triggered 5 minutes before expiry

#### Refresh Token

- Permanent (unless user revokes access)
- Stored securely in database
- Used to obtain new access tokens

#### Token Refresh Logic

```go
// Before each API call
if time.Now().Add(5*time.Minute).After(token.Expiry) {
    // Refresh token
}

```

### Listening History Strategy

Due to the 50-track limitation:

1. Sync frequently (every 15-30 minutes recommended)
2. Track last sync timestamp
3. Use `after` parameter to get only new plays
4. Deduplicate based on `played_at` timestamp

### Playlist Syncing

- Fetches all user's playlists (including private)
- Supports creating new playlists
- Can update existing playlists
- Handles playlist cover images

### Track Matching

- Uses ISRC codes for accurate matching across services
- Falls back to name/artist/album matching
- Spotify IDs used as provider-specific identifiers

### Error Handling

- **401 Unauthorized**: Token expired or invalid → triggers refresh
- **403 Forbidden**: Insufficient permissions → requires re-auth
- **429 Too Many Requests**: Rate limited → back off and retry
- **5xx errors**: Spotify service issues → retry with exponential backoff

## Testing

### Running Tests

```bash

# Run Spotify provider tests

go test ./internal/providers/spotify/...

# Run with verbose output

go test -v ./internal/providers/spotify/...

# Run with coverage

go test -cover ./internal/providers/spotify/...

```

### Test Coverage

- Factory creation with/without credentials
- OAuth2 flow (mock)
- Token refresh logic
- Recent tracks retrieval
- Playlist operations
- Error handling for all status codes

### Mocking Spotify API

Tests use `httptest.NewServer` to mock Spotify API responses. Example:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify Authorization header
    assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

    // Return mock response
    json.NewEncoder(w).Encode(mockResponse)
}))

```

## Troubleshooting

### "Invalid Client" Error

- Verify Client ID and Client Secret are correct
- Check for extra whitespace in configuration
- Ensure credentials match the Spotify dashboard

### "Redirect URI Mismatch"

- The redirect URI in your config must exactly match Spotify dashboard
- Including protocol (http/https), port, and path
- Check for trailing slashes

### "Invalid Token" Errors

- Token may have been revoked by user
- User needs to disconnect and reconnect in Spotter
- Check token expiry handling

### Only 50 Tracks Syncing

- This is a Spotify API limitation
- Solution: Sync more frequently (every 15-30 minutes)
- Consider using Last.fm as primary history source

### Token Refresh Failing

- Refresh token may be invalid or revoked
- Check for network issues
- Verify OAuth2 configuration is correct
- User may need to re-authenticate

### Missing Playlists

- Check that all required scopes are granted
- Private playlists require `playlist-read-private` scope
- Collaborative playlists require `playlist-read-collaborative` scope

### Rate Limit Errors

- Reduce sync frequency
- Implement backoff strategy
- Check for excessive concurrent requests

## API Reference

- **Official Docs**: <https://developer.spotify.com/documentation/web-api>
- **Authorization Guide**: <https://developer.spotify.com/documentation/web-api/concepts/authorization>
- **Recently Played**: <https://developer.spotify.com/documentation/web-api/reference/get-recently-played>
- **Playlists**: <https://developer.spotify.com/documentation/web-api/reference/get-a-list-of-current-users-playlists>
- **OAuth2 Scopes**: <https://developer.spotify.com/documentation/web-api/concepts/scopes>

## Example Usage

```go
// Create factory
factory := spotify.New(logger, config)

// Create provider for user
provider, err := factory(ctx, user)
if err != nil {
    // Handle error
}
if provider == nil {
    // User hasn't connected Spotify or credentials not configured
}

// Fetch recent listens
historyFetcher := provider.(providers.HistoryFetcher)
since := time.Now().Add(-1 * time.Hour)

err = historyFetcher.GetRecentListens(ctx, since, func(tracks []providers.Track) error {
    // Process batch of tracks (max 50)
    for _, track := range tracks {
        fmt.Printf("Played: %s by %s at %s\n",
            track.Name, track.Artist, track.PlayedAt)
    }
    return nil
})

// Get playlists
playlistManager := provider.(providers.PlaylistManager)
playlists, err := playlistManager.GetPlaylists(ctx)
if err != nil {
    // Handle error
}

for _, playlist := range playlists {
    fmt.Printf("Playlist: %s (%d tracks)\n",
        playlist.Name, playlist.TrackCount)
}

```

## Best Practices

1. **Sync Frequently**: Due to 50-track limitation, sync every 15-30 minutes
2. **Handle Token Refresh**: Always check token expiry before API calls
3. **Respect Rate Limits**: Implement exponential backoff for 429 errors
4. **Use ISRCs**: For cross-service track matching
5. **Store Provider IDs**: Keep Spotify track/playlist IDs for future operations
6. **Deduplicate**: Use timestamp + track ID to avoid duplicate listens

## Related Files

- `spotify.go`: Main provider implementation
- `spotify_test.go`: Unit tests
- `../../ent/spotifyauth.go`: Database schema for auth data
