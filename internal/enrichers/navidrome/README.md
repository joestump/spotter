# Navidrome Enricher

## Overview

The Navidrome enricher provides metadata enrichment from your self-hosted Navidrome music server. Unlike the Navidrome provider (which syncs playlists and history), this enricher extracts metadata from your local music library to enrich Spotter's database. It uses the Subsonic API for compatibility and requires per-user authentication.

## Features

- **Implements**: `ArtistEnricher`, `AlbumEnricher`, `TrackEnricher`
- **Data provided**:

  - **Artists**: Navidrome ID, MusicBrainz ID, biography, images, album count
  - **Albums**: Navidrome ID, MusicBrainz ID, year, genre, cover art
  - **Tracks**: Navidrome ID, MusicBrainz ID, duration, BPM, track/disc numbers
  - **Cover Art**: Generates authenticated URLs for album/artist artwork
  - **Search**: Finds entities by name or MusicBrainz ID
  - **Local Data**: Enriches from your own music collection

## Configuration

### Required Settings

```yaml
navidrome:
  base_url: "<http://localhost:4533">

  # No separate config for enricher - uses same settings as provider

```

**Environment Variables** (alternative):

```bash
NAVIDROME_BASE_URL=<http://localhost:4533>

```

### Configuration Notes

- **Base URL**: The URL where your Navidrome server is accessible
- **Per-User Auth**: Each user must connect their Navidrome account
- **Same Config**: Uses same configuration as Navidrome provider
- **Local or Remote**: Works with localhost or remote Navidrome servers

### Per-User Authentication

Required for enricher to work:

1. User must connect Navidrome in Spotter preferences
2. Provides username and password
3. Stored securely in database
4. Used for Subsonic API authentication

**Note**: Navidrome provider must be connected for enricher to function.

## How to Set Up

**No API key required!** Uses Subsonic API with username/password.

### Setup Steps

1. **Configure Navidrome in Spotter**

   - Set `navidrome.base_url` in config
   - User connects via Spotter preferences

2. **Ensure Music is Scanned**

   - Navidrome must have scanned your music library
   - Check Navidrome web UI: Settings → Scanning
   - Trigger manual scan if needed

3. **Verify Connection**

   - Enricher will test connection on first use
   - Check Spotter logs for errors

4. **Optional: Enable Last.fm in Navidrome**

   - Navidrome can fetch Last.fm data (artist info, images)
   - Settings → Integrations → Last.fm
   - Improves biography and image quality

## Subsonic API Integration

Navidrome enricher uses the Subsonic API for metadata access.

### Authentication Method

Uses salt + MD5 token authentication (same as provider):

1. **Generate Random Salt**

   - 16-character hexadecimal string
   - Fresh for each request

2. **Create Token**

   - Hash: `MD5(password + salt)`
   - Convert to hex string

3. **Include in Request**

   - `u`: Username
   - `s`: Salt
   - `t`: Token
   - `c`: Client name ("spotter")
   - `v`: API version ("1.16.1")
   - `f`: Format ("json")

### Artist Info vs Artist Data

Navidrome provides two artist endpoints:

**getArtist** (`/rest/getArtist`):

- Basic artist data from library
- Album list
- Album count
- MusicBrainz ID (if tagged)
- Cover art ID

**getArtistInfo2** (`/rest/getArtistInfo2`):

- Extended metadata (often from Last.fm)
- Biography
- External URLs (Last.fm)
- Multiple image sizes

Spotter queries both and merges the data.

### Cover Art System

Navidrome serves cover art via authenticated URLs:

**Construction**:

```text
GET /rest/getCoverArt?id={coverArtId}&u={username}&s={salt}&t={token}&c=spotter&v=1.16.1

```

**Parameters**:

- `id`: Cover art ID from artist/album data
- Authentication params (u, s, t, c, v)

**Spotter Handling**:

- Generates authenticated URL
- Downloads and caches locally
- Includes in image data with full auth params

## API Limitations

### Rate Limits

- **Self-Hosted**: No rate limits for your own server
- **Recommended**: Be respectful with concurrent requests
- **Spotter**: No built-in rate limiting needed

