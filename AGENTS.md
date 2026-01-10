# Spotter Development Guide

## Project Overview

**Spotter** is an AI-powered playlist generator and metadata enrichment system for Navidrome. It aggregates listening history from multiple sources (Navidrome, Spotify, Last.fm), enriches music metadata using AI and external services, and generates intelligent playlists through customizable DJ personas.

### Key Features
- Unified listening history across multiple music services
- AI-powered mixtape generation with customizable DJ personas
- Automatic metadata enrichment from MusicBrainz, Spotify, Last.fm, Fanart.tv, and OpenAI
- Playlist synchronization between services with intelligent track matching
- Real-time updates via Server-Sent Events (SSE)
- Retro-themed UI (1970s music cabinet light theme, 1980s cyberpunk dark theme)
- Pluggable provider and enricher architecture

### User Stories

**Authentication & Onboarding**
- As a user, I want to log in using my existing Navidrome credentials so I don't need another account
- As a user, I want to connect my Spotify account to import my listening history and playlists
- As a user, I want to connect my Last.fm account to sync my scrobble history
- As a user, I want to disconnect external services when I no longer want to share data

**Listening History**
- As a user, I want to see my recent listening history from all connected services in one unified view
- As a user, I want to paginate through my listening history to explore older listens
- As a user, I want to see real-time updates when new tracks are played without refreshing the page
- As a user, I want to see which service each listen came from (Navidrome, Spotify, Last.fm)

**Playlist Management**
- As a user, I want to view all my playlists from Navidrome, Spotify, and Last.fm in one place
- As a user, I want to sync playlists from Spotify or Last.fm to my Navidrome library
- As a user, I want to see sync status (pending, success, warning, error) for each playlist
- As a user, I want to see how many tracks were successfully matched during sync
- As a user, I want to manually trigger playlist sync when I make changes
- As a user, I want to rebuild a playlist sync from scratch if something goes wrong
- As a user, I want to disable sync for a playlist and optionally remove it from Navidrome
- As a user, I want to see detailed match statistics (total tracks, matched tracks, percentage)
- As a user, I want automatic periodic sync so my playlists stay up-to-date

**AI-Powered Mixtapes (Vibes Engine)**
- As a user, I want to create DJ personas with unique personalities and music preferences
- As a user, I want to generate mixtapes based on a DJ's personality and my listening history
- As a user, I want to seed mixtapes with specific artists, albums, or tracks
- As a user, I want to schedule mixtapes to regenerate daily, weekly, or monthly
- As a user, I want to see why the AI selected each track for my mixtape
- As a user, I want to sync generated mixtapes to Navidrome as playable playlists
- As a user, I want to enhance existing playlists with AI-suggested tracks that complement the vibe
- As a user, I want to reorder playlist tracks using AI for better flow and energy progression
- As a user, I want to create mixtapes inspired by specific artists from their detail pages

**Metadata Enrichment**
- As a user, I want artist biographies and tags automatically added to my library
- As a user, I want album summaries and metadata enriched from multiple sources
- As a user, I want high-quality artist and album images downloaded to my library
- As a user, I want AI-generated summaries and tags clearly marked in the UI
- As a user, I want to manually trigger enrichment for specific artists or albums
- As a user, I want periodic background enrichment so my library stays current

**Artist Discovery**
- As a user, I want to find similar artists within my own library using AI analysis
- As a user, I want to see confidence scores for artist similarities
- As a user, I want to see explanations for why artists are considered similar
- As a user, I want to refresh similar artist recommendations to discover new connections
- As a user, I want to create mixtapes inspired by artists I discover

**Preferences & Customization**
- As a user, I want to choose between light, dark, or system-based themes
- As a user, I want to customize the AI personality for playlist generation
- As a user, I want to configure pagination (items per page) for listings
- As a user, I want to see when my connected services last synced
- As a user, I want to manually trigger background tasks (sync, enrichment, cleanup)

**User Experience**
- As a user, I want toast notifications for important events (sync started, completed, failed)
- As a user, I want progress indicators during long-running operations
- As a user, I want timeago timestamps that automatically update (e.g., "5 minutes ago")
- As a user, I want keyboard navigation and accessibility features
- As a user, I want responsive design that works on mobile and desktop

### RFC 2119 Requirements

