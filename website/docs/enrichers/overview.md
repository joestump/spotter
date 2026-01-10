---
sidebar_position: 1
---

# Enrichers Overview

Enrichers enhance your music library metadata from various sources. They run automatically and in a configurable priority order.

## Available Enrichers

| Enricher | API Key | Description |
| :--- | :--- | :--- |
| [MusicBrainz](/docs/enrichers/musicbrainz) | Not required | Open music database |
| [Spotify](/docs/enrichers/spotify) | Required (OAuth) | Audio features, artwork |
| [Last.fm](/docs/enrichers/lastfm) | Required | Tags, biographies |
| [Fanart.tv](/docs/enrichers/fanart) | Required | High-quality images |
| [Lidarr](/docs/enrichers/lidarr) | Required | Music collection manager |
| [OpenAI](/docs/enrichers/openai) | Required | AI-generated content |

## How Enrichment Works

1. **Scheduling**: Enrichment runs periodically based on `SPOTTER_METADATA_INTERVAL`
2. **Priority Order**: Enrichers process entities in the configured order
3. **Data Merging**: Later enrichers can override data from earlier ones
4. **AI Enhancement**: OpenAI runs last to summarize all collected data

## Configuration

```bash
# Enable enrichment
SPOTTER_METADATA_ENABLED=true

# Run every hour
SPOTTER_METADATA_INTERVAL=1h

# Processing order (left to right)
SPOTTER_METADATA_ORDER=lidarr,musicbrainz,navidrome,spotify,lastfm,fanart,openai
```

## Enrichment Order Strategy

The default order is designed so that:

1. **Lidarr** provides initial structure and organization
2. **MusicBrainz** adds canonical IDs and basic metadata
3. **Navidrome** adds local server data
4. **Spotify** adds audio features and high-quality images
5. **Last.fm** adds community tags and biographies
6. **Fanart.tv** adds high-quality artwork
7. **OpenAI** generates summaries from all available data

## Manual Enrichment

Trigger enrichment manually from **Preferences** > **Tasks**:

- **Run Metadata Enricher**: Process all entities
- **Sync All Artist Images**: Refresh artist images
- **Sync All Album Art**: Refresh album artwork

## Enriched Entity Types

### Artists

- Names and aliases
- Biographies
- Genres and tags
- Profile images
- Background images
- Logos
- External IDs

### Albums

- Titles
- Release dates
- Genres and tags
- Cover artwork (multiple sizes)
- Track listings

### Tracks

- Titles
- Durations
- Audio features (BPM, key, energy)
- Genres and tags
- ISRC codes

## Rate Limiting

Each enricher respects API rate limits:

| Enricher | Rate Limit |
| :--- | :--- |
| MusicBrainz | 1 req/second |
| Spotify | Dynamic |
| Last.fm | Generous |
| Fanart.tv | Tier-based |
| OpenAI | Token-based |

Spotter automatically handles rate limiting with exponential backoff.