### Data Availability

**Complete**:

- All artists, albums, tracks in scanned library
- File-based metadata (tags, duration, bitrate)

**Partial**:

- Biographies (requires Last.fm integration or file tags)
- Images (depends on embedded artwork or Last.fm)
- MusicBrainz IDs (only if files are tagged)

**Not Available**:

- Audio features (BPM in files only, no analysis)
- Popularity scores
- External IDs (Spotify, etc.)

### Known Quirks

1. **Biography Source**

   - If Last.fm integration enabled: fetches from Last.fm
   - Otherwise: uses comment/description tags from files
   - Quality varies by source

2. **Image Sizes**

   - Cover art served at original file resolution
   - Navidrome may resize on-the-fly
   - Multiple sizes available via size parameter

3. **MusicBrainz ID Priority**

   - Search by MBID more accurate than name
   - Format: `mbid:{mbid}` in search query
   - Falls back to name search if MBID not found

4. **Genre Handling**

   - Returns genre from file tags
   - May be multiple genres separated
   - Not standardized across files

5. **Search Behavior**

   - Case-insensitive
   - Partial matching supported
   - Returns multiple results (Spotter uses first)

## Implementation Notes

### Search vs Direct Lookup

**Search** (`/rest/search3`):

- Use when Navidrome ID unknown
- Searches by name or MusicBrainz ID
- Returns multiple results
- Spotter uses first match

**Direct Lookup**:

- Use when Navidrome ID known
- More efficient
- Single entity returned

### MusicBrainz Integration

For accurate matching:

1. Check if entity has MusicBrainz ID
2. Search using `mbid:{mbid}` query
3. Fall back to name search if not found
4. Verify MBID in result matches query

### Cover Art URL Generation

```go
func (e *Enricher) getCoverArtURL(coverArtID string) string {
    if coverArtID == "" {
        return ""
    }

    salt := generateSalt()
    token := generateToken(e.auth.Password, salt)

    params := url.Values{}
    params.Set("id", coverArtID)
    params.Set("u", e.user.Username)
    params.Set("s", salt)
    params.Set("t", token)
    params.Set("c", "spotter")
    params.Set("v", "1.16.1")

    return fmt.Sprintf("%s/rest/getCoverArt?%s",
        e.config.Navidrome.BaseURL, params.Encode())
}

```

### Artist Enrichment Flow

1. Check if artist has Navidrome ID
2. If not, search by MusicBrainz ID or name
3. Get artist data: `/rest/getArtist?id={id}`
4. Get artist info: `/rest/getArtistInfo2?id={id}`
5. Merge data from both endpoints
6. Return combined metadata

### Album Enrichment Flow

1. Check if album has Navidrome ID
2. If not, search with artist context
3. Get album data: `/rest/getAlbum?id={id}`
4. Extract year, genre, MusicBrainz ID
5. Get cover art URL
6. Return enriched data

### Track Enrichment Flow

1. Check if track has Navidrome ID
2. If not, search by artist + album + track name
3. Match by track number when available
4. Extract duration, BPM, MusicBrainz ID
5. Return enriched data

### Duration Conversion

Navidrome returns duration in seconds, Spotter uses milliseconds:

```go
durationMs := navidromeDuration * 1000

```

## Testing

### Running Tests

```bash

# Run Navidrome enricher tests

go test ./internal/enrichers/navidrome/...

# Run with verbose output

go test -v ./internal/enrichers/navidrome/...

# Run with coverage

go test -cover ./internal/enrichers/navidrome/...

# Run specific test

go test -run TestEnrichArtist ./internal/enrichers/navidrome/...

```

### Test Coverage

Tests cover:

- Factory creation with/without auth
- Subsonic authentication (salt + token)
- Artist enrichment (by ID and search)
- Album enrichment (by ID and search)
- Track enrichment and matching
- MusicBrainz ID search
- Cover art URL generation
- Multiple search results
- Error codes (10, 40, 70)
- Missing data handling

### Mock Subsonic Server

