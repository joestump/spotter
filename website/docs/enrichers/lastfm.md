---
sidebar_position: 4
---

# Last.fm Enricher

The Last.fm enricher provides community-driven metadata including tags, biographies, and artist information.

## Features

- **Community tags**: Genre and mood tags from millions of users
- **Artist biographies**: Detailed artist information
- **Similar artists**: Related artist recommendations
- **Play statistics**: Global play counts

## Configuration

Uses the same credentials as the Last.fm Provider:

```bash
SPOTTER_LASTFM_API_KEY=your_api_key
SPOTTER_LASTFM_SHARED_SECRET=your_shared_secret
```

## Enriched Data

### Artists

- Biography (wiki summary)
- Tags (genres, moods, eras)
- Similar artists
- Play count (global)
- Listener count
- Last.fm URL

### Albums

- Wiki summary
- Tags
- Play count
- Listener count
- Last.fm URL

### Tracks

- Tags
- Play count
- Listener count
- Last.fm URL

## Tag System

Last.fm tags are community-driven and include:

- **Genres**: rock, electronic, jazz, etc.
- **Moods**: chill, energetic, melancholic
- **Eras**: 80s, 90s, 2000s
- **Descriptors**: female vocalists, instrumental, acoustic

Tags are weighted by usage, allowing Spotter to filter for the most relevant ones.

## Rate Limiting

Last.fm has generous rate limits:

- No strict per-second limits
- Recommended: 200ms between requests
- Spotter handles throttling automatically

## Troubleshooting

### "Invalid API key"

1. Verify your API key is correct
2. Check it's active at last.fm/api/accounts
3. Try regenerating the key

### Missing Biographies

Not all artists have biographies:

- Lesser-known artists may lack content
- Biographies are user-contributed
- Consider contributing to Last.fm

### Tag Quality

Tags are community-generated:

- Popular artists have better tags
- Obscure artists may have fewer/no tags
- AI enrichment can supplement missing tags
