# Spotify Enricher

## Overview

The Spotify enricher provides rich metadata and audio features from Spotify's Web API. Unlike the Spotify provider (which fetches listening history), this enricher focuses on enhancing artist, album, and track metadata with Spotify-specific information like audio features (BPM, key, energy), popularity scores, and high-quality images.

## Features

- **Implements**: `ArtistEnricher`, `AlbumEnricher`, `TrackEnricher`, `IDMatcher`
- **Data provided**:

  - **Artists**: Spotify ID, genres, popularity score, follower count, images
  - **Albums**: Spotify ID, album type, release date, label, popularity, images
  - **Tracks**: Spotify ID, ISRC, popularity, Spotify URL
  - **Audio Features**: BPM/tempo, musical key, mode (major/minor), energy, danceability, valence, acousticness, instrumentalness, speechiness, liveness
  - **Images**: High-resolution artist and album artwork
  - **ID Matching**: Search by name to find Spotify IDs with confidence scores

## Configuration

### Required Settings

```yaml
spotify:
  client_id: "your-client-id"
  client_secret: "your-client-secret"

# User must also have Spotify connected as a provider
# The enricher uses the user's OAuth2 tokens

```

**Environment Variables** (alternative):

```bash
SPOTIFY_CLIENT_ID=your-client-id
SPOTIFY_CLIENT_SECRET=your-client-secret

```

### Configuration Notes

- **Client ID**: Public identifier for your Spotify application
- **Client Secret**: Secret key for OAuth2 authentication
- **User Requirement**: User must have connected their Spotify account
- **Token Reuse**: Uses same tokens as Spotify provider
- **Automatic Refresh**: Tokens refreshed automatically when expired

## How to Get API Keys

1. **Go to Spotify Developer Dashboard**

   - Visit: <https://developer.spotify.com/dashboard>

2. **Log in with Spotify Account**

   - Use your Spotify account (free or premium)

3. **Create an App**

   - Click "Create app"
   - Fill in app details:
     - **App name**: `Spotter` (or your custom name)
     - **App description**: Brief description
     - **Redirect URI**: `<http://localhost:8080/auth/spotify/callback`>
   - Accept terms and click "Create"

4. **Get Your Credentials**

   - On the app page, click "Settings"
   - Copy the **Client ID**
   - Click "View client secret" and copy the **Client Secret**

5. **Configure in Spotter**

   - Add credentials to your configuration file
   - User must also connect Spotify in preferences

**Note**: Same credentials used for both provider and enricher.

## Rate Limits

### Official Limits

- **Standard**: ~180 requests per minute
- **Extended**: Some operations allow burst requests
- **Rate Limit Response**: HTTP 429 with `Retry-After` header
- **Per-User**: Rate limits apply per access token

### Spotter Implementation

