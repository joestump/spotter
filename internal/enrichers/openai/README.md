# OpenAI Enricher

## Overview

The OpenAI enricher uses AI language models to generate rich, contextual metadata including summaries, enhanced biographies, and intelligent tags. It runs as the final enrichment step, synthesizing all previously collected data into cohesive, readable content. Supports OpenAI's API and LiteLLM-compatible proxies for flexibility.

## Features

- **Implements**: `ArtistEnricher`, `AlbumEnricher`, `TrackEnricher`
- **Data provided**:

  - **Artists**: AI-generated biography, summary, intelligent tags
  - **Albums**: Album summary, contextual tags, cover art commentary, dominant colors
  - **Tracks**: Track summary, mood/style tags
  - **Custom Prompts**: Template-based prompt system with Go templates
  - **LiteLLM Support**: Use any OpenAI-compatible API (Anthropic, local models, etc.)
  - **Cost Tracking**: Monitors token usage and costs
  - **Smart Re-enrichment**: Only re-enriches when data changes or sufficient time has passed

## Configuration

### Required Settings

```yaml
openai:
  api_key: "your-openai-api-key"
  base_url: "<https://api.openai.com/v1">  # Or LiteLLM proxy URL
  model: "gpt-4o"  # Or gpt-3.5-turbo, gpt-4-turbo, etc.

metadata:
  ai:
    enabled: true
    prompts_directory: "./prompts"  # Optional: custom prompt templates
    max_tags: 10
    re_enrich_after_days: 30

```

**Environment Variables** (alternative):

```bash
OPENAI_API_KEY=your-api-key
OPENAI_BASE_URL=<https://api.openai.com/v1>
OPENAI_MODEL=gpt-4o
AI_ENABLED=true
AI_PROMPTS_DIRECTORY=./prompts
AI_MAX_TAGS=10
AI_RE_ENRICH_AFTER_DAYS=30

```

### Configuration Notes

- **API Key**: OpenAI API key or LiteLLM proxy key
- **Base URL**: OpenAI endpoint or LiteLLM proxy URL
- **Model**: Which LLM to use (affects cost and quality)
- **Prompts Directory**: Path to custom prompt templates (optional)
- **Max Tags**: Maximum AI-generated tags per entity (prevents over-tagging)
- **Re-enrich After**: Days to wait before re-enriching (prevents unnecessary API calls)

## How to Get API Keys

### OpenAI API Key

1. **Go to OpenAI Platform**

   - Visit: <https://platform.openai.com/>

2. **Create an Account**

   - Sign up with email or Google/Microsoft account
   - Verify email address

3. **Add Payment Method**

   - Go to Settings → Billing
   - Add credit card (required for API access)
   - Set usage limits to control costs

4. **Create API Key**

   - Go to API Keys section
   - Click "Create new secret key"
   - Name it "Spotter" (or custom name)
   - Copy the key immediately (shown only once)

5. **Add to Spotter Config**

   - Paste API key into configuration file
   - Set model preference

6. **Set Usage Limits**

   - Settings → Limits
   - Set monthly budget cap
   - Enable email notifications

### LiteLLM Setup (Alternative)

For non-OpenAI models or cost savings:

1. **Install LiteLLM**

   ```bash
   pip install litellm
   ```

2. **Start LiteLLM Proxy**

   ```bash
   litellm --model gpt-3.5-turbo

   # Or for Anthropic: litellm --model claude-3-sonnet-20240229
   # Or for local: litellm --model ollama/llama2

   ```

3. **Configure Spotter**

   ```yaml
   openai:
     api_key: "any-key"  # LiteLLM may not require real key for local models
     base_url: "<http://localhost:8000">
     model: "gpt-3.5-turbo"  # Or your chosen model
   ```

## AI Enrichment Strategy

### When AI Enrichment Runs

OpenAI enricher runs **last** in the enrichment pipeline because:

1. **Synthesizes All Data**: Uses metadata from all other enrichers
2. **Context Aware**: Can reference biographies, genres, tags from multiple sources
3. **Avoids Redundancy**: Doesn't re-process data that other enrichers provide better
4. **Cost Optimization**: Only runs on entities that need AI enhancement

