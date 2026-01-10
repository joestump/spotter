# Last.fm Provider

## Overview

The Last.fm provider syncs listening history from Last.fm to Spotter. It retrieves scrobbles (listening history) from the Last.fm API and normalizes them into Spotter's track format.

## Features

- **Implements**: `HistoryFetcher`, `Authenticator`
- **Key capabilities**:

  - Fetches listening history with pagination
  - Automatically skips "now playing" tracks
  - Supports filtering by date (fetch only new listens)
  - Session-based authentication (no expiring tokens)
  - Automatic retry logic for 500 errors

## Configuration

### Required Settings

```yaml
lastfm:
  api_key: "your-api-key"
  shared_secret: "your-shared-secret"
  redirect_url: "<http://localhost:8080/auth/lastfm/callback">

```

**Environment Variables** (alternative):

```bash
LASTFM_API_KEY=your-api-key
LASTFM_SHARED_SECRET=your-shared-secret
LASTFM_REDIRECT_URL=<http://localhost:8080/auth/lastfm/callback>

```

### Configuration Notes

- **API Key**: Public identifier for your application
- **Shared Secret**: Used to sign API requests (MD5 signatures)
- **Redirect URL**: Where Last.fm redirects after user authorizes

## How to Get API Keys

1. **Go to Last.fm API Account Creation**

   - Visit: <https://www.last.fm/api/account/create>

2. **Fill in Application Details**

   - Application name: `Spotter` (or your custom name)
   - Application description: Brief description of your use case
   - Callback URL: `<http://localhost:8080/auth/lastfm/callback`>

3. **Submit the Form**

   - After submission, you'll receive your **API Key** and **Shared Secret**

4. **Copy Credentials**

   - Copy the API Key and Shared Secret
   - Add them to your Spotter configuration file

5. **Set Redirect URL**

   - Ensure the redirect URL in your config matches what you registered
   - For local development: `<http://localhost:8080/auth/lastfm/callback`>
   - For production: `<https://yourdomain.com/auth/lastfm/callback`>

## Authentication Flow

Last.fm uses a custom authentication flow (not OAuth2):

1. **User Initiates Connection**

   - User clicks "Connect Last.fm" in Spotter preferences

2. **Redirect to Last.fm**

   - Spotter redirects to: `<http://www.last.fm/api/auth/?api_key=YOUR_KEY&cb=CALLBACK_URL`>

3. **User Authorizes**

   - User logs into Last.fm (if not already logged in)
   - User clicks "Yes, allow access"

4. **Callback with Token**

   - Last.fm redirects back with a `token` parameter
   - Example: `<http://localhost:8080/auth/lastfm/callback?token=abc123`>

5. **Exchange Token for Session Key**

   - Spotter calls `auth.getSession` with the token
   - Request is signed with MD5 signature (using shared secret)
   - Last.fm returns a permanent session key and username

6. **Store Session Key**

   - Session key is stored in the database
   - Session keys never expire (until user revokes access)

### MD5 Signature Algorithm

All authenticated Last.fm API calls require an MD5 signature:

1. Sort parameters alphabetically by key
2. Concatenate as: `key1value1key2value2...`
3. Append the shared secret
4. Calculate MD5 hash
5. Add `api_sig` parameter with the hash

Example:

```text
Parameters: {method: "auth.getSession", token: "abc", api_key: "xyz"}
Sorted: api_key=xyz, method=auth.getSession, token=abc
String: api_keyxyzmethod auth.getSessiontokenabc[shared_secret]
MD5: 1234567890abcdef1234567890abcdef

```

## API Limitations

### Rate Limits

- **No official rate limit** for most read operations
- Spotter implements retry logic for 500 errors (3 retries with exponential backoff)
- Be respectful: don't make excessive concurrent requests

### Historical Data

