---
sidebar_position: 1
slug: /
---

# Introduction

**Spotter** is an AI-powered playlist generator for [Navidrome](https://www.navidrome.org/). It aggregates your listening history from various sources (Navidrome, Spotify, Last.fm) and uses that data to generate personalized playlists. AI-powered metadata enrichment generates intelligent summaries, biographies, and tags for your music library.

## Key Features

- **Unified Listening History**: Syncs recent listens from Navidrome, Spotify, and Last.fm into a single view with pagination
- **Playlist Management**: View and sync playlists from all connected services
- **Vibes Engine**: AI-powered mixtape generation with customizable DJ personas that curate playlists based on your listening history
- **Navidrome Integration**: Log in using your existing Navidrome credentials
- **External Service Support**: Connect your Spotify and Last.fm accounts to import history and improve recommendations
- **Metadata Enrichment**: Automatically enriches artist, album, and track metadata from MusicBrainz, Fanart.tv, Spotify, Last.fm, and more
- **AI-Powered Enrichment**: Optional OpenAI integration generates intelligent summaries, biographies, and tags for artists, albums, and tracks
- **Real-time Updates**: Server-Sent Events (SSE) push new listens and sync notifications to the UI automatically
- **Retro-Themed UI**: Custom-designed themes featuring a warm 1970s music cabinet aesthetic (light mode) and an 1980s cyberpunk vibe (dark mode)

## Architecture Overview

Spotter is built with:

- **Backend**: Go with `chi` router and Server-Sent Events (SSE) for real-time updates
- **Database**: SQLite (via `ent` ORM) with automatic migrations
- **Frontend**: Server-side rendering with `templ` + `HTMX` for interactivity and real-time updates
- **Styling**: `DaisyUI` + `Tailwind CSS` with custom retro themes and `@iconify/tailwind` for icons
- **Background Jobs**: Configurable periodic sync for all connected providers
- **Real-time**: Event Bus + SSE for push notifications and live updates
- **Metadata Enrichment**: Pluggable enricher system that aggregates data from multiple sources

## Quick Links

- [Installation Guide](/docs/getting-started/installation)
- [Configuration Reference](/docs/getting-started/configuration)
- [Vibes Engine Documentation](/docs/features/vibes-engine)
- [API Reference](/docs/api/endpoints)
