# MusicBrainz Enricher

## Overview

The MusicBrainz enricher provides comprehensive metadata from the world's largest open music database. It handles artist, album, and track metadata enrichment, plus ID matching across services. MusicBrainz is completely free and requires no API key.

## Features

- **Implements**: `ArtistEnricher`, `AlbumEnricher`, `TrackEnricher`, `IDMatcher`
- **Data provided**:

  - **Artists**: MusicBrainz ID (MBID), sort name, tags, country, area
  - **Albums**: MBID, release date, year, tags, album type (album/single/EP/etc.)
  - **Tracks**: MBID, ISRC codes, duration, tags, MusicBrainz URL
  - **Images**: Cover art from Cover Art Archive (albums only)
  - **ID Matching**: Search by name to find MBIDs with confidence scores

## Configuration

### Required Settings

```yaml
metadata:
  musicbrainz:
    user_agent: "YourApp/1.0.0 (<https://yourwebsite.com>)"  # Optional but recommended

```

**Environment Variables** (alternative):

```bash
MUSICBRAINZ_USER_AGENT="YourApp/1.0.0 (<https://yourwebsite.com>)"

```

### Configuration Notes

- **User Agent**: Recommended to identify your application
- **Default**: If not configured, uses `"Spotter/1.0.0 (<https://github.com/spotter>)"`
- **Format**: `ApplicationName/Version (Contact URL or Email)`

## How to Get API Keys

**No API key required!** MusicBrainz is a free, open-source music database.

However, you should:

1. Set a descriptive User-Agent header to identify your application
2. Follow the rate limit guidelines (max 1 request per second)
3. Consider supporting MusicBrainz: <https://metabrainz.org/donate>

## Rate Limits

### Official Limits

- **Maximum**: 1 request per second (enforced by MusicBrainz)
- **Commercial use**: Should use MusicBrainz mirror or database replication
- **Burst requests**: Not allowed - must space out requests

### Spotter Implementation

- Rate limiting enforced via mutex and sleep
- Minimum 1.1 seconds between requests (1100ms)
- Applied to all API calls automatically
- Shared across all enrichment operations

### Error Responses

- **503 Service Unavailable**: Server overloaded or maintenance
- **429 Too Many Requests**: Rate limit exceeded (shouldn't happen with our implementation)

## Data Quality

### Strengths

- **Comprehensive**: Largest open music database
- **Accurate**: Community-vetted data
- **Stable IDs**: MBIDs are permanent identifiers
- **Open Data**: Free to use and share
- **Well-Maintained**: Active community of editors

### Limitations

- **Coverage**: Some obscure artists/albums may be missing
- **Completeness**: Not all releases have cover art
- **Lag**: New releases may take time to be added
- **Consistency**: Data quality varies by popularity

### Tag Quality

- Tags sorted by vote count (most popular first)
- Only tags with count > 0 are included
- User-generated (may include unexpected values)
- Examples: "rock", "alternative", "british", "90s"

### MBID Matching

- Search returns confidence scores (0.0 to 1.0)
- Based on MusicBrainz's internal search algorithm
- Score of 1.0 = 100% match
- Lower scores indicate fuzzy matches
- Empty result = no match found

## Implementation Notes

### Rate Limiting Implementation

```go
// Enforced automatically before every API call
func (e *Enricher) rateLimit() {
    e.mu.Lock()
    defer e.mu.Unlock()

    elapsed := time.Since(e.lastCall)
    if elapsed < rateLimitDelay {
        time.Sleep(rateLimitDelay - elapsed)
    }
    e.lastCall = time.Now()
}

```

### Search vs. Lookup

**Search** (used for matching):

- Endpoint: `/ws/2/artist?query=...`
- Returns multiple results with scores
- Used when MBID is unknown
- Fuzzy matching supported

**Lookup** (used for enrichment):

- Endpoint: `/ws/2/artist/{mbid}`
- Returns single entity with full details
- Used when MBID is known
- Includes relationships, tags, ratings

### Release Group vs. Release

MusicBrainz distinguishes:

- **Release Group**: Abstract album (e.g., "Abbey Road")
- **Release**: Specific edition (e.g., "Abbey Road [2009 Remaster, US]")

Spotter uses Release Groups for albums as they represent the canonical album.

### Cover Art Archive

- Separate service built on MusicBrainz data
- URL: `<https://coverartarchive.org`>
- Uses same MBIDs as MusicBrainz
- Returns multiple images per release:

  - Front cover (primary)
  - Back cover
  - Medium/booklet images

- Thumbnails available (small: 250px, large: 500px)

### ISRC Codes

- International Standard Recording Code
- Unique identifier for sound recordings
- Format: CC-XXX-YY-NNNNN (12 characters)
- Used for cross-service track matching
- Not all tracks have ISRCs

### Year Parsing

From release dates:

- Full date: `2023-05-15` → Year: 2023
- Year-month: `2023-05` → Year: 2023
- Year only: `2023` → Year: 2023
- Partial/invalid: Returns 0

## Testing

### Running Tests

```bash

# Run MusicBrainz enricher tests

go test ./internal/enrichers/musicbrainz/...

# Run with verbose output

go test -v ./internal/enrichers/musicbrainz/...

# Run with coverage

go test -cover ./internal/enrichers/musicbrainz/...

# Run specific test

go test -run TestMatchArtist ./internal/enrichers/musicbrainz/...

```

### Test Coverage

Tests cover:

- Factory creation
- Artist/album/track matching with various scores
- Enrichment with and without MBIDs
- Rate limiting enforcement
- Error handling (503, 429, 500, 404)
- Malformed JSON responses
- Cover Art Archive integration
- Tag extraction and sorting
- Year parsing edge cases

### Mock API Server

Tests use `httptest.NewServer` to simulate MusicBrainz API:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify User-Agent header
    assert.NotEmpty(t, r.Header.Get("User-Agent"))

    // Return mock MusicBrainz response
    json.NewEncoder(w).Encode(mockResponse)
}))

