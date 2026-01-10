---
sidebar_position: 1
---

# Installation

This guide will walk you through setting up Spotter on your local machine.

## Prerequisites

Before installing Spotter, ensure you have the following:

- **Go 1.23+** - [Download Go](https://go.dev/dl/)
- **Node.js & npm** - For Tailwind CSS generation ([Download Node.js](https://nodejs.org/))
- **Make** - Build automation tool (usually pre-installed on macOS/Linux)

## Installation Steps

### 1. Clone the Repository

```bash
git clone https://github.com/joestump/spotter.git
cd spotter
```

### 2. Install Dependencies

```bash
make deps
```

This command will:
- Download Go dependencies
- Install development tools (templ, air, golangci-lint)
- Install Node.js dependencies

### 3. Configure Environment

Copy the example environment file and configure it with your settings:

```bash
cp .env.example .env
```

Edit `.env` with your Navidrome URL and API keys. See the [Configuration](/docs/getting-started/configuration) guide for detailed instructions.

### 4. Build and Run

```bash
make run
```

The server will start at `http://localhost:8080`.

## Development Mode

For development with hot-reload:

```bash
make dev
```

This starts:
- **Air**: Go hot-reload on `http://localhost:8080`
- **Templ**: Template watching with proxy on `http://localhost:7331`
- **Tailwind**: CSS watching and rebuilding

## Verify Installation

1. Open `http://localhost:8080` in your browser
2. Log in using your Navidrome credentials
3. You should see the Spotter dashboard

## Troubleshooting

### Port Already in Use

If port 8080 is already in use, you can change it in your `.env` file:

```bash
SPOTTER_SERVER_PORT=3000
```

### Missing Dependencies

If you encounter errors about missing tools, ensure all prerequisites are installed:

```bash
# Verify Go installation
go version

# Verify Node.js installation
node --version
npm --version

# Verify Make installation
make --version
```

### Database Issues

Spotter uses SQLite by default. If you encounter database errors, try removing the database file and restarting:

```bash
rm spotter.db
make run
```

## Next Steps

- [Configure Spotter](/docs/getting-started/configuration)
- [Set up Docker deployment](/docs/getting-started/docker)
- [Connect external services](/docs/providers/spotify)