### Re-enrichment Logic

AI enrichment is expensive, so Spotter only re-enriches when:

1. **Never enriched**: `LastAiEnrichedAt` is null
2. **Sufficient time passed**: More than `re_enrich_after_days` since last enrichment
3. **Manual trigger**: User explicitly requests re-enrichment

Example:

```go
if artist.LastAiEnrichedAt == nil ||
   time.Since(*artist.LastAiEnrichedAt) > 30*24*time.Hour {
    // Run AI enrichment
}

```

### Tag Deduplication

AI-generated tags are deduplicated against existing tags:

- Case-insensitive comparison
- Removes tags already from MusicBrainz, Last.fm, Spotify
- Respects `max_tags` limit
- Prioritizes novel, descriptive tags

## Model Selection

### Recommended Models

| Model | Cost | Quality | Speed | Use Case |
|-------|------|---------|-------|----------|
| `gpt-4o` | $$$ | Excellent | Fast | Best overall (recommended) |
| `gpt-4-turbo` | $$$ | Excellent | Medium | High-quality summaries |
| `gpt-3.5-turbo` | $ | Good | Very Fast | Cost-effective for large libraries |
| `gpt-4o-mini` | $$ | Very Good | Very Fast | Balance of cost/quality |

### Cost Comparison

Approximate costs per 1,000 entities (as of 2024):

- **gpt-4o**: $15-30 (with images)
- **gpt-4-turbo**: $30-50
- **gpt-3.5-turbo**: $3-6
- **gpt-4o-mini**: $5-10

> Actual costs depend on prompt length and entity complexity

### Choosing a Model

**Use gpt-4o if**:

- You want the best quality summaries
- Budget allows for premium models
- Processing albums with cover art

**Use gpt-3.5-turbo if**:

- You have a large library (>10,000 entities)
- Cost is a primary concern
- You're okay with slightly less nuanced text

**Use gpt-4o-mini if**:

- You want balance between cost and quality
- Processing medium-sized libraries
- Good enough quality for most use cases

## Prompt Template System

### Built-in Prompts

Spotter includes default prompts for:

- `artist.tmpl`: Artist biography and summary generation
- `album.tmpl`: Album summary and commentary
- `track.tmpl`: Track summary and mood analysis

### Custom Prompts

Create custom prompts in the `prompts_directory`:

**prompts/artist.tmpl**:

```text
You are a music journalist writing about {{ .Name }}.

{{- if .Bio }}
Existing biography: {{ .Bio }}
{{- end }}

{{- if .Genres }}
Genres: {{ join .Genres ", " }}
{{- end }}

Generate:

1. A concise 2-3 sentence biography
2. A one-sentence summary
3. 5-8 descriptive tags (mood, style, era, influence)

Respond in JSON format:
{
  "biography": "...",
  "summary": "...",
  "tags": ["tag1", "tag2", ...]
}

```

### Available Template Variables

**Artist Template**:

- `Name`, `SortName`, `Bio`, `Genres`, `Tags`
- `Popularity`, `FollowerCount`
- `MusicBrainzID`, `SpotifyID`, `LastFMURL`
- `Albums` (array of album info)
- `Tracks` (array of track info)

**Album Template**:

- `Name`, `Artist`, `Year`, `ReleaseDate`
- `AlbumType`, `Label`, `Genre`, `Tags`
- `Popularity`, `TotalTracks`
- `Tracks` (array of track details)
- `ArtistBio`, `ArtistGenres`
- `HasCoverArt` (boolean)

**Track Template**:

- `Name`, `Artist`, `Album`
- `TrackNumber`, `DiscNumber`, `Duration`
- `BPM`, `MusicalKey`, `Energy`, `Danceability`
- `Tags`, `Genres`
- `ArtistBio`

## API Limitations

### Rate Limits

**OpenAI**:

- **Tier 1** (new): 500 RPM (requests per minute)
- **Tier 2** ($50+ spent): 5,000 RPM
- **Tier 3** ($100+ spent): 10,000 RPM