#### Configuration (CFG)
- **CFG-001**: The system MUST validate that NavidromeBaseURL is provided in configuration
- **CFG-002**: The system MUST validate that OpenAI API key is provided when AI features are enabled
- **CFG-003**: The system MUST validate that Lidarr base URL is provided when Lidarr integration is enabled
- **CFG-004**: The system MUST validate that Lidarr API key is provided when Lidarr integration is enabled
- **CFG-005**: The system MUST validate that required enrichers are enabled
- **CFG-006**: The system SHOULD set default theme to "dark" when not specified
- **CFG-007**: The system MUST support environment variable overrides for all configuration values
- **CFG-008**: The system MUST load configuration from YAML files when present

#### Authentication & Authorization (AUTH)
- **AUTH-001**: The system MUST authenticate users via Navidrome Subsonic API
- **AUTH-002**: The system MUST reject login attempts with invalid credentials
- **AUTH-003**: The system MUST create or retrieve user records after successful authentication
- **AUTH-004**: The system MUST create NavidromeAuth edges linking users to their Navidrome credentials
- **AUTH-005**: The system MUST set secure HTTP-only session cookies upon successful authentication
- **AUTH-006**: The system MUST redirect authenticated users to the home page
- **AUTH-007**: The system MUST validate that username and password are provided in login requests
- **AUTH-008**: The system MUST NOT expose Navidrome credentials in responses
- **AUTH-009**: The system MUST use Subsonic authentication protocol for Navidrome API calls
- **AUTH-010**: The system MUST generate salted MD5 tokens for Subsonic API authentication
- **AUTH-011**: The system MUST include client and version parameters in Subsonic API requests

#### Provider Integration (PROV)
- **PROV-SP-001**: The system MUST refresh expired OAuth2 tokens automatically
- **PROV-SP-002**: The system MUST check token expiry before making API requests
- **PROV-SP-003**: The system MUST calculate playlist statistics including unique artists and albums
- **PROV-SP-004**: The system MUST handle token refresh failures gracefully
- **PROV-SP-005**: The system SHOULD cache token refresh results to avoid redundant API calls
- **PROV-ND-001**: The system MUST support fetching playlists from Navidrome
- **PROV-ND-002**: The system MUST support creating new playlists in Navidrome
- **PROV-ND-003**: The system MUST support updating existing playlists in Navidrome
- **PROV-ND-004**: The system MUST support deleting playlists from Navidrome
- **PROV-ND-005**: The system MUST fetch recent listens with configurable time ranges
- **PROV-ND-006**: The system MUST filter recent listens by timestamp
- **PROV-ND-007**: The system SHOULD handle pagination for large result sets
- **PROV-LF-001**: The system MUST authenticate with Last.fm using API keys
- **PROV-LF-002**: The system MUST fetch artist information including biography and tags
- **PROV-LF-003**: The system MUST fetch album information and release metadata
- **PROV-LF-004**: The system MUST strip HTML tags from biography text
- **PROV-LF-005**: The system MUST parse MusicBrainz IDs from Last.fm responses
- **PROV-LF-006**: The system SHOULD handle missing or incomplete metadata gracefully

#### Enrichers (ENR)
- **ENR-FA-001**: The system MUST require API key for Fanart.tv integration
- **ENR-FA-002**: The system MUST fetch artist images by MusicBrainz ID
- **ENR-FA-003**: The system MUST fetch album images by MusicBrainz ID
- **ENR-FA-004**: The system MUST prioritize image types (thumbnail, cover, banner)
- **ENR-FA-005**: The system SHOULD cache Fanart.tv API responses to minimize requests
- **ENR-LF-001**: The system MUST enrich artists with Last.fm biography data
- **ENR-LF-002**: The system MUST enrich albums with Last.fm metadata
- **ENR-LF-003**: The system MUST enrich tracks with Last.fm metadata
- **ENR-LF-004**: The system MUST strip HTML from biography content before storage
- **ENR-LF-005**: The system MUST parse and store tags from Last.fm
- **ENR-LF-006**: The system MUST extract MusicBrainz IDs when available
- **ENR-LF-007**: The system SHOULD deduplicate tags across multiple sources
- **ENR-MB-001**: The system MUST respect MusicBrainz API rate limits (1 request per second)
- **ENR-MB-002**: The system MUST search for artist MusicBrainz IDs by name
- **ENR-MB-003**: The system MUST search for album MusicBrainz IDs by title and artist
- **ENR-MB-004**: The system MUST search for track MusicBrainz IDs by title, artist, and album
- **ENR-MB-005**: The system MUST parse release year from MusicBrainz date fields
- **ENR-MB-006**: The system MUST handle fuzzy matching for artist/album/track names
- **ENR-MB-007**: The system SHOULD cache MusicBrainz ID lookups to reduce API calls
- **ENR-AI-001**: The system MUST use OpenAI API for AI-powered metadata enrichment
- **ENR-AI-002**: The system MUST parse JSON responses from OpenAI completions
- **ENR-AI-003**: The system MUST deduplicate tags from AI enrichment
- **ENR-AI-004**: The system MUST skip AI enrichment for recently enriched entities (within 7 days)
- **ENR-AI-005**: The system MUST sanitize AI-generated JSON to handle trailing commas
- **ENR-AI-006**: The system SHOULD handle AI API failures gracefully without blocking other operations
- **ENR-AI-007**: The system MUST provide entity context (artist/album/track data) to AI prompts