- No built-in rate limiting (relies on Spotify's enforcement)
- Enrichment runs in batches to minimize API calls
- Audio features fetched in bulk (up to 100 tracks per request)
- Token refresh handled automatically

### Usage Best Practices

- Run enrichment during off-peak hours
- Batch operations when possible
- Cache Spotify IDs to avoid repeated searches
- Monitor for 429 responses

## Data Quality

### Strengths

- **Comprehensive**: Excellent coverage for most music
- **Audio Features**: Unique technical analysis (BPM, key, etc.)
- **Accurate**: High-quality metadata from Spotify's catalog
- **Images**: High-resolution artwork
- **Popularity**: Real-time popularity metrics
- **Current**: Frequently updated catalog

### Limitations

- **Requires User Auth**: User must have connected Spotify
- **Token Dependency**: Enricher fails if tokens invalid
- **Coverage Gaps**: Some indie/regional artists missing
- **No Lyrics**: Lyrics not available via API
- **Genre Limitations**: Artist-level only, not per-album/track
- **Podcast/Audiobook**: Not supported

### Audio Features Explained

**Technical**:

- `tempo`: BPM (beats per minute), e.g., 120.0
- `key`: Pitch class (0-11), e.g., 0=C, 1=C#, 2=D, etc.
- `mode`: 0=minor, 1=major
- `time_signature`: Beats per bar, e.g., 4

**Perceptual** (0.0 to 1.0):

- `energy`: Intensity and activity level
- `danceability`: How suitable for dancing
- `valence`: Musical positiveness/happiness
- `acousticness`: Confidence track is acoustic
- `instrumentalness`: Predicts no vocals
- `speechiness`: Presence of spoken words
- `liveness`: Presence of audience

### Musical Key Conversion

Spotify returns numeric keys (0-11), Spotter converts to strings:

- `0` → `C` (or `Cm` if mode=0)
- `1` → `C#`
- `2` → `D`
- `7` → `G`
- etc.

Example: Key=2, Mode=0 → `Dm` (D minor)

## Implementation Notes

### Token Management

The enricher reuses the user's Spotify provider tokens:

```go
// Token refresh before API calls
if time.Now().Add(5*time.Minute).After(token.Expiry) {
    // Automatically refresh using refresh token
}

```

**Important**: If user hasn't connected Spotify provider, enricher returns `nil`.

### Matching vs. Enrichment

**Matching** (Search):

- Used to find Spotify ID by name
- Returns confidence score (0.9 for exact, 0.7 for partial)
- Fuzzy matching supported
- Returns first result (highest relevance)

**Enrichment** (Direct Lookup):

- Uses known Spotify ID
- Returns complete metadata
- Includes audio features for tracks
- Fetches multiple images

### Search Confidence

```go
// Exact name match
if strings.EqualFold(result.Name, searchName) {
    confidence = 0.9
} else {
    confidence = 0.7 // Partial match
}

```

### Audio Features Retrieval

- Separate API call from track metadata
- Can batch up to 100 tracks per request
- Not all tracks have audio features
- Returns `nil` if unavailable

### Image Handling

Spotify provides multiple image sizes:

- Typically: 640x640, 300x300, 64x64
- Spotter downloads and resizes to max 1024px
- Saves as PNG in local storage
- First image marked as primary

### Error Handling

- **401 Unauthorized**: Token expired → triggers refresh
- **403 Forbidden**: Insufficient scope → user needs to reconnect
- **404 Not Found**: Track/artist/album not in Spotify
- **429 Rate Limited**: Too many requests → back off
- **5xx errors**: Spotify service issues → retry later

### Metadata Enrichment Strategy

1. Check if Spotify ID already exists
2. If not, search by name (match)
3. Fetch full metadata by Spotify ID
4. For tracks: fetch audio features
5. Download and save images
6. Return enriched data

## Testing

### Running Tests

```bash

# Run Spotify enricher tests

go test ./internal/enrichers/spotify/...

# Run with verbose output

go test -v ./internal/enrichers/spotify/...

# Run with coverage

go test -cover ./internal/enrichers/spotify/...

# Run specific test

go test -run TestEnrichTrack ./internal/enrichers/spotify/...

```

### Test Coverage

Tests should cover:

- Factory creation with/without credentials
- Token refresh logic
- Artist/album/track matching
- Enrichment with Spotify IDs
- Audio features retrieval
- Image downloading
- Error handling (401, 404, 429, 500)
- Malformed responses
- Missing audio features
- Key/mode conversion

### Mock API Server

Tests use `httptest.NewServer`:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify Authorization header
    assert.Contains(t, r.Header.Get("Authorization"), "Bearer")

    // Return mock Spotify response
    json.NewEncoder(w).Encode(mockTrack)
}))

```

## Troubleshooting

### "No Spotify Auth Configured"

- User hasn't connected Spotify in Spotter
- Go to preferences → connect Spotify
- Complete OAuth2 flow

### "Spotify API Unauthorized"

- Token may be expired (should auto-refresh)
- Token may be invalid/revoked
- User needs to disconnect and reconnect

### No Audio Features

- Not all tracks have audio features on Spotify
- This is normal and expected
- Feature returns `nil` (not an error)

### Incorrect Matches

- Search may match wrong track/artist
- Check confidence scores
- Consider manual verification for important matches

### Token Refresh Failing

- Refresh token may be revoked
- OAuth2 configuration may be incorrect
- User needs to re-authenticate

### Rate Limit Errors (429)

- Too many requests in short time
- Wait for `Retry-After` duration
- Reduce enrichment frequency
- Batch operations

### Missing Artist Genres

- Some artists don't have genres assigned
- Spotify's genre classification is limited
- This is expected behavior

## API Reference

- **Spotify Web API**: <https://developer.spotify.com/documentation/web-api>
- **Search API**: <https://developer.spotify.com/documentation/web-api/reference/search>
- **Get Artist**: <https://developer.spotify.com/documentation/web-api/reference/get-an-artist>
- **Get Album**: <https://developer.spotify.com/documentation/web-api/reference/get-an-album>
- **Get Track**: <https://developer.spotify.com/documentation/web-api/reference/get-track>
- **Audio Features**: <https://developer.spotify.com/documentation/web-api/reference/get-audio-features>
- **Authorization**: <https://developer.spotify.com/documentation/web-api/concepts/authorization>

## Example Usage

```go
// Create factory
factory := spotify.New(logger, config)

