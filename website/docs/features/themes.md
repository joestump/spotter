---
sidebar_position: 5
---

# Themes

Spotter features two custom-designed retro themes that create an immersive nostalgic experience.

## Light Theme - 1970s Music Cabinet

The light theme evokes the warm, cozy feeling of a vintage 1970s hi-fi system.

### Design Elements

- **Color Palette**: Rich amber (#d97706), golden yellow (#f59e0b), and deep brown (#92400e) tones
- **Background**: Cream and beige tones (#fef3c7) reminiscent of wood veneer
- **Typography**: Bold, slightly spaced lettering with gentle shadows

### Visual Effects

- Subtle wood-grain texture overlays
- Raised beveled borders on cards and buttons
- Soft inset shadows suggesting depth
- Warm text shadows for a soft, analog feel

### Aesthetic

Think vintage record players, warm living rooms, and analog warmth.

## Dark Theme - 1980s Cyberpunk

The dark theme channels the neon-soaked, digital future imagined in 1980s cyberpunk.

### Design Elements

- **Color Palette**: Neon cyan (#00d9ff), magenta (#ff00ff), and electric green (#00ff41)
- **Background**: Deep dark blue-black (#0f0f23) with navy accents
- **Typography**: Sharp, uppercase lettering with wider tracking and neon glow

### Visual Effects

- Scan line overlay across the entire interface
- Glowing neon borders on all interactive elements
- Multiple layered box shadows creating depth and glow
- Text shadows with neon glow effects
- Icon glow filters for that electric feel

### Aesthetic

Blade Runner meets Tron - dark, electric, and futuristic.

## Theme Controls

### Sidebar Toggle

Temporarily switch themes using the toggle in the sidebar. This preference is persisted in browser localStorage and does not affect your database settings.

### Preferences

Permanently set your theme preference from **Preferences** > **General**:

- **Light**: Always use the 1970s theme
- **Dark**: Always use the 1980s theme
- **System**: Automatically follow your operating system's preference

## Configuration

Configure available themes and defaults:

```bash
# Available themes (DaisyUI theme names)
SPOTTER_THEME_AVAILABLE=light,dark,cupcake

# Default theme for new users
SPOTTER_THEME_DEFAULT=dark
```

## Technical Details

Themes are built on top of [DaisyUI](https://daisyui.com/) with extensive custom CSS overlays. The theme system uses:

- CSS custom properties for color variables
- JavaScript for theme switching
- LocalStorage for temporary preferences
- Database storage for permanent preferences
- System preference detection via `prefers-color-scheme`
