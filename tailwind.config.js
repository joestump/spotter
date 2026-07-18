/** @type {import('tailwindcss').Config} */

/**
 * Spotter Design System — "evokes Spotify, legally its own thing"
 * ---------------------------------------------------------------------------
 * Ownable identity (NOT Spotify's #1DB954 / #1ED760):
 *   Spotter Green  #1FDF6E  — bright spring-emerald primary
 *   Vibe Violet    #8B5CF6  — electric-violet secondary for AI / Vibes features
 *   Warm Gold      #F5B921  — sparing energy accent
 * Near-black cool-tinted elevation ramp, Figtree (geometric, Circular-adjacent,
 * open-source) type, pill buttons + rounded cards.
 *
 * The `spotter` DaisyUI theme below is the single source of truth for the app;
 * the same values are mirrored in docs-site/src/css/custom.css and ds-bundle/.
 */

const spotterTheme = {
  primary: "#1FDF6E",
  "primary-content": "#04140A",
  secondary: "#8B5CF6",
  "secondary-content": "#0B0616",
  accent: "#F5B921",
  "accent-content": "#1A1200",
  neutral: "#1C1F26",
  "neutral-content": "#E6E7EA",
  "base-100": "#16181D", // cards, sidebar, navbar (elevated surface)
  "base-200": "#0C0D10", // page background (darkest)
  "base-300": "#252830", // borders, hover, raised
  "base-content": "#E6E7EA",
  info: "#3ABEF9",
  "info-content": "#001019",
  success: "#1FDF6E",
  "success-content": "#04140A",
  warning: "#F5B921",
  "warning-content": "#1A1200",
  error: "#F43F5E",
  "error-content": "#1A0308",
  "--rounded-box": "1rem",
  "--rounded-btn": "9999px",
  "--rounded-badge": "9999px",
  "--tab-radius": "0.6rem",
  "--animation-btn": "0.22s",
  "--animation-input": "0.2s",
  "--btn-focus-scale": "0.97",
  "--border-btn": "1px",
  colorScheme: "dark",
};

module.exports = {
  content: [
    "./cmd/**/*.{html,templ,go}",
    "./internal/**/*.{html,templ,go}",
    "!./**/*_test.go",
  ],
  theme: {
    extend: {
      fontFamily: {
        sans: ['Figtree', 'Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        display: ['Figtree', 'Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
      boxShadow: {
        'glow-primary': '0 8px 28px -6px rgba(31, 223, 110, 0.45)',
        'glow-secondary': '0 8px 28px -6px rgba(139, 92, 246, 0.45)',
      },
    },
  },
  plugins: [
    require("@iconify/tailwind").addDynamicIconSelectors(),
    require("daisyui"),
  ],
  daisyui: {
    // Curated theme set — `spotter` is the default; the rest are offered in the
    // appearance switcher (all exist in DaisyUI so the switcher never breaks).
    themes: [
      { spotter: spotterTheme },
      "night",
      "synthwave",
      "dracula",
      "dark",
      "light",
    ],
  },
};
