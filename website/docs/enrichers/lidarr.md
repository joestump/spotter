---
sidebar_position: 6
---

# Lidarr Enricher

Lidarr is a music collection manager that provides organization and metadata for your music library.

## Features

- **Artist monitoring**: Track new releases
- **Quality profiles**: Manage audio quality
- **Release tracking**: Automatic downloads
- **Metadata**: Artist and album information

## Setup

### Get API Key

1. Open Lidarr
2. Go to **Settings** > **General**
3. Copy the **API Key** from the Security section

### Configure

```bash
SPOTTER_LIDARR_BASE_URL=http://localhost:8686
SPOTTER_LIDARR_API_KEY=your_lidarr_api_key
```

## Enriched Data

### Artists

- Lidarr ID
- Monitoring status
- Quality profile
- Path on disk
- Metadata profile

### Albums

- Lidarr ID
- Release date
- Album type
- Quality profile
- Monitored status

## Integration Benefits

When Lidarr is configured:

1. **Existing Metadata**: Leverage Lidarr's existing database
2. **Path Information**: Know where files are stored
3. **Monitoring Status**: See what's being tracked
4. **Quality Info**: Understand audio quality settings

## Configuration

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_LIDARR_BASE_URL` | Lidarr instance URL | *Required* |
| `SPOTTER_LIDARR_API_KEY` | Lidarr API key | *Required* |

## Network Configuration

### Docker

If Lidarr is on the same Docker network:

```bash
SPOTTER_LIDARR_BASE_URL=http://lidarr:8686
```

If on the host:

```bash
# Docker Desktop
SPOTTER_LIDARR_BASE_URL=http://host.docker.internal:8686

# Linux
SPOTTER_LIDARR_BASE_URL=http://172.17.0.1:8686
```

## Troubleshooting

### "Connection refused"

1. Verify Lidarr is running
2. Check the URL is correct
3. Ensure network connectivity

### "Unauthorized"

1. Verify the API key
2. Check API access is enabled in Lidarr
3. Try regenerating the API key

### Missing Data

Not all artists/albums may be in Lidarr:
1. They may not be monitored
2. Try adding them to Lidarr first
3. Spotter will sync on next enrichment run