- **Full history available**: Last.fm stores all scrobbles since account creation
- No time-based limitations (unlike Spotify's 50-track limit)
- Pagination: 200 tracks per page (default)

### Data Quality

- Track metadata quality depends on what was scrobbled
- Missing album information is common (especially for radio scrobbles)
- Artist/track names may vary based on scrobble source
- No audio features (BPM, key, etc.) available

### Known Quirks

1. **Now Playing Tracks**

   - API returns current track with `nowplaying="true"` attribute
   - These tracks don't have timestamps yet
   - Spotter automatically skips them

2. **Timestamp Format**

   - Last.fm uses Unix timestamps (UTS) in seconds
   - API returns timestamps as strings, not integers

3. **No Refresh Tokens**

   - Session keys don't expire
   - No automatic token refresh needed
   - Users must manually disconnect/reconnect if session invalidated

4. **Error Handling**

   - Error code 6 = "Invalid parameters" (often means "not found")
   - Error code 9 = "Invalid session key"
   - Error code 11 = "Service offline"
   - Error code 16 = "Service temporarily unavailable"

## Implementation Notes

### Pagination Strategy

- Fetches 200 tracks per page by default
- Continues until no more tracks or reaching `totalPages`
- Uses `page` parameter to iterate through results

### Filtering by Date

- Uses `from` parameter (Unix timestamp) to fetch only recent listens
- Spotter tracks the last sync time and only fetches new scrobbles
- Reduces API calls and sync time

### Track ID Handling

- Last.fm doesn't provide stable track IDs
- Spotter uses the Last.fm track URL as the provider-specific ID
- Format: `<http://www.last.fm/music/Artist/_/Track`>

### Error Recovery

- Implements retry logic for 500 errors:

  - Attempt 1: immediate
  - Attempt 2: wait 1 second
  - Attempt 3: wait 2 seconds
  - Attempt 4: wait 4 seconds

- Does NOT retry 4xx errors (client errors)

### Session Management

- Session keys stored in `lastfm_auth` table
- No need for token refresh
- Spotter validates session on first API call each sync

## Testing

### Running Tests

```bash

# Run Last.fm provider tests

go test ./internal/providers/lastfm/...

# Run with verbose output

go test -v ./internal/providers/lastfm/...

# Run with coverage

go test -cover ./internal/providers/lastfm/...

```

### Test Structure

- `lastfm_test.go`: Comprehensive unit tests
- Uses `httptest.NewServer` to mock Last.fm API
- Tests cover:

  - Factory creation with/without auth
  - Authentication flow
  - Recent listens retrieval
  - Pagination
  - Error handling
  - MD5 signature generation

### Mock Responses

Tests use realistic JSON responses matching Last.fm's API structure. See test file for examples.

## Troubleshooting

### "Invalid API Key"

- Verify `api_key` in config matches Last.fm dashboard
- Check for extra whitespace or quotes in configuration

### "Invalid Session Key" (Error 9)

- Session may have been revoked by user on Last.fm website
- User needs to disconnect and reconnect in Spotter

### "No Recent Tracks"

- User may not have any scrobbles
- Check Last.fm profile to verify scrobbles exist
- Verify date filter isn't too restrictive

### Sync is Slow

- Last.fm API can be slow during peak hours
- Large scrobble histories take time to sync initially
- Subsequent syncs are faster (only fetch new scrobbles)

### Duplicate Tracks

- Can occur if sync is interrupted
- Spotter's deduplication logic should handle this
- Based on artist, track name, and timestamp

## API Reference

- **Official Docs**: <https://www.last.fm/api>
- **Auth Documentation**: <https://www.last.fm/api/authentication>
- **user.getRecentTracks**: <https://www.last.fm/api/show/user.getRecentTracks>
- **auth.getSession**: <https://www.last.fm/api/show/auth.getSession>

## Example Usage

```go
// Create factory
factory := lastfm.New(logger, config)

// Create provider for user
provider, err := factory(ctx, user)
if err != nil {
    // Handle error
}
if provider == nil {
    // User hasn't connected Last.fm
}

// Fetch recent listens
historyFetcher := provider.(providers.HistoryFetcher)
since := time.Now().Add(-7 * 24 * time.Hour) // Last 7 days

err = historyFetcher.GetRecentListens(ctx, since, func(tracks []providers.Track) error {
    // Process batch of tracks
    for _, track := range tracks {
        fmt.Printf("Played: %s by %s at %s\n",
            track.Name, track.Artist, track.PlayedAt)
    }
    return nil
})

```

## Related Files

- `lastfm.go`: Main provider implementation
- `lastfm_test.go`: Unit tests
- `../../ent/lastfmauth.go`: Database schema for auth data
