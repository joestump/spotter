---
sidebar_position: 2
---

# MusicBrainz Enricher

MusicBrainz is a free, open music encyclopedia that provides metadata enrichment without requiring an API key.

## Features

- **No API key required**: Just configure a User-Agent
- **Canonical IDs**: MusicBrainz IDs (MBIDs) for artists, albums, and tracks
- **Comprehensive metadata**: Release dates, genres, relationships
- **Cover Art Archive**: Album artwork integration

## Configuration

```bash
# User-Agent is required by MusicBrainz API
SPOTTER_METADATA_MUSICBRAINZ_USER_AGENT="Spotter/1.0.0 (https://github.com/joestump/spotter)"
```

:::caution User-Agent Required
MusicBrainz requires a proper User-Agent string that identifies your application. Requests without a valid User-Agent may be blocked.
:::

## User-Agent Format

According to [MusicBrainz requirements](https://musicbrainz.org/doc/MusicBrainz_API/Rate_Limiting), your User-Agent should include:

- Application name
- Version
- Contact URL or email

```
ApplicationName/Version (ContactURL)
```

## Enriched Data

### Artists

- MusicBrainz ID (MBID)
- Canonical name
- Aliases and alternate names
- Country of origin
- Formation/dissolution dates
- Genre tags
- Related artists

### Albums

- MusicBrainz ID (MBID)
- Release date
- Release type (album, single, EP, etc.)
- Label information
- Track listing
- Cover artwork (via Cover Art Archive)

### Tracks

- MusicBrainz ID (MBID)
- Track number
- Duration
- ISRC codes
- Recording relationships

## Rate Limiting

MusicBrainz enforces strict rate limits:

- **1 request per second** maximum
- Requests are automatically throttled
- Bursting will result in temporary blocks

Spotter handles this automatically with built-in request queuing.

## Cover Art Archive

Album artwork is retrieved from the Cover Art Archive, which is integrated with MusicBrainz:

- Front cover
- Back cover
- Medium (CD, vinyl) images
- Booklet images

## Matching Strategy

Entities are matched using:

1. **Existing MBID**: If already stored, use it directly
2. **Search API**: Query by artist + title
3. **Confidence scoring**: Select best match above threshold

## Troubleshooting

### "Rate limit exceeded"

If you see 503 errors:
1. Spotter will automatically back off
2. Wait a few minutes before retrying
3. Ensure you're not running multiple instances

### "No results found"

If MusicBrainz can't find matches:
1. The artist/album may not be in MusicBrainz
2. Try different search terms
3. Consider contributing the data to MusicBrainz

### Wrong matches

If metadata is incorrect:
1. The automatic matching may have failed
2. Check the MusicBrainz website for the correct entry
3. Future: Manual MBID override feature planned

## Contributing to MusicBrainz

MusicBrainz is community-maintained. If your music is missing or incorrect:

1. Create a MusicBrainz account
2. Add or edit entries
3. Your contributions help everyone!

Visit [musicbrainz.org](https://musicbrainz.org/) to contribute.