**LiteLLM**:

- Depends on underlying provider
- Local models: No limits

### Token Limits

**Context Windows**:

- `gpt-4o`: 128,000 tokens
- `gpt-3.5-turbo`: 16,385 tokens
- Spotter prompts typically use 500-2,000 tokens

**Output Limits**:

- Spotter requests max 500 tokens output
- Sufficient for summaries and tags

### Cost Considerations

**Input vs Output**:

- Input tokens (prompt) are cheaper
- Output tokens (response) cost more
- Spotter minimizes output length

**Image Tokens**:

- Album cover art analysis uses vision models
- Images count as ~1,000 tokens each
- Disabled by default to control costs

### Known Quirks

1. **JSON Extraction**

   - AI may wrap JSON in markdown code blocks
   - Spotter automatically extracts JSON from text
   - Handles various response formats

2. **Rate Limit Errors**

   - Exponential backoff on 429 errors
   - Retry after 1s, 2s, 4s, 8s
   - Max 4 retries before failing

3. **Token Counting**

   - Approximate, not exact
   - Used for cost estimation only
   - Actual billing from OpenAI may differ slightly

4. **Model Updates**

   - OpenAI periodically updates models
   - Model names remain stable
   - Quality may improve over time

## Implementation Notes

### Prompt Construction

1. **Load Template**: From file or use fallback
2. **Gather Context**: Collect all available metadata
3. **Execute Template**: Fill in variables
4. **Send to AI**: POST to chat completions API
5. **Parse Response**: Extract JSON from text
6. **Deduplicate Tags**: Remove existing tags
7. **Return Data**: Structured metadata

### Response Parsing

Handles multiple response formats:

```text
Plain JSON: {"summary": "...", "tags": [...]}

Markdown: ```json
{"summary": "...", "tags": [...]}

```

With text: Here's the response:
{"summary": "...", "tags": [...]}
Done.

```text

### Tag Deduplication Algorithm

```go

func deduplicateTags(newTags, existingTags []string, maxTags int) []string {
    // 1. Normalize to lowercase
    existing := make(map[string]bool)
    for _, tag := range existingTags {
        existing[strings.ToLower(strings.TrimSpace(tag))] = true
    }

    // 2. Filter new tags
    var deduplicated []string
    for _, tag := range newTags {
        normalized := strings.ToLower(strings.TrimSpace(tag))
        if normalized != "" && !existing[normalized] {
            deduplicated = append(deduplicated, strings.TrimSpace(tag))
        }
    }

    // 3. Apply max limit
    if len(deduplicated) > maxTags {
        deduplicated = deduplicated[:maxTags]
    }

    return deduplicated
}

```text

### Cost Tracking

Spotter logs token usage for monitoring:

```

Input tokens: 1,234
Output tokens: 456
Estimated cost: $0.0123

```text

Use these logs to:

- Monitor spending
- Optimize prompts (reduce input tokens)
- Choose appropriate model

### Error Handling

- **401 Unauthorized**: Invalid API key
- **429 Too Many Requests**: Rate limited, retry with backoff
- **500 Internal Server Error**: OpenAI issue, retry
- **JSON Parse Error**: AI didn't return valid JSON, log and skip

## Testing

### Running Tests

```bash

# Run OpenAI enricher tests

go test ./internal/enrichers/openai/...

# Run with verbose output

go test -v ./internal/enrichers/openai/...

# Run with coverage

go test -cover ./internal/enrichers/openai/...

# Run specific test

go test -run TestEnrichArtist ./internal/enrichers/openai/...

```text

### Test Coverage

Tests cover:

- Factory creation with/without API key
- Template loading and execution
- Fallback prompts
- JSON response parsing (various formats)
- Tag deduplication logic
- Cost estimation
- Duration formatting
- Skip logic (recently enriched)
- Error handling (401, 429, 500)
- Mock API responses

### Mock AI Server

Tests use `httptest.NewServer`:

