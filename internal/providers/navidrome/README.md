# Navidrome Provider

## Overview

The Navidrome provider integrates with Navidrome music servers to sync playlists, retrieve listening history, and manage your music library. Unlike other providers, Navidrome serves as Spotter's **primary music server** - the central hub where your music collection lives. It uses the Subsonic API for compatibility with the broader ecosystem of music server software.

## Features

- **Implements**: `HistoryFetcher`, `PlaylistManager`, `Authenticator`
- **Key capabilities**:

  - Fetches recently played tracks from Navidrome's internal API
  - Retrieves "now playing" status via Subsonic API
  - Syncs playlists FROM Spotter TO Navidrome
  - Creates and manages playlists on Navidrome
  - Deletes playlists from Navidrome
  - Updates playlist tracks and metadata
  - Username/password authentication (no OAuth complexity)
  - Compatible with any Subsonic-compatible server

## Configuration

### Required Settings

```yaml
navidrome:
  base_url: "<http://localhost:4533">

  # No API key required - uses username/password per user

```

**Environment Variables** (alternative):

```bash
NAVIDROME_BASE_URL=<http://localhost:4533>

```

### Configuration Notes

- **Base URL**: The URL where your Navidrome server is accessible
- **No API Key**: Authentication uses individual user credentials
- **Per-User Auth**: Each user connects with their own Navidrome username/password
- **Local or Remote**: Can be localhost (self-hosted) or remote server

### Per-User Authentication

Each user must connect their Navidrome account in Spotter preferences:

1. Username: Their Navidrome username
2. Password: Their Navidrome password

This is stored securely in the database (not the config file).

## How to Get API Keys

**No API key required!** Navidrome uses username/password authentication.

### Setup Steps

1. **Install Navidrome**

   - Download from: <https://www.navidrome.org/docs/installation/>
   - Or use Docker: `docker run -d --name navidrome -p 4533:4533 -v /path/to/music:/music deluan/navidrome`

2. **Create User Account**

   - Open Navidrome in your browser: `<http://localhost:4533`>
   - Create an admin account (first user)
   - Or create additional user accounts in Settings → Users

3. **Configure in Spotter**

   - Set `navidrome.base_url` in Spotter config
   - Users connect via Spotter preferences using their Navidrome credentials

4. **Verify Connection**

   - Spotter will test authentication on first connection
   - Check logs for any connection errors

## Subsonic API Authentication

Navidrome implements the Subsonic API, which uses a unique authentication mechanism:

### Authentication Flow

1. **Generate Random Salt**

   - 16-character random hexadecimal string
   - Generated fresh for each request

2. **Create Token**

   - Concatenate: `password + salt`
   - Calculate MD5 hash of the result
   - Convert to hexadecimal string

3. **Include in Request**

   - `u`: Username
   - `s`: Salt (generated in step 1)
   - `t`: Token (calculated in step 2)
   - `c`: Client name ("spotter")
   - `v`: API version ("1.16.1")
   - `f`: Format ("json")

### Example Authentication

```text
Password: mypassword
Salt: abc123def456
String to hash: mypasswordabc123def456
MD5 hash: e10adc3949ba59abbe56e057f20f883e
Token: e10adc3949ba59abbe56e057f20f883e

Request URL:
<http://localhost:4533/rest/getArtist?>
  id=123&
  u=myusername&
  s=abc123def456&
  t=e10adc3949ba59abbe56e057f20f883e&
  c=spotter&
  v=1.16.1&
  f=json

```

### No JWT Tokens for Subsonic

While Navidrome's internal API uses JWT tokens, the Subsonic API uses salt+token authentication. Spotter uses both:

- **Subsonic API**: For standard operations (playlists, now playing)
- **Internal API**: For recently played history (richer data)

## API Limitations

### Rate Limits

- **No official rate limit** for self-hosted instances
- Be respectful with concurrent requests
- Spotter batches operations to minimize load

### Historical Data

