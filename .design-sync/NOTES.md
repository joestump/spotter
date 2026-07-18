# design-sync notes — Spotter Design System

## What this is

A Spotify-**evoking** but legally ownable design language across the whole
Spotter product, driven from one canonical palette:

- **Spotter Green `#1FDF6E`** — primary (NOT Spotify's `#1DB954`/`#1ED760`)
- **Vibe Violet `#8B5CF6`** — secondary, reserved for AI / Vibes features
- **Warm Gold `#F5B921`** — sparing accent
- Near-black cool-tinted surfaces, **Figtree** type, pill controls.

Three surfaces, one look:

1. **The app** (Go + templ + Tailwind/DaisyUI). The `spotter` DaisyUI theme is
   the source of truth — defined in `tailwind.config.js`. It's the **default**
   theme (`internal/config/config.go` → `theme.default=spotter`,
   `theme.available=spotter,night,synthwave,dracula,dark,light`; `Base()` in
   `internal/views/layouts/base.templ` renders `data-theme="spotter"`).
   Global polish (Figtree, btn glow, card hover, green active-nav, violet Vibes
   utilities) lives in `static/css/input.css`; rebuild with `npm run build:css`.
   Because the app uses DaisyUI semantic classes (`bg-base-*`, `text-primary`,
   `btn-primary`, `badge-*`), the theme cascades through every view.

2. **The docs** (Docusaurus at `docs-site/`). `docs-site/src/css/custom.css`
   was re-themed off Spotify's exact green onto the Spotter palette + Figtree +
   a Vibe-Violet accent + pill buttons. NOTE: this file is **managed** by the
   design-docs scaffold (`.design-docs.json`) — its checksum there was updated
   to match, but a future `docs` scaffold regen could still reset it; re-apply
   the Spotter theme if that happens.

3. **claude.ai/design project** "Spotter Design System" (hand-authored,
   class-based `.sp-*`) in `ds-bundle/` —
   https://claude.ai/design/p/76ec4472-fa47-43c8-aea6-c9ebaf66f76f
   Each component's `.prompt.md` maps `.sp-*` → the app's DaisyUI classes.

## Legal hygiene (the "don't send lawyers" goal)

- Never reintroduce `#1DB954` / `#1ED760`. The docs used to hardcode `#1DB954`
  everywhere and call the theme "Spotify-Inspired" — both were removed.
- No real streaming brand's logo, wordmark, or proprietary typeface (Circular →
  Figtree, an open SIL OFL geometric face). The violet secondary intentionally
  breaks the green-only monochrome.

## Keeping it in sync / re-syncing

- Palette changes must be made in ALL THREE token homes: `tailwind.config.js`
  (spotterTheme), `docs-site/src/css/custom.css`, and
  `ds-bundle/tokens/spotter.tokens.css`.
- The claude.ai/design project has no `_ds_sync.json` anchor → re-uploads
  re-verify/re-upload the whole `ds-bundle/` to the pinned project
  (`.design-sync/config.json`). Conventions header source is `ds-bundle/README.md`
  (mirrored to `.design-sync/conventions.md`).

## Build / verify

- App: `make generate` (or `templ generate`) → `npm run build:css` → `go build ./...`.
  Config tests: `go test ./internal/config/` (updated for the new theme defaults).
- Docs: `cd docs-site && npm run build`.