```go

server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

    response := ChatResponse{
        Choices: []struct {
            Message struct {
                Role    string `json:"role"`
                Content string `json:"content"`
            } `json:"message"`
        }{
            {
                Message: struct {
                    Role    string `json:"role"`
                    Content string `json:"content"`
                }{
                    Role:    "assistant",
                    Content: `{"summary": "Test", "tags": ["tag1"]}`,
                },
            },
        },
    }
    json.NewEncoder(w).Encode(response)
}))

```text

## Troubleshooting

### "Invalid API Key" (401)

- Verify API key is correct in configuration
- Check key hasn't been revoked in OpenAI dashboard
- Ensure key has no extra whitespace
- Test key manually: `curl <https://api.openai.com/v1/models> -H "Authorization: Bearer YOUR_KEY"`

### "Rate Limit Exceeded" (429)

- Wait for rate limit to reset (usually 1 minute)
- Reduce concurrent enrichment operations
- Upgrade OpenAI tier (spend more to increase limits)
- Consider using gpt-3.5-turbo (higher rate limits)

### High Costs

- Switch to gpt-3.5-turbo or gpt-4o-mini
- Reduce prompt length (minimize context)
- Increase `re_enrich_after_days` to avoid re-enrichment
- Disable album cover art analysis
- Set monthly budget limits in OpenAI dashboard

### Poor Quality Responses

- Upgrade to gpt-4o or gpt-4-turbo
- Refine custom prompts with better instructions
- Ensure other enrichers ran first (provide good context)
- Check that entity has sufficient existing metadata

### No Tags Generated

- AI may not return tags field
- Check AI response in logs
- Verify prompt requests tags
- Try different model (gpt-4o better at structured output)

### Tags All Duplicates

