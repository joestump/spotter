# Button

Fully-rounded pills. Primary carries a green glow — the signature Spotter CTA.

## Classes

- `sp-btn` base. Variant: `sp-btn--primary` (green, main action), `sp-btn--secondary` (**violet — reserve for AI / Vibes actions**), `sp-btn--outline`, `sp-btn--ghost`, `sp-btn--danger`.
- Size: `sp-btn--sm`, `sp-btn--lg`. Icon: `sp-btn--icon` (+ `aria-label`).

## Rules

- One green primary per view. Violet secondary signals "smart / AI" features (Vibes, mixtape generation) — don't use it for ordinary actions.
- Always pill — never square the corners.

## In the app (DaisyUI)

Maps to `btn btn-primary` / `btn btn-secondary` / `btn btn-outline` / `btn btn-ghost` under `data-theme="spotter"`.

```html
<button class="sp-btn sp-btn--primary">Generate Mixtape</button>
<button class="sp-btn sp-btn--secondary">Ask Vibes AI</button>
```