#### Handlers (HAND)
- **HAND-AL-001**: The system MUST display album details including tracks
- **HAND-AL-002**: The system MUST enforce user isolation (users can only view their own albums)
- **HAND-AL-003**: The system MUST trigger manual enrichment when requested
- **HAND-AL-004**: The system SHOULD display enrichment status and timestamps
- **HAND-PL-001**: The system MUST allow users to toggle playlist sync on/off
- **HAND-PL-002**: The system MUST enforce user isolation (users can only access their own playlists)
- **HAND-PL-003**: The system MUST display sync status with appropriate badges (Success, Warning, Error, Pending, Neutral)
- **HAND-PL-004**: The system MUST show WARNING status when zero tracks are matched (not SUCCESS)
- **HAND-PL-005**: The system MUST calculate and display match statistics (total tracks, matched tracks, match percentage)
- **HAND-PL-006**: The system SHOULD refresh playlist data after sync operations
- **HAND-VB-001**: The system MUST generate AI-powered mixtapes based on user prompts
- **HAND-VB-002**: The system MUST validate that prompts are provided for mixtape generation
- **HAND-VB-003**: The system MUST match AI-generated tracks to user's Navidrome library
- **HAND-VB-004**: The system MUST use fuzzy matching with confidence thresholds for track matching
- **HAND-VB-005**: The system SHOULD display unmatched tracks to the user
- **HAND-VB-006**: The system MUST allow users to create playlists from mixtapes

#### Services (SRV)
- **SRV-AI-001**: The system MUST enrich artists that have never been AI-enriched (last_ai_enriched_at IS NULL)
- **SRV-AI-002**: The system MUST re-enrich artists whose AI enrichment is older than 7 days
- **SRV-AI-003**: The system MUST track separate timestamps for AI enrichment vs general enrichment
- **SRV-AI-004**: The system MUST query for artists needing AI enrichment independently of general enrichment status
- **SRV-AI-005**: The system MUST update last_ai_enriched_at timestamp after successful AI enrichment
- **SRV-AI-006**: The system SHOULD prioritize artists with no AI enrichment over stale enrichment
- **SRV-PS-001**: The system MUST sync playlists from external providers to Navidrome
- **SRV-PS-002**: The system MUST match external tracks to Navidrome library tracks
- **SRV-PS-003**: The system MUST use fuzzy matching with configurable confidence thresholds
- **SRV-PS-004**: The system MUST track sync status (success, warning, error, pending)
- **SRV-PS-005**: The system MUST record match statistics (total tracks, matched tracks)
- **SRV-PS-006**: The system MUST respect delete configuration (delete orphaned tracks or preserve them)
- **SRV-PS-007**: The system MUST update playlist metadata in database after sync
- **SRV-PS-008**: The system SHOULD handle partial sync failures gracefully
- **SRV-PS-009**: The system MUST NOT mark syncs as successful when zero tracks are matched
- **SRV-TM-001**: The system MUST perform fuzzy matching between external and library tracks
- **SRV-TM-002**: The system MUST normalize track titles by removing "(Remastered)", "(Live)", and punctuation
- **SRV-TM-003**: The system MUST normalize artist names for comparison
- **SRV-TM-004**: The system MUST normalize album titles for comparison
- **SRV-TM-005**: The system MUST calculate match confidence scores (0-100)
- **SRV-TM-006**: The system MUST enforce minimum confidence threshold (default 80)
- **SRV-TM-007**: The system MUST match on artist + title as primary criteria
- **SRV-TM-008**: The system SHOULD boost confidence when album names also match
- **SRV-TM-009**: The system SHOULD use Levenshtein distance for fuzzy string matching
- **SRV-TM-010**: The system MUST return the highest confidence match when multiple candidates exist
- **SRV-SA-001**: The system MUST fetch similar artists from Last.fm
- **SRV-SA-002**: The system MUST store similar artist relationships in database
- **SRV-SA-003**: The system SHOULD deduplicate similar artist relationships
- **SRV-SA-004**: The system MUST handle cases where no similar artists are found
- **SRV-SY-001**: The system MUST sync artists from Navidrome to local database
- **SRV-SY-002**: The system MUST sync albums from Navidrome to local database
- **SRV-SY-003**: The system MUST sync tracks from Navidrome to local database
- **SRV-SY-004**: The system MUST create relationships between artists, albums, and tracks
- **SRV-SY-005**: The system MUST handle incremental updates (new artists/albums/tracks)
- **SRV-SY-006**: The system SHOULD batch database operations for performance
- **SRV-SY-007**: The system MUST preserve existing enrichment data during sync

