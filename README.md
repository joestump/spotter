# Spotter

AI-powered music discovery and playlist sync for [Navidrome](https://www.navidrome.org/). Syncs your listening history, enriches metadata, generates AI mixtapes, and discovers similar artists — all self-hosted.

**[Documentation →](https://joestump.github.io/spotter/)**

---

## Features

- **Playlist Sync** — Sync Spotify and Last.fm playlists to your Navidrome library with fuzzy track matching
- **Vibes Engine** — AI-powered mixtape generation with customizable DJ personas
- **Similar Artists** — Discover related artists within your own library using AI
- **Metadata Enrichment** — Pull artist/album/track data from MusicBrainz, Spotify, Last.fm, Fanart.tv, and Lidarr
- **Real-time Updates** — SSE push notifications, no polling required
- **Navidrome Auth** — Log in with your existing Navidrome credentials

## Quick Start

### Docker (recommended)

```bash
docker run -p 8080:8080 \
  -e SPOTTER_NAVIDROME_BASE_URL=https://your-navidrome-instance \
  -e SPOTTER_OPENAI_API_KEY=sk-... \
  -v ./data:/app/data \
  ghcr.io/joestump/spotter:latest
```

### From Source

```bash
git clone https://github.com/joestump/spotter.git
cd spotter
make deps && make run
```

## Configuration

Spotter is configured via environment variables. The only required ones to get started:

| Variable | Description |
| :--- | :--- |
| `SPOTTER_NAVIDROME_BASE_URL` | URL of your Navidrome instance |
| `SPOTTER_OPENAI_API_KEY` | OpenAI (or LiteLLM-compatible) API key |

### Database

Spotter supports multiple database backends. SQLite is the default and requires no additional setup.

| Driver | `SPOTTER_DATABASE_DRIVER` | `SPOTTER_DATABASE_SOURCE` example |
| :--- | :--- | :--- |
| SQLite (default) | `sqlite3` | `file:./data/spotter.db?_fk=1` |
| PostgreSQL | `postgres` | `host=localhost port=5432 dbname=spotter user=spotter password=secret sslmode=disable` |
| MariaDB / MySQL | `mysql` | `user:pass@tcp(localhost:3306)/spotter?parseTime=true&charset=utf8mb4` |

> **Note:** PostgreSQL and MySQL drivers require CGO to be enabled at build time. The official Docker image includes all three drivers.

### Docker Compose Examples

Ready-to-use Compose files are provided for PostgreSQL and MariaDB:

```bash
# PostgreSQL
docker compose -f docker-compose.postgres.yml up

# MariaDB
docker compose -f docker-compose.mariadb.yml up
```

For the full configuration reference, see the **[documentation site](https://joestump.github.io/spotter/)**.

## Upgrading

A few upgrades need one step run by hand. Those steps are `spotter-admin` commands, and they read the same
`SPOTTER_DATABASE_DRIVER` / `SPOTTER_DATABASE_SOURCE` configuration as the server (pass `--db=<dsn>` to point at
another database). Build the tool with `make build-admin`, then run `./spotter-admin --help` to list the commands.

### Unified tag taxonomy backfill

Tags used to live in JSON array fields on artists, albums, and tracks. The typed `Tag` entity replaced them, and a
database upgraded from before that change still has its tags only in the legacy fields until the backfill runs:

```bash
./spotter-admin backfill-tags
```

Run it once after upgrading. It applies the same schema migration the server runs at startup, so it works whether or
not the new version has started yet, and it is safe to run again: already-migrated rows are skipped, and a second run
exits 0 reporting `0 entities migrated, N already up to date`.

## Development

```bash
make test       # run tests
make css        # rebuild Tailwind CSS
make run        # run with hot reload
```

See the [architecture docs](https://joestump.github.io/spotter/decisions) for the full list of ADRs and the [spec docs](https://joestump.github.io/spotter/specs) for detailed specifications.

## AI Disclosure

Spotter was written almost entirely by [Claude Code](https://claude.ai/claude-code) (Anthropic's AI assistant). The human provided product direction and reviewed the output. This project is shared openly as an experiment in AI-assisted software development.

**Use at your own risk. No warranty is provided, express or implied.**

## License

MIT
