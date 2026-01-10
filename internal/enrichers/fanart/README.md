# Fanart.tv Enricher

## Overview

The Fanart.tv enricher provides high-quality images for artists and albums. It's a community-driven image database specializing in HD logos, backgrounds, banners, and album artwork. Fanart.tv requires a MusicBrainz ID for lookups.

## Features

- **Implements**: `ArtistEnricher`, `AlbumEnricher`
- **Data provided**:

  - **Artist Images**:
    - HD music logos (vector-quality)
    - Standard music logos
    - Artist backgrounds
    - Artist fanart
    - Artist thumbnails
    - Music banners
  - **Album Images**:
    - CD art (disc artwork)
    - Album covers
  - **Metadata**: Likes count for each image (community votes)
  - **Prioritization**: HD logos prioritized over standard logos

## Configuration

### Required Settings

```yaml
metadata:
  fanart:
    api_key: "your-api-key"

```

**Environment Variables** (alternative):

```bash
FANART_API_KEY=your-api-key

```

### Configuration Notes

- **API Key**: Required for all requests
- **No API Secret**: Only API key is needed (simpler than OAuth)
- **Personal vs. Project Key**: Free personal keys available; project keys for commercial use

## How to Get API Keys

### Free Personal API Key

1. **Visit Fanart.tv**

   - Go to: <https://fanart.tv>

2. **Create an Account**

   - Click "Register" in the top-right corner
   - Fill in username, email, and password
   - Verify your email address

3. **Get Your API Key**

   - Log in to your account
   - Go to your profile settings
   - Navigate to "API" or "Personal API Key" section
   - Copy your personal API key

4. **Add to Spotter Config**

   - Paste the API key into your configuration file

### Project API Key (Commercial Use)

For commercial use or higher rate limits:

1. Visit: <https://fanart.tv/get-an-api-key/>
2. Fill out the project application form
3. Explain your use case
4. Wait for approval (usually quick)
5. Receive project API key with higher limits

## Rate Limits

### Official Limits

- **Personal Key**:

  - 1,000 requests per day
  - ~40 requests per hour

- **Project Key**:

  - 10,000 requests per day
  - ~400 requests per hour

- **Rate Limit Response**: HTTP 429 (Too Many Requests)

### Spotter Implementation

- No built-in rate limiting (relies on Fanart.tv's enforcement)
- Recommend running enrichment during off-peak hours
- Images are cached locally after first download
- Subsequent syncs don't re-download existing images

### Best Practices

- Don't run enrichment too frequently (once per day is sufficient)
- Images rarely change on Fanart.tv
- Cache images locally to minimize API calls

## Data Quality

### Strengths

- **High Quality**: Professional-grade images, many in HD
- **Variety**: Multiple image types per artist/album
- **Community Curated**: Users vote on best images (likes)
- **HD Logos**: Vector-quality logos for artist branding
- **Transparent PNGs**: Logos and art with transparency

### Limitations

- **Coverage**: Less complete than MusicBrainz or Spotify
- **Requires MBID**: Must have MusicBrainz ID before enriching
- **No Artist Images Without MBID**: Artist lookup uses MBID only
- **Album Art Complexity**: Albums nested under artist MBID
- **Mainstream Bias**: Popular artists have more/better images

### Image Types Explained

**Artist Images**:

- `hdmusiclogo`: High-definition logo (preferred)
- `musiclogo`: Standard logo
- `artistbackground`: Backdrop/wallpaper images
- `artistfanart`: Fan art variations
- `artistthumb`: Thumbnail/avatar images
- `musicbanner`: Wide banner images

**Album Images**:

- `cdart`: Disc artwork (transparent disc with album art)
- `albumcover`: Front cover artwork

### Likes System

- Each image has a "likes" count
- Higher likes = more popular/better quality
- Used for sorting/prioritizing images
- Not a guarantee of quality

## Implementation Notes

### MusicBrainz ID Requirement

**Critical**: Fanart.tv lookups require MusicBrainz IDs:

```go
// Artist must have MBID
if artist.MusicbrainzID == "" {
    return nil, nil // Skip
}

// Album requires artist MBID + album MBID
if album.Edges.Artist.MusicbrainzID == "" || album.MusicbrainzID == "" {
    return nil, nil // Skip
}

```

**Recommendation**: Run MusicBrainz enricher first to populate MBIDs.

### Image Priority

**HD logos prioritized**:

1. HD music logos (`hdmusiclogo`) - marked as primary
2. Standard music logos (`musiclogo`) - secondary
3. Other images by type

**Primary flag**: First image of each type gets `IsPrimary: true`

### Album Art Lookup

Fanart.tv organizes album art under artist:

1. Query: `/music/{artist-mbid}`
2. Response contains `albums` object
3. Albums keyed by album MBID
4. Must match specific album MBID in response

```json
{
  "name": "Artist Name",
  "mbid_id": "artist-mbid",
  "albums": {
    "album-mbid-1": {
      "cdart": [...],
      "albumcover": [...]
    },
    "album-mbid-2": { ... }
  }
}

```

### Image Download Integration

Uses `enrichers.DownloadAndSaveImage()`:

- Checks if image already exists (skips re-download)
- Downloads from remote URL
- Resizes to max 1024px
- Converts to PNG
- Saves to local path: `data/images/artists/{id}_fanart_{image-id}.png`

### Error Handling

- **404 Not Found**: No data for that MBID (returns nil, nil)
- **401 Unauthorized**: Invalid API key
- **429 Rate Limited**: Too many requests
- **500 Server Error**: Fanart.tv service issue

### Metadata Returns Nil

Fanart.tv only provides images, not metadata:

```go
func (e *Enricher) EnrichArtist(ctx context.Context, artist *ent.Artist) (*enrichers.ArtistData, error) {
    return nil, nil // Only images, no metadata
}

```

Use `GetArtistImages()` and `GetAlbumImages()` instead.

## Testing

### Running Tests

```bash

# Run Fanart.tv enricher tests

go test ./internal/enrichers/fanart/...

# Run with verbose output

go test -v ./internal/enrichers/fanart/...

# Run with coverage

go test -cover ./internal/enrichers/fanart/...

# Run specific test

go test -run TestGetArtistImages ./internal/enrichers/fanart/...

```

### Test Coverage

Tests cover:

- Factory creation with/without API key
- Artist images retrieval
- Album images retrieval
- Missing MBID handling
- 404 Not Found responses
- Server errors (500)
- Malformed JSON
- Multiple image types
- HD logo prioritization
- Likes parsing
- Album not in response

### Mock API Server

Tests use `httptest.NewServer`:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    assert.Contains(t, r.URL.RawQuery, "api_key=test-api-key")

    response := fanartArtistResponse{
        Name: "Test Artist",
        MBID: "mbid-123",
        HDMusicLogo: []fanartImage{
            {ID: "1", URL: "http://example.com/logo.png", Likes: "42"},
        },
    }
    json.NewEncoder(w).Encode(response)
}))