#### Vibes (AI Mixtapes) (VIB)
- **VIB-GEN-001**: The system MUST generate mixtapes using OpenAI based on user prompts
- **VIB-GEN-002**: The system MUST parse AI response to extract track list with artist, title, and album
- **VIB-GEN-003**: The system MUST match AI-generated tracks to user's Navidrome library
- **VIB-GEN-004**: The system MUST use fuzzy matching with Levenshtein distance for track matching
- **VIB-GEN-005**: The system MUST calculate similarity scores for track matching
- **VIB-GEN-006**: The system MUST track which tracks were successfully matched vs unmatched
- **VIB-GEN-007**: The system SHOULD provide reasonable defaults when AI doesn't specify album names
- **VIB-ENH-001**: The system MUST enhance existing playlists with AI suggestions
- **VIB-ENH-002**: The system MUST analyze playlist characteristics (genres, moods, artists)
- **VIB-ENH-003**: The system MUST generate contextual AI prompts based on playlist content
- **VIB-ENH-004**: The system MUST match AI suggestions to user's library
- **VIB-ENH-005**: The system SHOULD preserve original playlist tracks while adding enhancements
- **VIB-PAR-001**: The system MUST parse JSON responses from AI mixtape generation
- **VIB-PAR-002**: The system MUST sanitize AI-generated JSON to fix trailing commas
- **VIB-PAR-003**: The system MUST handle malformed JSON from AI responses gracefully
- **VIB-PAR-004**: The system MUST extract track information (artist, title, album, reason) from AI JSON
- **VIB-PAR-005**: The system MUST validate that required fields (artist, title) are present
- **VIB-PAR-006**: The system SHOULD parse optional fields (album, reason) when available

#### View Components (VIEW)
- **VIEW-SS-001**: The system MUST display SUCCESS status when all tracks are matched (100%)
- **VIEW-SS-002**: The system MUST display WARNING status when some tracks are matched but not all
- **VIEW-SS-003**: The system MUST display WARNING status when zero tracks are matched (not SUCCESS)
- **VIEW-SS-004**: The system MUST display ERROR status when sync operations fail
- **VIEW-SS-005**: The system MUST display PENDING status when sync is in progress
- **VIEW-SS-006**: The system MUST display NEUTRAL status when no sync has been attempted
- **VIEW-SS-007**: The system MUST apply appropriate CSS classes for badge styling
- **VIEW-SS-008**: The system MUST calculate progress percentage for partial matches
- **VIEW-SS-009**: The system MUST display match statistics (X of Y tracks matched)
- **VIEW-TT-001**: The system MUST prioritize medium image URLs over small image URLs when available
- **VIEW-TT-002**: The system MUST fall back to small image URLs when medium images are not available
- **VIEW-TT-003**: The system MUST handle missing album images gracefully
- **VIEW-TT-004**: The system SHOULD display track duration in human-readable format

#### Data Validation (VAL)
- **VAL-001**: The system MUST validate that required fields are present before API calls
- **VAL-002**: The system MUST validate that API keys are configured before making external requests
- **VAL-003**: The system MUST validate user ownership before allowing data access
- **VAL-004**: The system MUST validate confidence thresholds are within valid ranges (0-100)
- **VAL-005**: The system MUST validate that timestamps are in correct format
- **VAL-006**: The system SHOULD validate string lengths to prevent buffer overflows
- **VAL-007**: The system MUST validate that MusicBrainz IDs are in correct UUID format
- **VAL-008**: The system MUST validate that playlist IDs exist before sync operations

#### Error Handling (ERR)
- **ERR-001**: The system MUST return appropriate HTTP status codes for errors (400, 401, 404, 500)
- **ERR-002**: The system MUST provide descriptive error messages for validation failures
- **ERR-003**: The system MUST handle network timeouts gracefully
- **ERR-004**: The system MUST handle external API failures without crashing
- **ERR-005**: The system MUST log errors with sufficient context for debugging
- **ERR-006**: The system MUST handle database connection failures gracefully
- **ERR-007**: The system SHOULD retry transient failures with exponential backoff
- **ERR-008**: The system MUST NOT expose sensitive information (API keys, passwords) in error messages
- **ERR-009**: The system MUST handle JSON parsing errors from external APIs
- **ERR-010**: The system MUST handle rate limit errors with appropriate delays

