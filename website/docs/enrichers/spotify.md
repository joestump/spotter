---
sidebar_position: 3
---

# Spotify Enricher

The Spotify enricher provides audio features and high-quality metadata from Spotify's extensive database.

## Features

- **Audio Features**: BPM, key, energy, danceability, and more
- **High-resolution artwork**: Album and artist images
- **Popularity scores**: Track and artist popularity
- **External IDs**: Spotify URIs and ISRCs

## Requirements

The Spotify enricher requires user authentication via the Spotify Provider. See [Spotify Provider](/docs/providers/spotify) for setup instructions.

## Audio Features

Spotify provides detailed audio analysis for tracks:

| Feature | Range | Description |
| :--- | :--- | :--- |
| BPM (tempo) | 0-250 | Beats per minute |
| Key | 0-11 | Musical key (C=0, C#=1, etc.) |
| Mode | 0-1 | Major (1) or minor (0) |
| Danceability | 0-1 | How suitable for dancing |
| Energy | 0-1 | Intensity and activity |
| Valence | 0-1 | Musical positiveness |
| Acousticness | 0-1 | Acoustic vs electronic |
| Instrumentalness | 0-1 | Likelihood of no vocals |
| Liveness | 0-1 | Presence of live audience |
| Speechiness | 0-1 | Presence of spoken words |

## Enriched Data

### Artists

- Spotify ID
- High-resolution images (multiple sizes)
- Genres
- Popularity score
- Follower count

### Albums

- Spotify ID
- Cover artwork (640px, 300px, 64px)
- Release date precision
- Album type (album, single, compilation)
- Label
- Copyrights

### Tracks

- Spotify ID
- ISRC code
- Audio features (see above)
- Duration (milliseconds)
- Popularity score
- Preview URL

## Configuration

No additional configuration needed beyond the Spotify Provider setup:

```bash
SPOTTER_SPOTIFY_CLIENT_ID=your_client_id
SPOTTER_SPOTIFY_CLIENT_SECRET=your_client_secret
SPOTTER_SPOTIFY_REDIRECT_URL=http://localhost:8080/auth/spotify/callback
```

## Matching Strategy

Entities are matched using:

1. **ISRC**: International Standard Recording Code (most reliable)
2. **Spotify ID**: If previously stored
3. **Search**: Artist + album/track name

## Rate Limiting

Spotify has dynamic rate limits:

- Limits vary by endpoint
- 429 errors include retry-after header
- Spotter handles backoff automatically

## Troubleshooting

### "User not authenticated"

The Spotify enricher requires a connected Spotify account:

1. Go to **Preferences** > **Services**
2. Connect your Spotify account
3. Re-run enrichment

### Missing Audio Features

Not all tracks have audio features:

- Local files won't have features
- Some regional content may be limited
- Very new releases may not be analyzed yet

### Wrong Matches

If Spotify matches incorrect tracks:

1. The automatic search may have failed
2. Try refreshing the entity
3. Manual override planned for future