- **Full history available** via internal API
- No time-based limitations (unlike Spotify's 50-track limit)
- Depends on Navidrome's database retention

### Playlist Limitations

- **Maximum tracks**: No hard limit (depends on database)
- **Playlist creation**: Unlimited
- **Modification**: Can only modify your own playlists
- **Public/Private**: Navidrome supports both

### Known Quirks

1. **Internal API vs Subsonic API**

   - Internal API: `/api/play/history` - Recently played with full metadata
   - Subsonic API: `/rest/getNowPlaying` - Current plays only
   - Spotter prefers internal API for richer data

2. **Subsonic API Versioning**

   - Spotter uses Subsonic API version 1.16.1
   - Newer versions available but 1.16.1 widely supported
   - Compatible with OpenSubsonic implementations

3. **Password Storage**

   - Passwords stored encrypted in Spotter database
   - Never transmitted in plain text (only MD5 token sent)
   - Change Navidrome password = must reconnect in Spotter

4. **Track Matching**

   - Uses Navidrome's internal track IDs
   - Matches by file path, artist, album, track name
   - MusicBrainz IDs used when available for accuracy

## Implementation Notes

### Playlist Sync Direction

**Important**: Navidrome provider syncs playlists **FROM Spotter TO Navidrome**, not the reverse.

Flow:

1. User creates/edits playlist in Spotter
2. Spotter syncs to Navidrome (and other connected services)
3. Playlist appears in Navidrome for playback

This makes Navidrome the **playback destination** for playlists managed in Spotter.

### Track Matching Strategy

When syncing playlists, Spotter matches tracks by:

1. **Navidrome ID** (if already known)
2. **MusicBrainz ID** (if available in both systems)
3. **File path** (if track was scanned from same location)
4. **Metadata match**: Artist + Album + Track Name

### Recently Played History

Two methods:

1. **Internal API** (preferred):

   - Endpoint: `/api/play/history`
   - Requires JWT authentication
   - Returns full play history with timestamps
   - Richer metadata

2. **Subsonic API** (fallback):

   - Endpoint: `/rest/getNowPlaying`
   - Returns only currently playing tracks
   - Limited to active sessions

### Playlist Operations

**Create Playlist**:

- POST to `/rest/createPlaylist`
- Optionally include track IDs on creation

**Update Playlist**:

- POST to `/rest/updatePlaylist` (metadata)
- POST to `/rest/createPlaylist` with same ID (add tracks)

**Delete Playlist**:

- GET to `/rest/deletePlaylist?id=xxx`

**Get Playlists**:

- GET to `/rest/getPlaylists`
- Returns all user's playlists

### Error Handling

Subsonic error codes:

- **10**: Required parameter missing
- **20**: Incompatible Subsonic version
- **40**: Wrong username or password
- **41**: Token authentication not supported
- **50**: User not authorized
- **70**: Requested data not found

## Testing

### Running Tests

```bash

# Run Navidrome provider tests

go test ./internal/providers/navidrome/...

# Run with verbose output

go test -v ./internal/providers/navidrome/...

# Run with coverage

go test -cover ./internal/providers/navidrome/...

```

### Test Coverage

Tests cover:

- Factory creation with/without credentials
- Subsonic authentication (salt + MD5 token)
- Recently played retrieval (both APIs)
- Now playing status
- Playlist operations (create, update, delete)
- Playlist track management
- Error handling for all Subsonic error codes
- JWT token authentication for internal API

### Mock Subsonic Server

Tests use `httptest.NewServer` to simulate Navidrome:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify authentication parameters
    assert.NotEmpty(t, r.URL.Query().Get("u"))
    assert.NotEmpty(t, r.URL.Query().Get("s"))
    assert.NotEmpty(t, r.URL.Query().Get("t"))

    // Return mock Subsonic response
    response := map[string]interface{}{
        "subsonic-response": map[string]interface{}{
            "status": "ok",
            // ... response data
        },
    }
    json.NewEncoder(w).Encode(response)
}))

```

## Troubleshooting

### "Wrong username or password" (Error 40)

- Verify username and password are correct
- Check for extra whitespace in credentials
- Ensure Navidrome account is active (not disabled)
- Try logging into Navidrome web UI with same credentials

### "Requested data not found" (Error 70)

- Track or playlist doesn't exist in Navidrome
- Navidrome may not have scanned the file yet
- Check Navidrome's library scan status
- Verify file paths are accessible to Navidrome

### Connection Refused

- Verify Navidrome server is running
- Check `base_url` is correct (including http/https)
- Check firewall isn't blocking the port
- Test URL in browser: `<http://localhost:4533`>

### Playlist Sync Fails

- Check that all tracks exist in Navidrome library
- Verify Navidrome has scanned the music directory
- Check Spotter logs for specific error messages
- Ensure user has permission to create playlists

