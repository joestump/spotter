# Last.fm Enricher

## Overview

The Last.fm enricher provides community-curated metadata including artist biographies, tags (genres), and images. Unlike the Last.fm provider (which fetches listening history), this enricher focuses on enhancing artist, album, and track metadata with Last.fm's rich community-generated content. No user authentication required.

## Features

- **Implements**: `ArtistEnricher`, `AlbumEnricher`, `TrackEnricher`
- **Data provided**:

  - **Artists**: Biography (cleaned HTML), tags, Last.fm URL, images (5 sizes)
  - **Albums**: Tags, Last.fm URL, album artwork (5 sizes)
  - **Tracks**: Tags, duration, MusicBrainz ID
  - **Images**: Small (34px), Medium (64px), Large (174px), Extralarge (300px), Mega (500px)
  - **Tags**: Community-voted genre/style tags
  - **Bio Cleaning**: Automatic HTML tag removal and "Read more" link removal

## Configuration

### Required Settings

```yaml
lastfm:
  api_key: "your-api-key"

  # shared_secret not required for enricher (only for provider auth)

```

**Environment Variables** (alternative):

```bash
LASTFM_API_KEY=your-api-key

```

### Configuration Notes

- **API Key**: Required for all API requests
- **No Shared Secret**: Enricher doesn't need authentication (read-only operations)
- **No User Auth**: Unlike provider, enricher works without user connection
- **Free Tier**: Generous limits for non-commercial use

## How to Get API Keys

1. **Go to Last.fm API Account Creation**

   - Visit: <https://www.last.fm/api/account/create>

2. **Fill in Application Details**

   - **Application name**: `Spotter` (or your custom name)
   - **Application description**: Brief description of your use case
   - **Callback URL**: Not required for enricher (leave empty or use dummy)

3. **Submit the Form**

   - After submission, you'll receive your **API Key** and **Shared Secret**

4. **Copy API Key**

   - Copy only the API Key (Shared Secret not needed for enricher)
   - Add to your Spotter configuration file

5. **Test the Key**

   - Spotter will validate the key on first enrichment
   - Check logs for any API key errors

**Note**: Same API key can be used for both Last.fm provider and enricher.

## Data Quality

### Strengths

- **Community Curated**: Millions of users contribute data
- **Rich Biographies**: Detailed artist backgrounds from Wikipedia/MusicBrainz
- **Comprehensive Tags**: Genre, style, mood, era tags
- **Good Coverage**: Excellent for popular and indie artists
- **Active Community**: Data constantly updated by users
- **Free Access**: Generous API limits for read operations

### Limitations

- **Coverage Gaps**: Obscure artists may have minimal data
- **HTML in Responses**: Biographies contain HTML tags (Spotter cleans automatically)
- **"Read More" Links**: Bio truncated with Last.fm link (Spotter removes)
- **Tag Quality Variance**: User-generated tags can be inconsistent
- **No Audio Features**: No BPM, key, or technical analysis
- **Image Quality**: Lower resolution than Spotify or Fanart.tv

### Biography Data

**Raw Format**:

- Contains HTML tags: `<p>`, `<strong>`, `<em>`, `<a>`, `<br>`
- Truncated with "Read more on Last.fm" link
- May include Creative Commons attribution

**Spotter Cleaning**:

- Removes all HTML tags
- Removes "Read more on Last.fm" link and everything after
- Preserves paragraph structure
- Trims whitespace

**Example**:

```text
Raw: "<p>Radiohead are an <strong>English</strong> rock band. <a href=\"<https://www.last.fm/music/Radiohead\>">Read more on Last.fm</a>.</p>"
Cleaned: "Radiohead are an English rock band."

```

### Image Sizes

Last.fm provides 5 standard sizes:

| Size | Dimensions | Use Case |
|------|------------|----------|
| `small` | 34x34 | Thumbnails, lists |
| `medium` | 64x64 | Small avatars |
| `large` | 174x174 | Standard display |
| `extralarge` | 300x300 | Large display (primary) |
| `mega` | 500x500 | Maximum resolution |

