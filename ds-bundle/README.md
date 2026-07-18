# Spotter Design System

A dark, energetic music-app design language for **Spotter** (AI-powered music
discovery and playlist sync for Navidrome). It **evokes** the feel of a modern
streaming app — near-black surfaces, a vivid accent, pill controls, browsable
art shelves — with an **ownable identity that copies no real brand**:

- **Spotter Green `#1FDF6E`** — primary (deliberately NOT Spotify's `#1DB954`/`#1ED760`)
- **Vibe Violet `#8B5CF6`** — secondary, reserved for AI / Vibes features
- **Warm Gold `#F5B921`** — sparing highlight accent

Adding the violet as a first-class secondary is intentional: it breaks the
green-only monochrome that reads as one specific competitor, and it gives AI
features their own visual home.

## Two idioms, one look

This library is **class-based (`.sp-*`)** so previews render standalone. The
**production Spotter app** is Go + templ styled with **Tailwind + DaisyUI**, and
uses the DaisyUI **`spotter` theme** (defined in `tailwind.config.js`) under
`data-theme="spotter"`. The `.sp-*` classes here map 1:1 onto DaisyUI classes:

| Spotter DS class | DaisyUI in the app |
|---|---|
| `sp-btn sp-btn--primary` | `btn btn-primary` (green) |
| `sp-btn sp-btn--secondary` | `btn btn-secondary` (violet) |
| `sp-badge sp-badge--green / --violet / --gold` | `badge badge-primary / -secondary / -accent` |
| `sp-card` / `sp-card--hover` | `card bg-base-100` / `.card-hover` |
| `sp-input` | `input input-bordered` |
| surfaces `--sp-bg / --sp-surface / --sp-line` | `bg-base-200 / bg-base-100 / border-base-300` |

When building for the real app, prefer the DaisyUI classes with
`data-theme="spotter"`. When building a standalone mock here, use `.sp-*`.

## Setup

Wrap the UI in `sp-root` (sets Figtree, near-black background, base text). The
system is dark-first; `data-theme="spotter"` is the canonical theme.

```html
<body class="sp-root" data-theme="spotter"> … </body>
```

## The styling idiom

Style with the `.sp-*` component classes; use `var(--sp-*)` tokens for your own
layout glue. Do **not** invent new class names.

### Components

| Family | Base class | Key modifiers / parts |
|---|---|---|
| Button | `sp-btn` | `--primary` `--secondary` `--outline` `--ghost` `--danger` · `--sm` `--lg` `--icon` |
| Badge | `sp-badge` | `--green` `--violet` `--gold` `--danger` `--neutral` `--soft` |
| Card / Tile | `sp-card` | `--hover` `--pad` · parts `sp-card__body/__title/__sub` · art `sp-art` |
| Stat | `sp-stat` | `sp-stat__label/__value/__delta` |
| Now playing | `sp-progress` | `sp-progress__fill` (green scrubber) |
| Vibes (AI) | `sp-vibes` | violet gradient + ring surface |
| Field | `sp-field` | `sp-field__label`, control `sp-input` |
| Sidebar | `sp-nav` | `sp-nav__item`, `sp-nav__item--active` |
| Alert | `sp-alert` | `--success` `--info` `--danger` |
| Type | `sp-display` `sp-h1` `sp-h2` `sp-h3` `sp-body` | `sp-muted` `sp-small` `sp-wordmark` `sp-eyebrow` `sp-code` |
| Layout | `sp-stack` | `sp-row` (horizontal wrap) |

### Token vocabulary (see `tokens/spotter.tokens.css` for all)

- **Brand:** `--sp-green` `--sp-green-hi` `--sp-green-lo` · `--sp-violet` `--sp-violet-hi` · `--sp-gold`
- **Surfaces:** `--sp-bg` `--sp-surface` `--sp-surface-2` `--sp-line` · **Text:** `--sp-text` `--sp-text-strong` `--sp-text-muted` `--sp-text-faint`
- **Semantic:** `--sp-info` `--sp-success` `--sp-warning` `--sp-danger`
- **Radii:** `--sp-radius-sm/md/lg/xl` + `--sp-radius-pill` (controls are pill; cards `--sp-radius-lg`)
- **Glows:** `--sp-glow-green` `--sp-glow-violet` (on primary/secondary CTAs)
- **Type:** `--sp-font-sans` (Figtree), `--sp-font-mono`

## Brand rules that matter

- **Green leads; violet means "AI".** Primary actions and healthy state are green. Violet is reserved for Vibes / AI-generated content. Gold is a rare highlight.
- **Everything rounds.** Buttons/badges/inputs are pills; cards/art use `--sp-radius-lg/md`.
- **Near-black, high energy.** Dark surfaces, bold Figtree headings (700–800, tight tracking), a green glow on the main CTA.
- **Ownable, not imitative.** Never reintroduce `#1DB954`/`#1ED760`, and don't use any real streaming brand's logo, wordmark, or proprietary typeface.

## Where the truth lives

- `styles.css` — every component class.
- `tokens/spotter.tokens.css` — the full token set.
- `components/<group>/<Name>/<Name>.prompt.md` — per-component usage + DaisyUI mapping.
- `guidelines/colors.html`, `guidelines/typography.html` — foundations.

## One idiomatic snippet

```html
<body class="sp-root" data-theme="spotter">
  <div class="sp-vibes">
    <div class="sp-row" style="gap:8px">
      <span class="sp-h3" style="margin:0">Vibes Engine</span>
      <span class="sp-badge sp-badge--violet">AI</span>
    </div>
    <p class="sp-muted">Generate a mixtape from your listening history.</p>
    <div class="sp-row" style="flex-wrap:nowrap">
      <input class="sp-input" style="flex:1" placeholder="Describe the vibe…" />
      <button class="sp-btn sp-btn--secondary">Create</button>
    </div>
  </div>
</body>
```
