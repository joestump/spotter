# VibesPanel (AI surface)

The panel for AI features (mixtape generation, DJ personas). The **violet
gradient + ring is how Spotter signals "this is the smart part"** — the one
place violet leads instead of green.

## Structure

- `sp-vibes` — violet-tinted gradient container with an inset violet ring.
- Header: a ✦ glyph + `sp-h3` title + `sp-badge--violet` "AI" chip.
- Body: `sp-input` + `sp-btn--secondary` (violet) to submit a prompt.

## Rules

- Reserve `sp-vibes` for genuinely AI-driven surfaces. Everywhere else, green leads.

## In the app

Compose with DaisyUI: `.vibe-gradient` / `.vibe-ring` utilities (static/css/input.css) + `btn-secondary`.