Tests use `httptest.NewServer`:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify authentication
    assert.NotEmpty(t, r.URL.Query().Get("u"))
    assert.NotEmpty(t, r.URL.Query().Get("s"))
    assert.NotEmpty(t, r.URL.Query().Get("t"))

    // Return mock Subsonic response
    response := map[string]interface{}{
        "subsonic-response": map[string]interface{}{
            "status": "ok",
            "artist": map[string]interface{}{
                "id":   "artist-123",
                "name": "Radiohead",
            },
        },
    }
    json.NewEncoder(w).Encode(response)
}))

```

## Troubleshooting

### "Wrong username or password" (Error 40)

- Verify Navidrome credentials are correct
- Try logging into Navidrome web UI
- Check if account is active
- Reconnect in Spotter preferences

### "Requested data not found" (Error 70)

- Artist/album/track not in Navidrome library
- Trigger a library scan in Navidrome
- Verify music files are accessible
- Check file paths are correct

### No Biography Data

- Enable Last.fm integration in Navidrome
- Navidrome Settings → Integrations → Last.fm API Key
- Or ensure files have description/comment tags
- Not all artists have biographies

### Cover Art Not Loading

- Verify cover art embedded in files or in folder
- Check Navidrome Settings → Cover Art preferences
- Ensure files are readable by Navidrome
- Try re-scanning library

### Search Returns Wrong Artist

- Use MusicBrainz IDs for accurate matching
- Tag your files with MBIDs (Picard, Beets)
- Check for duplicate artists in library
- Verify search query is correct

### Connection Timeouts

- Check Navidrome server is running
- Verify base URL is correct
- Test URL in browser
- Check firewall/network settings

### Authentication Fails After Password Change

- User must reconnect in Spotter
- Old password hash is invalid
- Disconnect and reconnect Navidrome

## API Reference

- **Navidrome Docs**: <https://www.navidrome.org/docs/>
- **Subsonic API**: <http://www.subsonic.org/pages/api.jsp>
- **OpenSubsonic**: <https://opensubsonic.netlify.app/>
- **Subsonic Endpoints**:

  - `getArtist`: <http://www.subsonic.org/pages/api.jsp#getArtist>
  - `getArtistInfo2`: <http://www.subsonic.org/pages/api.jsp#getArtistInfo2>
  - `getAlbum`: <http://www.subsonic.org/pages/api.jsp#getAlbum>
  - `search3`: <http://www.subsonic.org/pages/api.jsp#search3>
  - `getCoverArt`: <http://www.subsonic.org/pages/api.jsp#getCoverArt>

## Example Usage

```go
// Create factory
factory := navidrome.New(logger, config)

// Create enricher for user (requires Navidrome auth)
enricher, err := factory(ctx, user)
if err != nil {
    // Handle error
}
if enricher == nil {
    // User hasn't connected Navidrome
    return
}

// Enrich artist
artist := &ent.Artist{
    ID:            1,
    Name:          "Radiohead",
    MusicbrainzID: "mbid-123", // Optional but recommended
}

artistEnricher := enricher.(enrichers.ArtistEnricher)
data, err := artistEnricher.EnrichArtist(ctx, artist)
if err != nil {
    // Handle error
}
if data == nil {
    // Artist not in Navidrome library
    return
}

fmt.Printf("Navidrome ID: %s\n", data.NavidromeID)
fmt.Printf("MusicBrainz ID: %s\n", data.MusicBrainzID)
fmt.Printf("Biography: %s\n", data.Bio)

// Get artist images
images, err := artistEnricher.GetArtistImages(ctx, artist)
if err != nil {
    // Handle error
}

for _, img := range images {
    fmt.Printf("Image URL: %s\n", img.URL) // Authenticated cover art URL
    fmt.Printf("Local path: %s\n", img.LocalPath)
}

// Enrich album
album := &ent.Album{
    ID:            1,
    Name:          "OK Computer",
    MusicbrainzID: "album-mbid",
    Edges: ent.AlbumEdges{
        Artist: &ent.Artist{
            Name:        "Radiohead",
            NavidromeID: "artist-123",
        },
    },
}

