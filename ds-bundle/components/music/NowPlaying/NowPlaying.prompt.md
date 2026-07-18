# NowPlaying & Stats

## NowPlaying

A card with cover art, track title/artist, a green progress bar, and a round
play button.

- `sp-progress` + `sp-progress__fill` (set `width` %) — the green scrubber.
- `sp-btn--primary sp-btn--icon` for play/pause.

## Stat

- `sp-stat` container; `sp-stat__label`, `sp-stat__value` (green, heavy), `sp-stat__delta`.
- Use the green value to make key numbers pop against near-black.

## In the app (DaisyUI)

Progress → `progress progress-primary`; stat → `stats`/`stat` with `stat-value text-primary`.
