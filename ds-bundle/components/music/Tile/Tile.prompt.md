# Tile (Album / Playlist)

The browsable "shelf" unit of the app. Square art on top, title + subtitle below,
lifts on hover.

## Structure

- `sp-card` + `sp-card--hover` — the clickable container (wrap in `<a>`).
- `sp-art` — square 1:1 art. With no image it shows a green→violet gradient + glyph; drop an `<img>` inside for real art.
- `sp-card__title` / `sp-card__sub` — name + meta (track count, sync state).
- Add a `sp-badge sp-badge--violet` "AI" chip on AI-generated mixtapes.

## Rules

- Lay out in a responsive grid (`repeat(auto-fill, minmax(180px, 1fr))`).
- Keep art square — it's the signature music-app rhythm.

## In the app (DaisyUI)

Maps to `<a class="card bg-base-100 shadow-md card-hover">` with a `figure.pt-[100%]` art block (see `internal/views/components/playlist_tile.templ`).
