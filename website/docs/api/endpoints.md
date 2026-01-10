---
sidebar_position: 1
---

# API Endpoints

Spotter provides a RESTful API for programmatic access to its features.

## Base URL

```
http://localhost:8080
```

## Authentication

Most endpoints require authentication via session cookie. Log in through the web interface first.

## Endpoints

### Listening History

#### Get Recent Listens

```http
GET /api/listens
```

Query parameters:
- `page` (optional): Page number (default: 1)
- `limit` (optional): Items per page (default: 20)

Response:
```json
{
  "listens": [
    {
      "id": 1,
      "track_name": "Song Title",
      "artist_name": "Artist Name",
      "album_name": "Album Name",
      "listened_at": "2024-01-15T10:30:00Z",
      "source": "navidrome"
    }
  ],
  "total": 100,
  "page": 1,
  "limit": 20
}
```

### Playlists

#### List Playlists

```http
GET /api/playlists
```

#### Get Playlist

```http
GET /api/playlists/{id}
```

#### Sync Playlist to Navidrome

```http
POST /playlists/{id}/toggle-sync
```

#### Trigger Immediate Sync

```http
POST /playlists/{id}/sync
```

#### Get Sync Status

```http
GET /playlists/{id}/sync-status
```

Response:
```json
{
  "sync_enabled": true,
  "navidrome_id": "abc123",
  "matched_tracks": 25,
  "total_tracks": 30,
  "last_synced": "2024-01-15T10:30:00Z",
  "sync_error": null
}
```

### Artists

#### List Artists

```http
GET /api/artists
```

#### Get Artist

```http
GET /api/artists/{id}
```

#### Get Similar Artists

```http
GET /api/artists/{id}/similar
```

### Albums

#### List Albums

```http
GET /api/albums
```

#### Get Album

```http
GET /api/albums/{id}
```

### Vibes Engine

#### List DJs

```http
GET /api/vibes/djs
```

#### Create DJ

```http
POST /api/vibes/djs
```

Request body:
```json
{
  "name": "DJ Name",
  "personality": "A late-night radio host...",
  "genre_preferences": ["jazz", "soul"],
  "excluded_artists": [],
  "included_artists": []
}
```

#### List Mixtapes

```http
GET /api/vibes/mixtapes
```

#### Create Mixtape

```http
POST /api/vibes/mixtapes
```

#### Generate Mixtape

```http
POST /api/vibes/mixtapes/{id}/generate
```

### Tasks

#### Trigger Sync

```http
POST /api/tasks/sync
```

#### Trigger Enrichment

```http
POST /api/tasks/enrich
```

### Server-Sent Events

#### Subscribe to Events

```http
GET /api/events
```

Event types:
- `listen:new` - New listen recorded
- `sync:started` - Sync operation started
- `sync:completed` - Sync operation completed
- `playlist:updated` - Playlist was updated

## Error Responses

Errors follow a consistent format:

```json
{
  "error": "Error message",
  "code": "ERROR_CODE",
  "details": {}
}
```

Common error codes:
- `UNAUTHORIZED` - Not logged in
- `NOT_FOUND` - Resource not found
- `VALIDATION_ERROR` - Invalid request data
- `RATE_LIMITED` - Too many requests