- Normal if other enrichers already have good tags
- AI deduplicates against existing tags
- This saves costs (doesn't repeat work)
- Consider it a sign of good existing metadata

### JSON Parse Errors

- AI didn't return valid JSON
- Check prompt instructs JSON format
- Try gpt-4o (better at structured output)
- Review AI response in logs

### Timeout Errors

- Increase timeout in config (default 120s)
- Reduce prompt complexity
- Try faster model (gpt-3.5-turbo)
- Check network connection to OpenAI

## API Reference

- **OpenAI API**: <https://platform.openai.com/docs/api-reference>
- **Chat Completions**: <https://platform.openai.com/docs/api-reference/chat>
- **Models**: <https://platform.openai.com/docs/models>
- **Pricing**: <https://openai.com/pricing>
- **Rate Limits**: <https://platform.openai.com/docs/guides/rate-limits>
- **LiteLLM**: <https://docs.litellm.ai/>

## Example Usage

```go

// Create factory
factory := openai.New(logger, config)

// Create enricher (no user required)
enricher, err := factory(ctx, nil)
if err != nil {
    // Handle error
}
if enricher == nil {
    // No API key configured or AI disabled
    return
}

// Enrich artist (synthesizes all existing metadata)
artist := &ent.Artist{
    ID:             1,
    Name:           "Radiohead",
    Bio:            "English rock band...", // From Last.fm
    Genres:         []string{"alternative rock", "art rock"}, // From Spotify
    Tags:           []string{"experimental", "melancholic"}, // From Last.fm
    LastAiEnrichedAt: nil, // Never enriched with AI
}

artistEnricher := enricher.(enrichers.ArtistEnricher)
data, err := artistEnricher.EnrichArtist(ctx, artist)
if err != nil {
    // Handle error
}
if data == nil {
    // Skipped (recently enriched or insufficient data)
    return
}

fmt.Printf("AI Biography: %s\n", data.AIBiography)
fmt.Printf("AI Summary: %s\n", data.AISummary)
fmt.Printf("AI Tags: %v\n", data.AITags)
// AI Tags: ["progressive", "atmospheric", "90s", "british", "innovative"]

// Enrich album
album := &ent.Album{
    ID:    1,
    Name:  "OK Computer",
    Year:  1997,
    Genre: "Alternative Rock",
    Tags:  []string{"classic", "influential"},
    Edges: ent.AlbumEdges{
        Artist: &ent.Artist{
            Name: "Radiohead",
            Bio:  "English rock band...",
        },
    },
}

albumEnricher := enricher.(enrichers.AlbumEnricher)
albumData, err := albumEnricher.EnrichAlbum(ctx, album)
if err != nil {
    // Handle error
}

fmt.Printf("Album Summary: %s\n", albumData.AISummary)
// "A landmark album exploring themes of alienation and technology..."

// Enrich track
track := &ent.Track{
    ID:           1,
    Name:         "Paranoid Android",
    Tags:         []string{"progressive rock"},
    Genres:       []string{"alternative rock"},
    BPM:          144.5,
    Energy:       0.75,
    Danceability: 0.42,
    Edges: ent.TrackEdges{
        Artist: &ent.Artist{Name: "Radiohead"},
        Album:  &ent.Album{Name: "OK Computer"},
    },
}

trackEnricher := enricher.(enrichers.TrackEnricher)
trackData, err := trackEnricher.EnrichTrack(ctx, track)
if err != nil {
    // Handle error
}

fmt.Printf("Track Summary: %s\n", trackData.AISummary)
fmt.Printf("Mood Tags: %v\n", trackData.AITags)
// Mood Tags: ["anxious", "complex", "dramatic", "intense"]

```text

## Best Practices

1. **Run AI Last**: Always run OpenAI enricher after all other enrichers
2. **Use gpt-4o for Quality**: Best balance of speed, cost, and quality
3. **Use gpt-3.5-turbo for Volume**: Large libraries (>10,000 entities)
4. **Set Budget Limits**: Configure monthly cap in OpenAI dashboard
5. **Monitor Costs**: Check Spotter logs for token usage
6. **Increase Re-enrich Interval**: 30-90 days is usually sufficient
7. **Custom Prompts**: Tailor prompts to your music taste and needs
8. **Cache Results**: Don't re-enrich unnecessarily
9. **Deduplicate Tags**: Let Spotter filter existing tags automatically
10. **Test Prompts**: Try different prompts with small batches first
11. **Use LiteLLM**: For experimentation with different models
12. **Batch Processing**: Enrich in batches, not all at once (control costs)

## Why Use OpenAI Enricher?

- **Contextual Understanding**: AI understands music context, not just keywords
- **Synthesizes Data**: Combines information from all sources into coherent text
- **Natural Language**: Produces readable summaries, not just data points
- **Intelligent Tagging**: Generates mood, style, era tags that humans would use
- **Flexible**: Works with any amount of existing metadata
- **Customizable**: Template system allows full prompt control
- **Multi-Model**: Supports OpenAI, Anthropic, local models via LiteLLM
- **Cost Effective**: Only enriches when needed, deduplicates tags

## Enrichment Order

OpenAI enricher should run **LAST**:

Order:

1. MusicBrainz (IDs, basic metadata)
2. Lidarr (library management data)
3. Navidrome (local file metadata)
4. Spotify (audio features, popularity)
5. Last.fm (community tags, biographies)
6. Fanart.tv (high-quality images)
7. **OpenAI** (synthesize everything)

This ensures AI has maximum context from all other sources.

## Comparison with Other Enrichers

| Feature | OpenAI | MusicBrainz | Last.fm | Spotify |
|---------|--------|-------------|---------|---------|
| Summaries | ✓ (AI) | ✗ | ✗ | ✗ |
| Biographies | ✓ (Enhanced) | Limited | ✓ (Raw) | ✗ |
| Tags | ✓ (Intelligent) | ✓ | ✓ | Limited |
| Context Aware | ✓ | ✗ | ✗ | ✗ |
| Customizable | ✓ (Prompts) | ✗ | ✗ | ✗ |
| Cost | $$-$$$ | Free | Free | Free |
| Rate Limits | 500-10K RPM | 1/sec | None | 180/min |
| Quality | Excellent | Good | Good | Good |
| Requires Data | ✓ (from others) | ✗ | ✗ | ✗ |

## Related Files

- `openai.go`: Main enricher implementation
- `openai_test.go`: Comprehensive unit tests
- `../enrichers.go`: Base interfaces and types
- `prompts/`: Custom prompt templates (optional)