#### Integration (INT)
- **INT-001**: The system MUST support integration with Navidrome as the primary music server
- **INT-002**: The system MAY support integration with Spotify for playlist import
- **INT-003**: The system MAY support integration with Last.fm for metadata enrichment
- **INT-004**: The system MAY support integration with MusicBrainz for canonical IDs
- **INT-005**: The system MAY support integration with Fanart.tv for images
- **INT-006**: The system MAY support integration with Lidarr for music management
- **INT-007**: The system MUST support integration with OpenAI for AI features
- **INT-008**: The system MUST handle cases where optional integrations are disabled
- **INT-009**: The system MUST provide meaningful operation when enrichers fail
- **INT-010**: The system SHOULD allow per-user configuration of external integrations

## Issue Tracking (Beads)

This project uses **bd** (beads) for issue tracking.

### Workflow Checklist

**STOP. Follow this checklist for EVERY bead. No exceptions.**

```
┌─────────────────────────────────────────────────────────────────┐
│  BEADS WORKFLOW - MANDATORY STEPS                               │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  1. START                                                       │
│     [ ] bd ready                    # Find available work       │
│     [ ] bd show <id>                # Review details            │
│     [ ] bd update <id> --status in_progress                     │
│     [ ] git checkout main && git pull                           │
│     [ ] git checkout -b bead/<id>   # Create feature branch     │
│                                                                 │
│  2. WORK (repeat as needed)                                     │
│     [ ] Implement changes                                       │
│     [ ] make lint                   # BEFORE every commit       │
│     [ ] make test                   # BEFORE every commit       │
│     [ ] make run                    # Verify app starts         │
│     [ ] git add <files>                                         │
│     [ ] git commit -m "..."                                     │
│                                                                 │
│  3. COMPLETE (in this exact order)                              │
│     [ ] make lint                   # MUST pass                 │
│     [ ] make test                   # MUST pass                 │
│     [ ] make run                    # MUST start                │
│     [ ] make build                  # MUST succeed              │
│     [ ] bd close <id>               # Close issue               │
│     [ ] git push -u origin bead/<id>                            │
│     [ ] gh pr create --base main --head bead/<id>               │
│                                                                 │
│  RULES:                                                         │
│  • NEVER commit without running lint/test/run first             │
│  • NEVER close bead until quality gates pass                    │
│  • NEVER skip the PR - work isn't done until PR exists          │
│  • NEVER work directly on main                                  │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Quality Gates

**ALL must pass before `bd close` or `git commit`:**

| Gate | Command | Requirement |
|------|---------|-------------|
| Lint | `make lint` | Zero errors |
| Test | `make test` | All pass |
| Run | `make run` | App starts (Ctrl+C to stop) |
| Build | `make build` | Compiles successfully |
| Schema | `go generate ./ent` | If schema changed |

### Branch Naming

- Format: `bead/<bead-id>`
- Examples: `bead/spotter-vey`, `bead/spotter-ahw`
- Always branch from `main`

### Bead Requirements

**Every bead MUST have:**

| Requirement | Description |
|-------------|-------------|
| Separate Fields | Description, Acceptance Criteria, Notes (NOT all in description) |
| RFC 2119 Criteria | MUST/SHOULD/MAY statements defining "done" |
| 3-5 Labels | Including ONE category: `feature`/`research`/`toil`/`cleanup`/`refactor` |
| Markdown | Backticks for code, files, commands |
| Epic Emoji | Epics MUST have emoji in title |

**When to create beads:**
- Multi-session work, dependencies, discovered work, complex features, bugs, security issues

**Do NOT create beads for:**
- One-line fixes, trivial updates, single-session work (use TodoWrite instead)

### Bead Structure

```
┌─────────────────────────────────────────────────────────────────┐
│  BEAD TEMPLATE                                                  │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Title: 🔒 Brief description of work                            │
│  Type: bug | task | feature | epic                              │
│  Priority: P0 (critical) → P4 (backlog)                         │
│  Labels: <category>, <domain>, <component>, ...  (3-5 total)    │
│                                                                 │
│  Description:                                                   │
│  Brief problem statement (2-4 paragraphs). What and why.        │
│                                                                 │
│  ## Acceptance Criteria                                         │
│  - System MUST ...                                              │
│  - System SHOULD ...                                            │
│  - All tests MUST pass                                          │
│                                                                 │
│  ## Notes                                                       │
│  Files:                                                         │
│  - `path/to/file.go:42` - Description                           │
│  Related: spotter-xxx                                           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**Example:**
```bash
bd create --title "🔒 Encrypt OAuth tokens at rest" \
  --type bug --priority 1 \
  --labels "cleanup,security,database,auth" \
  --description "$(cat <<'EOF'
OAuth tokens stored in plaintext. Violates AUTH-008.

## Acceptance Criteria
- System MUST encrypt tokens using AES-256-GCM
- System MUST auto-decrypt on query
- All tests MUST pass

## Notes
Files:
- `ent/schema/spotifyauth.go:21-23`
Related: spotter-ahw
EOF
)"
```