Spotter downloads all sizes and marks `extralarge` as primary.

### Tags System

- **Community Voted**: Users apply tags to artists/albums/tracks
- **Sorted by Popularity**: Most popular tags first
- **Varied Types**: Genre (rock), style (alternative), mood (melancholic), era (90s), nationality (british)
- **Not Hierarchical**: Tags are flat, not organized in taxonomy
- **Case Sensitive**: "Rock" and "rock" are different (rare but possible)

## API Limitations

### Rate Limits

- **No Official Rate Limit**: For standard read operations
- **No Authentication Required**: API key only
- **Recommended**: Don't exceed 5 requests per second
- **Spotter**: No built-in rate limiting (relies on Last.fm)

### Data Availability

- **Most Artists**: Have basic info (name, URL)
- **Many Artists**: Have biographies and tags
- **Some Artists**: Have high-quality images
- **All Tracks**: Can query, but data varies

### Error Codes

Last.fm uses numeric error codes in JSON responses:

| Code | Meaning | Handling |
|------|---------|----------|
| 2 | Invalid service | Configuration error |
| 6 | Not found | Return nil (not an error) |
| 10 | Invalid API key | Check configuration |
| 11 | Service offline | Temporary, retry later |
| 13 | Invalid method signature | Internal error |
| 16 | Service temporarily unavailable | Retry with backoff |
| 26 | Suspended API key | Contact Last.fm |
| 29 | Rate limit exceeded | Wait before retrying |

**Error Code 6**: Most common, indicates artist/album/track not in database. Spotter treats this as "no data" (returns nil, not error).

### Known Quirks

1. **Autocorrect Parameter**

   - Setting `autocorrect=1` fixes typos in artist names
   - Example: "Radiohedd" → "Radiohead"
   - Always enabled in Spotter

2. **MusicBrainz ID Priority**

   - If MBID provided, Last.fm uses it for matching
   - More accurate than name-based search
   - Recommended when available

3. **Bio Truncation**

   - Biographies truncated to ~500 characters
   - Link to full bio on Last.fm website
   - Spotter removes truncation notice

4. **Empty Image URLs**

   - Some images have empty `#text` field
   - Spotter filters these out automatically

5. **No Pagination**

   - Single request returns all available data
   - No need for pagination logic

## Implementation Notes

### Bio Cleaning Algorithm

```go
func cleanBio(bio string) string {
    // 1. Remove "Read more on Last.fm" link
    if idx := strings.Index(bio, "<a href=\"<https://www.last.fm/">); idx != -1 {
        bio = bio[:idx]
    }

    // 2. Strip HTML tags
    bio = stripHTML(bio)

    // 3. Trim whitespace
    return strings.TrimSpace(bio)
}

```

### HTML Stripping

- Removes all HTML tags: `<tag>content</tag>` → `content`
- Preserves text content
- Handles self-closing tags: `<br/>`
- Simple state machine (not a full HTML parser)

### Image Download

- Downloads all 5 sizes
- Resizes to max 1024px (consistent with other enrichers)
- Saves as PNG: `data/images/artists/{id}_lastfm_{size}.png`
- Sorts by size (largest first)
- Marks `extralarge` as primary

### Tag Extraction

- Extracts from `tags.tag` array
- Filters out empty tag names
- Preserves order (popularity-sorted)
- No deduplication (assumes Last.fm provides unique tags)

### MusicBrainz Integration

If entity has MusicBrainz ID:

- Passes as `mbid` parameter to Last.fm
- Last.fm uses MBID for accurate matching
- Falls back to name if MBID not found

### API Request Format

```text
GET <https://ws.audioscrobbler.com/2.0/>
  ?method=artist.getinfo
  &artist=Radiohead
  &autocorrect=1
  &api_key=YOUR_KEY
  &format=json

```

## Testing

### Running Tests

