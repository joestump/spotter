---
sidebar_position: 7
---

# OpenAI Enricher

The OpenAI enricher uses AI to generate intelligent summaries, biographies, and tags for your music library.

## Features

- **Artist Biographies**: Engaging, detailed artist bios
- **Album Summaries**: Context about album significance
- **Track Descriptions**: Information about individual tracks
- **Smart Tags**: Relevant genre and mood tags
- **Image Analysis**: Vision-powered artwork analysis

## Setup

### Get API Key

1. Go to [OpenAI Platform](https://platform.openai.com/)
2. Sign up or log in
3. Navigate to [API Keys](https://platform.openai.com/api-keys)
4. Click **Create new secret key**
5. Copy the key (it won't be shown again)

### Configure

```bash
SPOTTER_OPENAI_API_KEY=sk-your-api-key-here
SPOTTER_OPENAI_MODEL=gpt-4o
```

## Configuration Options

| Variable | Description | Default |
| :--- | :--- | :--- |
| `SPOTTER_OPENAI_API_KEY` | OpenAI API key | *Required* |
| `SPOTTER_OPENAI_BASE_URL` | API endpoint URL | `https://api.openai.com/v1` |
| `SPOTTER_OPENAI_MODEL` | Model to use | `gpt-4o` |
| `SPOTTER_METADATA_AI_PROMPTS_DIRECTORY` | Prompt templates | `./data/prompts` |

## Using Alternative Providers

Spotter supports OpenAI-compatible APIs like LiteLLM:

```bash
SPOTTER_OPENAI_BASE_URL=https://your-litellm-instance.com/v1
SPOTTER_OPENAI_MODEL=claude-3-opus  # Or any supported model
```

## Generated Content

### Artists

- Engaging biography
- Musical style description
- Career highlights
- Influence and impact
- AI-generated genre tags

### Albums

- Album context and significance
- Musical themes
- Production notes
- Critical reception summary
- AI-generated mood tags

### Tracks

- Song description
- Musical characteristics
- Thematic content
- AI-generated tags

## Image Analysis

The enricher uses vision capabilities to analyze:

- Album artwork style and imagery
- Color palette
- Visual themes
- Artwork quality

This analysis informs the generated descriptions.

## Prompt Templates

Customize AI output by editing templates in `data/prompts/`:

```bash
SPOTTER_METADATA_AI_PROMPTS_DIRECTORY=./data/prompts
```

### Template Files

- `artist.tmpl` - Artist enrichment prompt
- `album.tmpl` - Album enrichment prompt
- `track.tmpl` - Track enrichment prompt

### Template Variables

Templates receive context including:

- Entity metadata (name, dates, etc.)
- Data from previous enrichers
- Image data (if available)

## Processing Order

OpenAI runs **last** in the enrichment pipeline to:

1. Have access to all metadata from other enrichers
2. Summarize and synthesize information
3. Fill gaps in missing data

## Rate Limiting

OpenAI limits are based on:

- Requests per minute
- Tokens per minute
- Tokens per day

Spotter handles rate limiting with automatic backoff.

## Cost Considerations

AI enrichment uses tokens:

| Model | Input Cost | Output Cost |
| :--- | :--- | :--- |
| gpt-4o | $5/1M tokens | $15/1M tokens |
| gpt-4o-mini | $0.15/1M tokens | $0.60/1M tokens |

For large libraries, consider:
- Using gpt-4o-mini for initial enrichment
- Processing incrementally over time
- Setting up a LiteLLM proxy with caching

## Troubleshooting

### "Invalid API key"

1. Verify the key is correct
2. Check it's not expired or revoked
3. Ensure you have API access (not just ChatGPT)

### Rate Limit Errors

1. Spotter will automatically back off
2. Consider upgrading your OpenAI tier
3. Use a smaller model (gpt-4o-mini)

### Poor Quality Output

1. Check prompt templates
2. Ensure other enrichers ran first
3. The model may lack context for obscure artists

## Marking AI Content

All AI-generated content is marked in the UI:

- AI badge indicator
- Distinguishes AI vs human-written content
- Allows users to identify generated text
