---
sidebar_position: 3
---

# Vibes Engine

The Vibes Engine is Spotter's AI-powered mixtape generation system. Create DJ personas with unique personalities that curate playlists based on your listening history.

## How It Works

1. **DJ Personas**: Create DJs with unique personalities, genre preferences, and artists to include/exclude
2. **Mixtapes**: Create mixtapes assigned to a DJ with optional scheduling
3. **Generation**: The AI uses the DJ's persona, your listening history, and available library tracks to curate a personalized mixtape
4. **Track Matching**: AI-suggested tracks are fuzzy-matched against your Navidrome library
5. **Sync to Navidrome**: Optionally sync generated mixtapes as playlists to Navidrome

## Creating a DJ Persona

A DJ persona defines the personality and preferences for mixtape generation:

- **Name**: Give your DJ a memorable name
- **Personality**: Describe their style (e.g., "A late-night radio host with a love for deep cuts")
- **Genre Preferences**: Preferred genres to include
- **Excluded Artists**: Artists to never include in mixtapes
- **Included Artists**: Artists to favor in selections

## Creating a Mixtape

1. Navigate to the **Vibes** section
2. Click **New Mixtape**
3. Select a DJ persona
4. Configure settings:
   - **Name**: Mixtape name
   - **Track Count**: Number of tracks (5-100)
   - **Seed** (optional): Seed the mixtape with an artist, album, or tracks
5. Click **Generate**

## Seeding Mixtapes

Mixtapes can be seeded with:

- **Artist**: Generate a mixtape inspired by a specific artist
- **Album**: Generate a mixtape based on an album's vibe
- **Tracks**: Generate a mixtape starting from specific seed tracks

### Create from Artist Page

1. Go to any artist's page
2. Click the **AI** dropdown menu
3. Select **Create Mixtape**
4. Choose a DJ persona
5. Configure and generate

## Scheduled Regeneration

Mixtapes can be scheduled to regenerate automatically:

- **Daily**: Fresh mixtape every day
- **Weekly**: New tracks every week
- **Monthly**: Monthly refresh

## Playlist Enhancement

The "Enhance Vibes" feature uses DJ personas to improve existing playlists:

1. **Reordering**: Analyze and reorder tracks for better flow
2. **Track Additions**: Suggest new tracks that complement the selection
3. **Guarantee**: Original tracks are never removed, only reordered and augmented

### Enhancement Modes

| Mode | Description |
| :--- | :--- |
| **One-time Enhance** | Apply changes directly to Navidrome. Playlist remains manually editable |
| **Convert to Mixtape** | DJ takes over the playlist. It becomes a regeneratable Mixtape |

## Configuration

```bash
# Track limits
SPOTTER_VIBES_DEFAULT_MAX_TRACKS=25
SPOTTER_VIBES_MIN_TRACKS=5
SPOTTER_VIBES_MAX_TRACKS=100

# Listening history context
SPOTTER_VIBES_HISTORY_DAYS=30
SPOTTER_VIBES_MAX_HISTORY_TRACKS=50

# AI settings
SPOTTER_VIBES_MODEL=gpt-4o
SPOTTER_VIBES_TEMPERATURE=0.8
SPOTTER_VIBES_MAX_TOKENS=4000
SPOTTER_VIBES_TIMEOUT_SECONDS=120

# Track matching
SPOTTER_VIBES_MIN_MATCH_CONFIDENCE=0.7
```

## Prompt Templates

The Vibes Engine uses customizable prompt templates stored in `data/prompts/`:

- `generate_mixtape.tmpl` - Template for mixtape generation
- `enhance_playlist.tmpl` - Template for playlist enhancement

You can customize these templates to adjust how the AI generates and enhances playlists.

## Similar Artists

The Similar Artists feature uses AI to find related artists in your library:

1. Click **Find Similar Artists** on an artist's page
2. The AI analyzes the artist's metadata and your library
3. Returns recommendations with:
   - **Confidence Score**: How similar the artists are (0.0-1.0)
   - **Explanation**: Why they're recommended
   - **Provider**: Source of the recommendation (OpenAI, LastFM, etc.)

### Features

- Recommendations are limited to artists in your library
- Visual indicators show AI badge and confidence score
- Hover over tiles to see similarity explanations
- Re-run analysis anytime for fresh recommendations