```bash

# Run Last.fm enricher tests

go test ./internal/enrichers/lastfm/...

# Run with verbose output

go test -v ./internal/enrichers/lastfm/...

# Run with coverage

go test -cover ./internal/enrichers/lastfm/...

# Run specific test

go test -run TestEnrichArtist ./internal/enrichers/lastfm/...

```

### Test Coverage

Tests cover:

- Factory creation with/without API key
- Artist enrichment with bio, tags, images
- Album enrichment with tags
- Track enrichment with tags, duration
- Bio HTML stripping (all common tags)
- "Read more" link removal
- Image size mapping
- Empty image URL filtering
- All error codes (2, 6, 10, 11, 16)
- MusicBrainz ID parameter
- Autocorrect parameter
- Missing data handling

### Mock API Server

Tests use `httptest.NewServer`:

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // Verify API key
    assert.Equal(t, "test-api-key", r.URL.Query().Get("api_key"))

    // Return mock Last.fm response
    response := lastfmArtistResponse{
        Artist: lastfmArtist{
            Name: "Radiohead",
            Bio: lastfmBio{
                Content: "Artist biography...",
            },
            Tags: struct {
                Tag []lastfmTag `json:"tag"`
            }{
                Tag: []lastfmTag{
                    {Name: "alternative rock"},
                },
            },
        },
    }
    json.NewEncoder(w).Encode(response)
}))

```

## Troubleshooting

### "Invalid API Key" (Error 10)

- Verify API key is correct in configuration
- Check for extra whitespace or quotes
- Ensure key is active (check Last.fm API dashboard)
- Test key manually: `curl "<http://ws.audioscrobbler.com/2.0/?method=artist.getinfo&artist=Radiohead&api_key=YOUR_KEY&format=json"`>

### No Data Returned

- Artist/album/track may not be in Last.fm database
- Search manually: <https://www.last.fm/music/>
- Try autocorrect (automatically enabled)
- Check for typos in names

### "Service Offline" (Error 11)

- Last.fm is temporarily down
- Check status: <https://twitter.com/lastfmstatus>
- Wait and retry later
- Consider caching data

### Bio Contains HTML

- Should not happen - Spotter cleans automatically
- Check cleanBio function is being called
- Verify stripHTML is working correctly
- Report as bug if HTML appears in final data

### Images Not Downloading

- Check network connectivity
- Verify image URLs are valid (not empty)
- Check disk space and permissions
- Ensure `data/images/` directory exists

### "Rate Limit Exceeded" (Error 29)

- Slow down enrichment frequency
- Add delay between requests
- Consider caching enriched data
- Contact Last.fm for increased limits

### Missing Tags

- Some artists have no tags
- This is expected - not an error
- Users must tag artists on Last.fm
- Consider using other enrichers for genres

### Empty Biographies

- Many artists lack biographies
- Especially true for new/obscure artists
- Try other enrichers (OpenAI, MusicBrainz)
- Consider contributing to Last.fm wiki

## API Reference

- **Last.fm API Docs**: <https://www.last.fm/api>
- **artist.getinfo**: <https://www.last.fm/api/show/artist.getInfo>
- **album.getinfo**: <https://www.last.fm/api/show/album.getInfo>
- **track.getinfo**: <https://www.last.fm/api/show/track.getInfo>
- **Error Codes**: <https://www.last.fm/api/errorcodes>
- **API Account**: <https://www.last.fm/api/account/create>

## Example Usage

```go
// Create factory
factory := lastfm.New(logger, config)

// Create enricher (no user required)
enricher, err := factory(ctx, nil)
if err != nil {
    // Handle error
}
if enricher == nil {
    // No API key configured
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
    // Artist not found on Last.fm
    return
}

fmt.Printf("Bio: %s\n", data.Bio) // HTML-cleaned
fmt.Printf("Tags: %v\n", data.Tags) // ["alternative rock", "indie", ...]
fmt.Printf("Last.fm URL: %s\n", data.LastFMURL)