```

## Troubleshooting

### "Invalid API Key"

- Verify API key is correct in config
- Check for extra whitespace
- Ensure key is active (check Fanart.tv account)

### No Images Returned

- **Most Common**: Artist/album not in Fanart.tv database
- Check manually: <https://fanart.tv/music/{mbid}/>
- Fanart.tv has less coverage than MusicBrainz
- Popular artists have better coverage

### "No MusicBrainz ID"

- Fanart.tv requires MBID to lookup
- Run MusicBrainz enricher first
- Check that entity has `musicbrainz_id` field populated

### Album Images Not Found

- Album might not have images in Fanart.tv
- Artist MBID required (even for album lookup)
- Album MBID must also be present
- Check nested structure in API response

### Rate Limit Errors (429)

- You've exceeded daily/hourly limits
- Wait until limit resets (usually next day/hour)
- Consider project API key for higher limits
- Reduce enrichment frequency

### Image Download Fails

- Check network connectivity
- Verify image URL is accessible
- Check disk space for image storage
- Ensure write permissions on `data/images/` directory

## API Reference

- **Official Docs**: <https://fanart.tv/api-docs/>
- **Artist API**: <https://fanart.tv/api-docs/music-api/>
- **Get API Key**: <https://fanart.tv/get-an-api-key/>
- **Browse Music**: <https://fanart.tv/music/>

## Example Usage

```go
// Create factory
factory := fanart.New(logger, config)

// Create enricher
enricher, err := factory(ctx, nil)
if err != nil {
    // Handle error
}
if enricher == nil {
    // No API key configured
}

// Get artist images
artist := &ent.Artist{
    ID: 1,
    Name: "Radiohead",
    MusicbrainzID: "a74b1b7f-71a5-4011-9441-d0b5e4122711",
}

artistEnricher := enricher.(enrichers.ArtistEnricher)
images, err := artistEnricher.GetArtistImages(ctx, artist)
if err != nil {
    // Handle error
}

for _, img := range images {
    fmt.Printf("Image: %s\n", img.Type)
    fmt.Printf("  URL: %s\n", img.URL)
    fmt.Printf("  Local: %s\n", img.LocalPath)
    fmt.Printf("  Likes: %d\n", img.Likes)
    fmt.Printf("  Primary: %v\n", img.IsPrimary)
}

// Get album images
album := &ent.Album{
    ID: 10,
    Name: "OK Computer",
    MusicbrainzID: "album-mbid-123",
    Edges: ent.AlbumEdges{
        Artist: &ent.Artist{
            MusicbrainzID: "a74b1b7f-71a5-4011-9441-d0b5e4122711",
        },
    },
}

albumEnricher := enricher.(enrichers.AlbumEnricher)
images, err = albumEnricher.GetAlbumImages(ctx, album)
if err != nil {
    // Handle error
}

```

## Usage Best Practices

1. **Run MusicBrainz First**: Always populate MBIDs before Fanart.tv
2. **Cache Images**: Don't re-download; check if local file exists
3. **Prioritize HD**: Use HD logos when available
4. **Check Likes**: Higher likes often means better quality
5. **Handle Missing Data**: Many entities won't have images
6. **Enrich Infrequently**: Images rarely change; once per week is enough
7. **Monitor Rate Limits**: Track API calls to avoid hitting limits
8. **Fallback to Other Sources**: Use Last.fm or Spotify if Fanart.tv has nothing

## Enrichment Order

Fanart.tv should run **after**:

1. MusicBrainz (provides required MBIDs)
2. Lidarr
3. Navidrome
4. Spotify
5. Last.fm

Fanart.tv should run **before**:

1. OpenAI (AI enrichment uses images)

This is the default order in `enrichers.DefaultOrder()`.

## Why Use Fanart.tv?

- **HD Quality**: Best source for high-resolution logos and art
- **Transparent PNGs**: Professional logos with transparency
- **Multiple Options**: Several images per entity to choose from
- **Community Curated**: Best images rise to top via likes
- **Simple API**: Easy to integrate, just needs API key
- **Complements MusicBrainz**: Good pairing for images

## Limitations to Consider

- Requires MusicBrainz IDs (dependency)
- Less coverage than major services
- Rate limits can be restrictive with free key
- Mainstream artists only (generally)
- No metadata enrichment (images only)

## Related Files

- `fanart.go`: Main enricher implementation
- `fanart_test.go`: Comprehensive unit tests
- `../enrichers.go`: Base interfaces and types
- `../images.go`: Image download utilities
