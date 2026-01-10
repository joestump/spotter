---
sidebar_position: 5
---

# Fanart.tv Enricher

Fanart.tv provides high-quality artist and album artwork including backgrounds, logos, and HD images.

## Features

- **HD Artist Images**: High-resolution artist photos
- **Artist Backgrounds**: Wide-format background images
- **Artist Logos**: Transparent logo images
- **Album Art**: High-quality cover art

## Setup

### Get API Key

1. Go to [fanart.tv](https://fanart.tv/)
2. Create an account or log in
3. Visit your [API page](https://fanart.tv/get-an-api-key/)
4. Copy your **Personal API Key**

### Configure

```bash
SPOTTER_METADATA_FANART_API_KEY=your_api_key_here
```

## Enriched Data

### Artists

- **Artist Thumb**: Square profile image
- **Artist Background**: Wide landscape image
- **HD Logo**: Transparent logo
- **Music Logo**: Text-based logo
- **Banner**: Wide banner image

### Albums

- **Album Cover**: Front cover art
- **CD Art**: Disc artwork
- **Album Back**: Back cover (if available)

## Image Types

| Type | Dimensions | Usage |
| :--- | :--- | :--- |
| artistthumb | 1000x1000 | Profile pictures |
| artistbackground | 1920x1080 | Page backgrounds |
| hdmusiclogo | ~800x310 | Headers, overlays |
| albumcover | 1000x1000 | Album artwork |
| cdart | 1000x1000 | Disc visualization |

## Rate Limiting

Fanart.tv limits depend on your account tier:

| Tier | Requests |
| :--- | :--- |
| Free | Limited |
| VIP | Higher limits |
| Project | Unlimited |

For personal use, the free tier is typically sufficient.

## MusicBrainz Dependency

Fanart.tv uses MusicBrainz IDs (MBIDs) for lookups:

1. Artist/album must have an MBID
2. MusicBrainz enricher should run first
3. Entities without MBIDs won't get Fanart.tv images

## Troubleshooting

### "Invalid API key"

1. Verify your API key
2. Check it's not expired
3. Try generating a new key

### No Images Found

- The artist/album may not have community submissions
- Fanart.tv is community-contributed
- Consider contributing artwork

### MBIDs Required

If images aren't fetching:
1. Check that MusicBrainz enricher is enabled
2. Verify the entity has an MBID
3. Wait for MusicBrainz to process first

## Contributing to Fanart.tv

Fanart.tv relies on community contributions:

1. Create an account
2. Submit high-quality artwork
3. Follow their submission guidelines
4. Help improve coverage for your favorite artists