// Get artist images
images, err := artistEnricher.GetArtistImages(ctx, artist)
if err != nil {
    // Handle error
}

for _, img := range images {
    fmt.Printf("Image (%s): %dx%d - %s\n",
        img.Source, img.Width, img.Height, img.URL)
    // Primary: extralarge (300x300)
    if img.IsPrimary {
        fmt.Printf("Primary image: %s\n", img.LocalPath)
    }
}

// Enrich album
album := &ent.Album{
    ID:   1,
    Name: "OK Computer",
    Edges: ent.AlbumEdges{
        Artist: &ent.Artist{Name: "Radiohead"},
    },
}

albumEnricher := enricher.(enrichers.AlbumEnricher)
albumData, err := albumEnricher.EnrichAlbum(ctx, album)
if err != nil {
    // Handle error
}

fmt.Printf("Album tags: %v\n", albumData.Tags)

// Enrich track
track := &ent.Track{
    ID:   1,
    Name: "Paranoid Android",
    Edges: ent.TrackEdges{
        Artist: &ent.Artist{Name: "Radiohead"},
    },
}

trackEnricher := enricher.(enrichers.TrackEnricher)
trackData, err := trackEnricher.EnrichTrack(ctx, track)
if err != nil {
    // Handle error
}

fmt.Printf("Track tags: %v\n", trackData.Tags)
fmt.Printf("Duration: %d ms\n", trackData.DurationMs)

```

## Best Practices

1. **Always Provide MusicBrainz IDs**: Run MusicBrainz enricher first for accurate matching
2. **Cache Biographies**: Bio data rarely changes, cache to reduce API calls
3. **Handle Missing Data**: Many entities have no bio or images - this is normal
4. **Use Autocorrect**: Enabled by default, fixes typos automatically
5. **Download All Image Sizes**: Different sizes useful for different contexts
6. **Filter Empty Tags**: Check for empty strings in tag arrays
7. **Respect API**: Don't hammer the API, even without rate limits
8. **Combine with Other Enrichers**: Last.fm best as supplement, not sole source
9. **Log Error Codes**: Track error patterns to identify issues
10. **Test API Key**: Validate key before running full enrichment

## Enrichment Order

Last.fm enricher should run **after**:

1. MusicBrainz (provides MBIDs for accurate matching)
2. Lidarr
3. Navidrome
4. Spotify

Last.fm enricher should run **before**:

1. OpenAI (AI uses all metadata including Last.fm tags)

This is the default order in `enrichers.DefaultOrder()`.

## Why Use Last.fm Enricher?

- **Community Data**: Benefits from millions of users' contributions
- **Rich Biographies**: Detailed artist backgrounds
- **Comprehensive Tags**: Genre, style, mood, era classifications
- **No Authentication**: Simple setup, just API key
- **Free**: No cost for non-commercial use
- **Good Coverage**: Most artists have some data
- **Active Community**: Constantly updated by users
- **Historical Data**: Years of accumulated information

## Comparison with Other Enrichers

| Feature | Last.fm | MusicBrainz | Spotify | OpenAI |
|---------|---------|-------------|---------|--------|
| Biographies | ✓ | Limited | ✗ | ✓ (AI) |
| Tags/Genres | ✓ | ✓ | ✓ | ✓ (AI) |
| Images | ✓ | ✗ | ✓ | ✗ |
| Audio Features | ✗ | ✗ | ✓ | ✗ |
| Free API | ✓ | ✓ | ✓ | ✗ |
| No Auth Required | ✓ | ✓ | ✗ | ✓ |
| Rate Limit | None | 1/sec | 180/min | Pay |
| Coverage | High | Highest | High | Any |
| Data Quality | Good | Excellent | Excellent | Variable |

## Related Files

- `lastfm.go`: Main enricher implementation
- `lastfm_test.go`: Comprehensive unit tests
- `../../providers/lastfm/`: Last.fm provider (different purpose - listening history)
- `../enrichers.go`: Base interfaces and types