### Metadata Reference

**Priority:** P0=critical, P1=high, P2=medium, P3=low, P4=backlog

**Types:** bug, task, feature, epic

**Categories (pick ONE):**
- `feature` - New functionality
- `research` - Investigation only (MUST NOT mutate code)
- `toil` - Repetitive maintenance
- `cleanup` - Code quality
- `refactor` - Restructure without behavior change

**RFC 2119 Keywords:**
- MUST/MUST NOT - Absolute requirement
- SHOULD/SHOULD NOT - Recommended
- MAY - Optional

**Comments:** Use `bd comments add <id> "update"` for progress updates (not Notes)

### Dependencies

```bash
bd dep add <issue> <depends-on>  # issue depends on depends-on
bd blocked                        # Show all blocked issues
```

### Formatting

- Use backticks for: files, paths, code, commands
- Emojis: 2-3 max per bead
- Research beads: MUST NOT mutate code, only create new beads

## Tech Stack
- **Language**: Go 1.24+
- **ORM**: [Ent](https://entgo.io/) (Code generation based)
- **Router**: [Chi](https://github.com/go-chi/chi) v5
- **Templating**: [Templ](https://github.com/a-h/templ)
- **Database**: SQLite (via `mattn/go-sqlite3`)
- **Logging**: `log/slog`
- **Config**: Viper
- **UI Icons**: [Hero Icons](http://heroicons.com)
- **UI Components**: DaisyUI (create shared, reusable components)

## Architecture

### Core Philosophy
Spotter separates "Providers" (sources of user data like history/playlists) from "Enrichers" (sources of metadata). Both use a Factory pattern for instantiation based on the current user context.

### Database (Ent)
- **Schema**: Defined in `ent/schema`
- **Generation**: Run `go generate ./ent` after schema changes
- **Usage**: Use the generated client for all DB operations. Avoid raw SQL.
- **Traversals**: Prefer Ent traversals over manual joins

### Providers (`internal/providers`)
Providers handle user-specific data interaction (Listen History, Playlists).

**Required Interfaces** (defined in `providers.go`):
- `HistoryFetcher`: Syncing listening history (handling pagination/cursors)
- `PlaylistManager`: Reading/Creating playlists
- `PlaylistSyncer`: Syncing playlists between services
- `Authenticator`: Handling OAuth flows

**Authentication:**
- Implement `Authenticator` for OAuth services (Spotify, Last.fm)
- **Token Management**: Auto-refresh tokens within provider methods. Callers should not handle expired tokens.
- **State**: Persist tokens via `ent` edges (e.g., `User.Edges.SpotifyAuth`)

**Factories**: `New(logger, config)` returns a `Factory` function `func(ctx, user) (Provider, error)`

### Enrichers (`internal/enrichers`)
Enrichers add metadata, images, and AI-generated content to local entities.

**Required Interfaces** (defined in `enrichers.go`):
- `IDMatcher`: **Critical**. Matches local names to external IDs (e.g., Name → Spotify ID)
- `ArtistEnricher`, `AlbumEnricher`, `TrackEnricher`: Fetches metadata using IDs

**Implementation:**
- Implement `IsAvailable() bool` to check if config/auth is present
- Return standardized structs (`ArtistData`, `AlbumData`)
- Download and resize images locally; do not store hotlinks

## Coding Standards

### Error Handling
- Use `fmt.Errorf("context: %w", err)` for wrapping
- Never panic
- Log errors with structured attributes using `slog`

### Context
- Pass `context.Context` as the first argument to all IO-bound functions
- Respect context cancellation

### Testing
- **Mocking**: Use `httptest.NewServer` to mock external APIs
- **No Network**: Tests must not make real network calls
- **Coverage**: Test happy paths, 401/403 (Auth), 429 (Rate Limits), and 404s
- **Regression Tests**: **MANDATORY** for all bug fixes
  - Write regression test BEFORE fixing the bug
  - Test MUST fail without the fix
  - Test MUST pass with the fix
  - Test name MUST include "Regression" prefix (e.g., `TestLogin_Regression_WhiteScreen`)
  - Test MUST document the original issue and bead ID in comments

### External API Etiquette
- **Rate Limiting**: Handle 429 responses gracefully (exponential backoff or error)
- **Batching**: Use batch APIs where possible (e.g., Spotify Audio Features)
- **User Agent**: Set a descriptive User-Agent string

## Linting & Code Quality

Spotter uses multiple linters to enforce code quality standards across all languages and file types. **Linting is mandatory before committing code.**

### Running Linters

```bash
make lint          # Run all linters (RECOMMENDED)
make lint-go       # Go code only (golangci-lint)
make lint-templ    # Templ templates only
make lint-yaml     # YAML files only
make lint-md       # Markdown documentation only
make lint-docker   # Dockerfile only
```

**When to run linters:**
- Before every `git commit`
- After making code changes
- When CI fails on linting
- Before closing beads

### Linters in Use

#### 1. **golangci-lint** (Go Code)
- **Purpose**: Meta-linter running 14+ Go linters in parallel
- **Config**: `.golangci.yml`
- **Enabled linters**: govet, staticcheck, errcheck, gosimple, ineffassign, unused, gofmt, goimports, misspell, revive, typecheck, goconst, unconvert, gocritic
- **Exclusions**: Generated code (`ent/`, `*_gen.go`, `*_templ.go`)
- **Timeout**: 5 minutes

**Common issues:**
```go
// ❌ BAD: Unchecked error
defer resp.Body.Close()

// ✅ GOOD: Check error
defer func() {
    if err := resp.Body.Close(); err != nil {
        log.Error("failed to close response body", "error", err)
    }
}()
```

#### 2. **templ fmt** (Templ Templates)
- **Purpose**: Format validation for `.templ` files
- **Config**: Built-in templ formatter
- **Files**: All 38 `.templ` files in `internal/views/`
- **Auto-fix**: Run `templ fmt .` (without `-fail`) to auto-format

#### 3. **yamllint** (YAML Files)
- **Purpose**: YAML syntax and formatting validation
- **Config**: `.yamllint.yml`
- **Standards**: 2-space indentation, 120 char line length
- **Exclusions**: `.beads/`, `node_modules/`, `.git/`
- **Files**: `.github/workflows/`, config files

#### 4. **markdownlint** (Markdown Documentation)
- **Purpose**: Consistent documentation formatting
- **Config**: `.markdownlint.json`
- **Files**: `README.md`, `AGENTS.md`, `SECURITY.md`, all `internal/*/README.md`
- **Standards**:
  - Line length: Disabled (too strict)
  - Allow inline HTML: `<details>`, `<summary>`, `<br>`, `<img>`
  - First line heading requirement: Disabled

#### 5. **hadolint** (Dockerfile)
- **Purpose**: Dockerfile best practices and security
- **Config**: `.hadolint.yaml`
- **Method**: Docker-based (hadolint/hadolint:latest)
- **Threshold**: Error-level only (warnings won't fail build)

### CI Integration

Linting runs automatically in GitHub Actions on:
- All pull requests to `main`
- All pushes to `main`

**CI Workflow:**
```
lint job (runs first)
  ├─ Set up Go 1.24 with caching
  ├─ Set up Node.js 20 with npm caching
  ├─ Install dependencies (golangci-lint, templ, yamllint, markdownlint)
  ├─ Run `make lint`
  └─ Fail-fast on first linting error
       ↓
test job (requires lint to pass)
  └─ Run `make test`
       ↓
build-and-push job (requires test to pass)
  └─ Build and push Docker image
```

**If linting fails in CI:**
1. Check the GitHub Actions log for specific errors
2. Run `make lint` locally to reproduce
3. Fix the issues or run auto-fixers (see below)
4. Commit fixes and push

### Auto-Fixing Linting Issues

Some linters can automatically fix issues:

```bash
# Go formatting
gofmt -w .
go run golang.org/x/tools/cmd/goimports@latest -w .

# Templ formatting
templ fmt .

# Markdown formatting (manual fixes usually required)
# YAML formatting (manual fixes usually required)
```

**IMPORTANT:** Always run `make lint` and `make test` after auto-fixing to ensure no breakage.

### Common Linting Errors

**Go (errcheck):**
```go
// ❌ Error: Error return value not checked
defer file.Close()

// ✅ Fix: Check and handle error
defer func() {
    if err := file.Close(); err != nil {
        slog.Error("failed to close file", "error", err)
    }
}()
```

**Go (shadow):**
```go
// ❌ Error: Variable shadows outer declaration
err := someFunc()
if err != nil {
    err := anotherFunc()  // shadows outer err
}

// ✅ Fix: Use different variable name or check inline
err := someFunc()
if err != nil {
    if err2 := anotherFunc(); err2 != nil {
        // ...
    }
}
```

**YAML:**
```yaml
# ❌ Error: Line too long
some_key: this is a very long value that exceeds the 120 character limit and should be wrapped

# ✅ Fix: Break into multiple lines
some_key: |
  this is a very long value that exceeds the limit
  and is now wrapped properly
```

**Markdown:**
```markdown
<!-- ❌ Error: MD033 - Inline HTML -->
<div>Some content</div>

<!-- ✅ Fix: Use allowed elements or convert to Markdown -->
<details>
<summary>Some content</summary>
...
</details>
```

### Troubleshooting

**"golangci-lint not found"**
```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

**"templ not found"**
```bash
go install github.com/a-h/templ/cmd/templ@latest
```

**"yamllint not found"**
```bash
# macOS
brew install yamllint

# Ubuntu/Debian
pip install yamllint
```

**"markdownlint not found"**
```bash
npm install
```

**"Docker daemon not running" (hadolint)**
- Start Docker Desktop
- Or skip Docker linting locally (CI will catch it)

## Documentation (Docusaurus)

Spotter documentation is built with [Docusaurus](https://docusaurus.io/) and deployed to GitHub Pages.

### Documentation Structure

```
website/
├── docs/                    # Documentation markdown files
│   ├── intro.md            # Landing/intro page
│   ├── getting-started/    # Installation, configuration, Docker
│   ├── features/           # Feature documentation
│   ├── providers/          # Provider docs (Navidrome, Spotify, Last.fm)
│   ├── enrichers/          # Enricher docs
│   ├── api/                # API reference
│   └── development/        # Contributing, architecture, testing
├── src/
│   ├── pages/              # Custom React pages (landing)
│   └── css/                # Custom CSS (retro themes)
├── static/                 # Static assets (images, favicon)
├── docusaurus.config.ts    # Site configuration
├── sidebars.ts             # Sidebar navigation
└── package.json            # Dependencies
```

### Documentation Commands

```bash
# Install documentation dependencies
make docs-deps

# Start local dev server with hot-reload
make docs-serve
# Opens at http://localhost:3000/spotter/

# Build static documentation site
make docs-build
# Output in website/build/

# Clean documentation artifacts
make docs-clean
```

### Writing Documentation

**File locations:**
- New feature docs go in `website/docs/features/`
- Provider docs go in `website/docs/providers/`
- Enricher docs go in `website/docs/enrichers/`
- API docs go in `website/docs/api/`

**Frontmatter:**
```markdown
---
sidebar_position: 1
---

# Page Title

Content here...
```

**Sidebar navigation:**
- Edit `website/sidebars.ts` to add new pages to navigation
- Pages are ordered by `sidebar_position` in frontmatter

**Admonitions (callouts):**
```markdown
:::note
Informational note
:::

:::tip
Helpful tip
:::

:::caution
Warning message
:::

:::danger
Critical warning
:::
```

### Documentation Deployment

Documentation automatically deploys to GitHub Pages when:
- Changes are pushed to `main` branch in the `website/` directory
- The `deploy-docs.yml` workflow runs

**Manual deployment:**
```bash
cd website
npm run build
npm run deploy
```

**Deployment URL:** https://joestump.github.io/spotter/

### Documentation Linting

Documentation markdown is linted by `make lint-md`. The `.markdownlint.json` config is set up for Docusaurus compatibility.

**Common fixes:**
- Use allowed HTML elements (details, summary, div, span)
- Avoid inline HTML when possible
- Add blank lines around code blocks
- Use consistent heading levels

### Adding New Documentation

1. Create markdown file in appropriate `website/docs/` subdirectory
2. Add frontmatter with `sidebar_position`
3. Add to `website/sidebars.ts` if not auto-detected
4. Run `make docs-serve` to preview
5. Run `make lint-md` to verify markdown
6. Commit changes - auto-deploys on merge to main

## Keeping AGENTS.md Updated

**META-RULE:** When workflow instructions are updated (Git, Beads, testing, etc.), AGENTS.md MUST be updated to reflect the changes. This ensures the documentation stays in sync with current practices and all contributors follow the same workflow.
