---
sidebar_position: 3
---

# Docker Deployment

Spotter can be run in a Docker container for easy deployment.

## Quick Start

### Build the Docker Image

```bash
make docker-build
```

This builds a Docker image tagged as `spotter`.

### Run with Docker

```bash
docker run -p 8080:8080 --env-file .env spotter
```

Or specify environment variables directly:

```bash
docker run -p 8080:8080 \
  -e SPOTTER_NAVIDROME_BASE_URL=https://music.example.com \
  -e SPOTTER_OPENAI_API_KEY=sk-your-api-key \
  spotter
```

## Docker Compose

For a more complete setup, use Docker Compose:

```yaml
version: '3.8'

services:
  spotter:
    build: .
    ports:
      - "8080:8080"
    environment:
      - SPOTTER_NAVIDROME_BASE_URL=https://music.example.com
      - SPOTTER_OPENAI_API_KEY=${SPOTTER_OPENAI_API_KEY}
      - SPOTTER_SPOTIFY_CLIENT_ID=${SPOTTER_SPOTIFY_CLIENT_ID}
      - SPOTTER_SPOTIFY_CLIENT_SECRET=${SPOTTER_SPOTIFY_CLIENT_SECRET}
      - SPOTTER_LASTFM_API_KEY=${SPOTTER_LASTFM_API_KEY}
      - SPOTTER_LASTFM_SHARED_SECRET=${SPOTTER_LASTFM_SHARED_SECRET}
    volumes:
      - spotter-data:/app/data
      - spotter-db:/app/spotter.db
    restart: unless-stopped

volumes:
  spotter-data:
  spotter-db:
```

## Persistent Data

To persist data between container restarts, mount volumes for:

- **Database**: `/app/spotter.db` (SQLite database)
- **Images**: `/app/data/images` (Downloaded artwork)

```bash
docker run -p 8080:8080 \
  -v $(pwd)/data:/app/data \
  -v $(pwd)/spotter.db:/app/spotter.db \
  --env-file .env \
  spotter
```

## Environment Variables

When running in Docker, ensure all required environment variables are set. See the [Configuration](/docs/getting-started/configuration) guide for the complete list.

### Required Variables

| Variable | Description |
| :--- | :--- |
| `SPOTTER_NAVIDROME_BASE_URL` | URL of your Navidrome instance |
| `SPOTTER_OPENAI_API_KEY` | OpenAI API key for AI features |

### Network Configuration

If Spotter needs to connect to services on the host machine (like a local Navidrome instance), use the special Docker DNS name:

```bash
# On Docker Desktop (macOS/Windows)
SPOTTER_NAVIDROME_BASE_URL=http://host.docker.internal:4533

# On Linux
docker run --add-host=host.docker.internal:host-gateway ...
```

## Health Checks

The container includes a basic health check. You can verify the container is running:

```bash
docker ps
curl http://localhost:8080/health
```

## Troubleshooting

### Container Won't Start

Check the logs:

```bash
docker logs <container_id>
```

### Permission Issues

If you encounter permission issues with mounted volumes:

```bash
# Fix ownership on Linux
sudo chown -R 1000:1000 ./data ./spotter.db
```

### Database Locked Errors

SQLite doesn't handle concurrent access well. Ensure only one container instance is running:

```bash
docker ps -a | grep spotter
docker stop <old_container_id>
```
