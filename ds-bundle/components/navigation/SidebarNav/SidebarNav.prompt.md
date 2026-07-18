# SidebarNav

The app's left navigation on an elevated near-black surface.

## Structure

- `sp-wordmark` — the green "Spotter" wordmark (heavy, tight).
- `sp-nav` list of `sp-nav__item`; the current page gets `sp-nav__item--active` (green text on a soft green pill).

## Rules

- Exactly one `--active` item. Icons sit left of the label.

## In the app (DaisyUI)

Maps to `.menu.menu-lg` items; active uses the `.active` class (styled green in static/css/input.css).