### Authentication Fails After Password Change

- User must disconnect and reconnect in Spotter
- Old password hash is invalid
- Go to Spotter preferences → Disconnect Navidrome → Reconnect

### Recently Played Not Showing

- Check if Navidrome is logging plays (Settings → Scrobbling)
- Verify internal API is accessible: `/api/play/history`
- Check JWT token hasn't expired (Spotter refreshes automatically)
- Look for errors in Spotter logs

### Tracks Not Found During Sync

- Navidrome may not have scanned the files yet
- Trigger a library scan in Navidrome
- Check that music files are in configured music folder
- Verify file permissions allow Navidrome to read files

## API Reference

- **Navidrome Docs**: <https://www.navidrome.org/docs/>
- **Subsonic API**: <http://www.subsonic.org/pages/api.jsp>
- **OpenSubsonic**: <https://opensubsonic.netlify.app/>
- **Internal API**: Check Navidrome's OpenAPI docs at `/rest/swagger.json`

## Example Usage

```go
// Create factory
factory := navidrome.New(logger, config)

// Create provider for user (requires Navidrome auth)
provider, err := factory(ctx, user)
if err != nil {
    // Handle error
}
if provider == nil {
    // User hasn't connected Navidrome
    return
}

// Fetch recent listens
historyFetcher := provider.(providers.HistoryFetcher)
since := time.Now().Add(-24 * time.Hour) // Last 24 hours

err = historyFetcher.GetRecentListens(ctx, since, func(tracks []providers.Track) error {
    // Process batch of tracks
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

// Create a new playlist
newPlaylist := providers.Playlist{
    Name:        "My Favorites",
    Description: "Created by Spotter",
    Public:      true,
}

tracks := []providers.PlaylistTrack{
    {ProviderID: "track-id-1"},
    {ProviderID: "track-id-2"},
}

err = playlistManager.CreatePlaylist(ctx, newPlaylist, tracks)
if err != nil {
    // Handle error
}

// Sync playlist to Navidrome
existingPlaylist := providers.Playlist{
    ID:          "playlist-123",
    Name:        "Updated Playlist",
    Description: "Now with more songs",
}

err = playlistManager.SyncPlaylist(ctx, existingPlaylist, tracks)
if err != nil {
    // Handle error
}

```

## Best Practices

1. **Set Correct Base URL**: Include http:// or <https://,> no trailing slash
2. **Use Strong Passwords**: Navidrome accounts should have secure passwords
3. **Regular Library Scans**: Keep Navidrome library updated for accurate matching
4. **Monitor Sync Errors**: Check logs when playlist syncs fail
5. **Test Connection**: Verify Navidrome is accessible before syncing
6. **Backup Playlists**: Navidrome stores playlists in database - back it up
7. **Use MusicBrainz IDs**: Tag your music with MBIDs for better matching
8. **Handle Missing Tracks**: Not all tracks may exist in Navidrome - handle gracefully
9. **Respect Server Load**: Don't sync too frequently, especially for large libraries
10. **Keep Navidrome Updated**: Newer versions have bug fixes and improvements

## Why Use Navidrome?

- **Self-Hosted**: Complete control over your music and data
- **Subsonic Compatible**: Works with many mobile apps (DSub, Ultrasonic, play:Sub)
- **Modern UI**: Beautiful web interface for music browsing
- **Fast**: Written in Go, very performant
- **Smart Playlists**: Supports dynamic playlists based on criteria
- **Multi-User**: Family members can have separate accounts
- **Transcoding**: On-the-fly audio conversion for mobile streaming
- **Last.fm Integration**: Built-in scrobbling support
- **Open Source**: Free and actively maintained

## Navidrome as Primary Server

Unlike Spotify or Last.fm (external services), Navidrome is where your music collection lives:

- **Source of Truth**: Your local files are the canonical music library
- **Playlist Destination**: Playlists synced here for playback
- **Metadata Hub**: Can provide metadata from scanned files
- **No Rate Limits**: Self-hosted = no API throttling
- **Privacy**: Your listening data stays on your server

This makes it the **foundation** of your Spotter setup, with other services enriching metadata.

## Related Files

- `navidrome.go`: Main provider implementation
- `navidrome_test.go`: Comprehensive unit tests
- `../../ent/navidromeauth.go`: Database schema for auth data
- `../../enrichers/navidrome/`: Navidrome enricher (metadata enrichment)