```

## Troubleshooting

### "Rate Limited" Errors

- Should be rare with built-in rate limiting
- May occur if using multiple instances of Spotter
- Solution: Ensure only one instance syncing at a time

### "Service Unavailable" (503)

- MusicBrainz server is overloaded or in maintenance
- Wait and retry later
- Check status: <https://metabrainz.org/status>

### No Match Found

- Artist/album/track not in MusicBrainz database
- Try searching manually: <https://musicbrainz.org>
- Consider contributing missing data to MusicBrainz

### Incorrect Matches

- Search algorithm may match wrong entity
- Check confidence scores (< 0.9 may be unreliable)
- Manual verification recommended for low scores

### Missing Cover Art

- Not all releases have cover art in Cover Art Archive
- Check manually: <https://coverartarchive.org/release-group/{mbid}>
- Consider using other enrichers (Fanart.tv, Last.fm)

### Slow Enrichment

- Rate limiting causes 1+ second per API call
- Multiple API calls per entity (search + lookup)
- This is intentional and required
- First sync is slowest; subsequent syncs are faster

## API Reference

- **MusicBrainz API**: <https://musicbrainz.org/doc/MusicBrainz_API>
- **API Docs**: <https://musicbrainz.org/doc/Development/XML_Web_Service/Version_2>
- **Rate Limiting**: <https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting>
- **Cover Art Archive**: <https://musicbrainz.org/doc/Cover_Art_Archive/API>
- **Search Syntax**: <https://musicbrainz.org/doc/Indexed_Search_Syntax>

## Example Usage

```go
// Create factory
factory := musicbrainz.New(logger, config)

// Create enricher
enricher, err := factory(ctx, nil)
if err != nil {
    // Handle error
}

// Match artist by name
matcher := enricher.(enrichers.IDMatcher)
mbid, confidence, err := matcher.MatchArtist(ctx, "Radiohead")
if err != nil {
    // Handle error
}
fmt.Printf("MBID: %s, Confidence: %.2f\n", mbid, confidence)

// Enrich artist with full metadata
artist := &ent.Artist{
    Name: "Radiohead",
    MusicbrainzID: mbid,
}

artistEnricher := enricher.(enrichers.ArtistEnricher)
data, err := artistEnricher.EnrichArtist(ctx, artist)
if err != nil {
    // Handle error
}

fmt.Printf("Tags: %v\n", data.Tags)
fmt.Printf("Sort Name: %s\n", data.SortName)

// Get album cover art
album := &ent.Album{
    Name: "OK Computer",
    MusicbrainzID: "album-mbid",
}

albumEnricher := enricher.(enrichers.AlbumEnricher)
images, err := albumEnricher.GetAlbumImages(ctx, album)
if err != nil {
    // Handle error
}

for _, img := range images {
    fmt.Printf("Image: %s (Type: %s, Primary: %v)\n",
        img.URL, img.Type, img.IsPrimary)
}

```

## Best Practices

1. **Always Set User-Agent**: Identify your application properly
2. **Respect Rate Limits**: Don't bypass the built-in rate limiting
3. **Cache MBIDs**: Once found, store them to avoid repeated searches
4. **Handle 503 Gracefully**: MusicBrainz can be unavailable
5. **Check Confidence Scores**: Scores < 0.8 may need verification
6. **Use in Enrichment Pipeline**: Run early (before other enrichers)
7. **Contribute Back**: Add missing data to MusicBrainz when found

## Why Use MusicBrainz?

- **First in Enrichment Order**: Provides stable IDs for other enrichers
- **Open Data**: No API key, no rate limit headaches (just respect 1/sec)
- **Comprehensive**: Excellent coverage for mainstream and indie artists
- **Stable IDs**: MBIDs never change, unlike names
- **Community**: Large active community maintaining data
- **Relationships**: Links artists, albums, tracks, and releases

## Related Files

- `musicbrainz.go`: Main enricher implementation
- `musicbrainz_test.go`: Comprehensive unit tests
- `../enrichers.go`: Base interfaces and types