albumEnricher := enricher.(enrichers.AlbumEnricher)
albumData, err := albumEnricher.EnrichAlbum(ctx, album)
if err != nil {
    // Handle error
}

fmt.Printf("Album year: %d\n", albumData.Year)
fmt.Printf("Genre: %s\n", albumData.Genre)

// Get album images
albumImages, err := albumEnricher.GetAlbumImages(ctx, album)
if err != nil {
    // Handle error
}

// Enrich track
trackNum := 2
track := &ent.Track{
    ID:          1,
    Name:        "Paranoid Android",
    TrackNumber: &trackNum,
    Edges: ent.TrackEdges{
        Artist: &ent.Artist{Name: "Radiohead"},
        Album:  &ent.Album{Name: "OK Computer", NavidromeID: "album-456"},
    },
}

trackEnricher := enricher.(enrichers.TrackEnricher)
trackData, err := trackEnricher.EnrichTrack(ctx, track)
if err != nil {
    // Handle error
}

fmt.Printf("Duration: %d ms\n", trackData.DurationMs)
fmt.Printf("BPM: %d\n", trackData.BPM) // If available in file

```

## Best Practices

1. **Tag Your Files**: Use Picard or Beets to add MusicBrainz IDs to files
2. **Enable Last.fm**: Improves biography and image quality
3. **Regular Scans**: Keep Navidrome library up-to-date
4. **Use MBIDs**: Always prefer MusicBrainz ID matching over name matching
5. **Handle Missing Data**: Not all tracks have all metadata - expect nil values
6. **Cache Cover Art**: Download once, reuse URLs with new auth params
7. **Run After MusicBrainz**: MusicBrainz enricher provides MBIDs for better matching
8. **Local First**: Navidrome enricher is fast and doesn't hit rate limits
9. **Check Scan Status**: Verify library scan completed before enriching
10. **Test Connection**: Verify Navidrome is accessible before batch enrichment

## Enrichment Order

Navidrome enricher should run **after**:

1. MusicBrainz (provides MBIDs for accurate matching)

Navidrome enricher should run **before**:

1. Lidarr
2. Spotify
3. Last.fm
4. Fanart.tv
5. OpenAI

This is the default order in `enrichers.DefaultOrder()`.

## Why Use Navidrome Enricher?

- **Local Data**: Enriches from your own music collection
- **Fast**: No external API calls, local server access
- **No Rate Limits**: Self-hosted server, unlimited requests
- **Accurate**: Data comes directly from your files
- **File Metadata**: Access to tag data (BPM, comments, etc.)
- **MusicBrainz Support**: Can provide MBIDs from file tags
- **Cover Art**: High-quality embedded artwork
- **Privacy**: Data stays on your server
- **Offline Capable**: Works without internet (if self-hosted locally)

## Local vs Remote Enrichment

**Local Navidrome** (localhost):

- Ultra-fast enrichment
- No network latency
- Offline capable
- Recommended for large libraries

**Remote Navidrome** (internet-hosted):

- Accessible from anywhere
- Depends on network speed
- Still faster than external APIs
- Good for personal cloud servers

Both use identical API and authentication.

## Comparison with Other Enrichers

| Feature | Navidrome | MusicBrainz | Spotify | Last.fm |
|---------|-----------|-------------|---------|---------|
| Data Source | Your files | Community DB | Spotify API | Community |
| Requires Auth | ✓ (per user) | ✗ | ✓ (per user) | ✗ |
| Rate Limits | None | 1/sec | 180/min | None |
| Coverage | Your library | Highest | High | High |
| Biographies | Via Last.fm | Limited | ✗ | ✓ |
| Audio Features | BPM only | ✗ | ✓ | ✗ |
| Images | ✓ (embedded) | ✗ | ✓ | ✓ |
| Offline | ✓ (local) | ✗ | ✗ | ✗ |
| Cost | Free | Free | Free | Free |

## Related Files

- `navidrome.go`: Main enricher implementation
- `navidrome_test.go`: Comprehensive unit tests
- `../../providers/navidrome/`: Navidrome provider (playlist syncing, history)
- `../enrichers.go`: Base interfaces and types
