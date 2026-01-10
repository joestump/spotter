---
sidebar_position: 1
---

# Architecture

Spotter is built with a modern Go stack emphasizing simplicity and performance.

## Technology Stack

### Backend

- **Language**: Go 1.23+
- **Router**: [chi](https://github.com/go-chi/chi) - Lightweight HTTP router
- **ORM**: [ent](https://entgo.io/) - Entity framework for Go
- **Database**: SQLite (default), PostgreSQL, MySQL supported

### Frontend

- **Templating**: [templ](https://templ.guide/) - Type-safe Go templates
- **Interactivity**: [HTMX](https://htmx.org/) - HTML over the wire
- **Styling**: [Tailwind CSS](https://tailwindcss.com/) + [DaisyUI](https://daisyui.com/)
- **Icons**: [@iconify/tailwind](https://iconify.design/)

### Real-time

- **Event Bus**: Internal pub/sub system
- **SSE**: Server-Sent Events for push notifications

## Directory Structure

```
spotter/
├── cmd/
│   └── server/
│       └── main.go          # Application entry point
├── internal/
│   ├── config/              # Configuration management
│   ├── handlers/            # HTTP handlers
│   ├── providers/           # External service providers
│   │   ├── navidrome/
│   │   ├── spotify/
│   │   └── lastfm/
│   ├── enrichers/           # Metadata enrichers
│   │   ├── musicbrainz/
│   │   ├── spotify/
│   │   ├── lastfm/
│   │   ├── fanart/
│   │   ├── lidarr/
│   │   └── openai/
│   ├── services/            # Business logic
│   ├── templates/           # templ templates
│   └── middleware/          # HTTP middleware
├── ent/
│   └── schema/              # Database schemas
├── static/
│   └── css/                 # Tailwind CSS
├── data/
│   ├── prompts/             # AI prompt templates
│   └── images/              # Downloaded images
└── website/                 # Docusaurus documentation
```

## Request Flow

```
Browser → HTTP Handler → Service → Repository → Database
                ↓
            Template → HTMX Response
                ↓
            SSE Event (if applicable)
```

## Key Components

### Providers

Providers sync data from external services:

```go
type Provider interface {
    Name() string
    SyncListens(ctx context.Context, user *ent.User) error
    SyncPlaylists(ctx context.Context, user *ent.User) error
}
```

### Enrichers

Enrichers enhance metadata from external sources:

```go
type Enricher interface {
    Name() string
    EnrichArtist(ctx context.Context, artist *ent.Artist) error
    EnrichAlbum(ctx context.Context, album *ent.Album) error
    EnrichTrack(ctx context.Context, track *ent.Track) error
}
```

### Event Bus

The event bus enables real-time updates:

```go
// Publish an event
eventBus.Publish("listen:new", listenData)

// Subscribe to events
eventBus.Subscribe("listen:new", func(data interface{}) {
    // Handle event
})
```

## Database Schema

Key entities:

- **User**: Application users (linked to Navidrome)
- **Artist**: Music artists with metadata
- **Album**: Albums with artwork and tracks
- **Track**: Individual songs with audio features
- **Listen**: Listening history entries
- **Playlist**: Playlists from various sources
- **DJ**: Vibes Engine DJ personas
- **Mixtape**: Generated mixtapes

## Background Jobs

Spotter runs several background processes:

1. **Sync Service**: Periodically syncs from providers
2. **Enrichment Service**: Runs metadata enrichment
3. **Playlist Sync**: Keeps Navidrome playlists updated

## Configuration

Configuration is loaded from environment variables using [Viper](https://github.com/spf13/viper):

```go
type Config struct {
    Server   ServerConfig
    Database DatabaseConfig
    Sync     SyncConfig
    Metadata MetadataConfig
    // ...
}
```

## Error Handling

Errors are wrapped with context:

```go
if err != nil {
    return fmt.Errorf("failed to sync listens: %w", err)
}
```

## Logging

Structured logging with [slog](https://pkg.go.dev/log/slog):

```go
slog.Info("syncing listens",
    "provider", provider.Name(),
    "user_id", user.ID,
)
```