// Create enricher for user (requires Spotify auth)
enricher, err := factory(ctx, user)
if err != nil {
    // Handle error
}
if enricher == nil {
    // User hasn't connected Spotify or no credentials
    return
}

// Match track by name
matcher := enricher.(enrichers.IDMatcher)
spotifyID, confidence, err := matcher.MatchTrack(ctx, "Paranoid Android", "Radiohead", "OK Computer")
if err != nil {
    // Handle error
}
fmt.Printf("Spotify ID: %s, Confidence: %.2f\n", spotifyID, confidence)

// Enrich track with full metadata
track := &ent.Track{
    Name: "Paranoid Android",
    SpotifyID: &spotifyID,
    Edges: ent.TrackEdges{
        Artist: &ent.Artist{Name: "Radiohead"},
        Album: &ent.Album{Name: "OK Computer"},
    },
}

trackEnricher := enricher.(enrichers.TrackEnricher)
data, err := trackEnricher.EnrichTrack(ctx, track)
if err != nil {
    // Handle error
}

// Audio features
fmt.Printf("BPM: %.0f\n", data.BPM)
fmt.Printf("Key: %s\n", data.Key)
fmt.Printf("Energy: %.2f\n", data.Energy)
fmt.Printf("Danceability: %.2f\n", data.Danceability)
fmt.Printf("Valence: %.2f\n", data.Valence)

// Get artist images
artist := &ent.Artist{
    Name: "Radiohead",
    SpotifyID: "artist-spotify-id",
}

artistEnricher := enricher.(enrichers.ArtistEnricher)
images, err := artistEnricher.GetArtistImages(ctx, artist)
if err != nil {
    // Handle error
}

for _, img := range images {
    fmt.Printf("Image: %dx%d - %s\n", img.Width, img.Height, img.URL)
}

```

## Best Practices

1. **User Must Connect**: Ensure user has connected Spotify provider
2. **Cache Spotify IDs**: Store IDs to avoid repeated searches
3. **Handle Token Refresh**: Built-in but monitor for failures
4. **Batch Audio Features**: Fetch multiple tracks at once
5. **Check Confidence Scores**: Verify matches for important data
6. **Handle Missing Features**: Not all tracks have audio analysis
7. **Respect Rate Limits**: Don't hammer the API
8. **Use for Popular Music**: Best coverage for mainstream artists
9. **Combine with MusicBrainz**: Use both for comprehensive data

## Enrichment Order

Spotify enricher should run **after**:

1. MusicBrainz (provides MBIDs)
2. Lidarr
3. Navidrome

Spotify enricher should run **before**:

1. Last.fm (less comprehensive)
2. Fanart.tv (images only)
3. OpenAI (AI enrichment uses all metadata)

This is the default order in `enrichers.DefaultOrder()`.

## Why Use Spotify Enricher?

- **Audio Features**: Unique technical analysis unavailable elsewhere
- **Popularity Metrics**: Real-time popularity scores
- **High-Quality Images**: Professional artwork
- **Accurate Metadata**: Well-maintained catalog
- **ISRC Codes**: For cross-service matching
- **Musical Analysis**: Key, tempo, mode for advanced features
- **Comprehensive**: Good coverage for most music

## Comparison with Other Enrichers

| Feature | Spotify | MusicBrainz | Last.fm | Fanart.tv |
|---------|---------|-------------|---------|-----------|
| Requires Auth | ✓ | ✗ | ✗ | ✗ |
| Audio Features | ✓ | ✗ | ✗ | ✗ |
| Popularity | ✓ | ✗ | ✗ | ✗ |
| Tags/Genres | ✓ | ✓ | ✓ | ✗ |
| Images | ✓ | ✓ | ✓ | ✓ |
| Free API | ✓ | ✓ | ✓ | ✓ |
| Rate Limit | 180/min | 1/sec | None | 1000/day |
| Coverage | High | Highest | Medium | Medium |

## Related Files

- `spotify.go`: Main enricher implementation
- `spotify_test.go`: Unit tests (to be created)
- `../../providers/spotify/`: Spotify provider (different purpose)
- `../enrichers.go`: Base interfaces and types
