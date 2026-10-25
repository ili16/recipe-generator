---
name: Savor Culinary AI
colors:
  surface: '#f8faf6'
  surface-dim: '#d9dad7'
  surface-bright: '#f8faf6'
  surface-container-lowest: '#ffffff'
  surface-container-low: '#f3f4f0'
  surface-container: '#edeeeb'
  surface-container-high: '#e7e9e5'
  surface-container-highest: '#e1e3df'
  on-surface: '#191c1a'
  on-surface-variant: '#424842'
  inverse-surface: '#2e312f'
  inverse-on-surface: '#f0f1ee'
  outline: '#727971'
  outline-variant: '#c2c8bf'
  surface-tint: '#45664b'
  primary: '#23422a'
  on-primary: '#ffffff'
  primary-container: '#3a5a40'
  on-primary-container: '#acd0af'
  inverse-primary: '#abd0af'
  secondary: '#9c4328'
  on-secondary: '#ffffff'
  secondary-container: '#ff8f6e'
  on-secondary-container: '#76270e'
  tertiary: '#304033'
  on-tertiary: '#ffffff'
  tertiary-container: '#475749'
  on-tertiary-container: '#baccba'
  error: '#ba1a1a'
  on-error: '#ffffff'
  error-container: '#ffdad6'
  on-error-container: '#93000a'
  primary-fixed: '#c7ecca'
  primary-fixed-dim: '#abd0af'
  on-primary-fixed: '#02210c'
  on-primary-fixed-variant: '#2e4e35'
  secondary-fixed: '#ffdbd1'
  secondary-fixed-dim: '#ffb59f'
  on-secondary-fixed: '#3a0a00'
  on-secondary-fixed-variant: '#7d2c13'
  tertiary-fixed: '#d5e7d5'
  tertiary-fixed-dim: '#b9cbb9'
  on-tertiary-fixed: '#101f13'
  on-tertiary-fixed-variant: '#3b4b3d'
  background: '#f8faf6'
  on-background: '#191c1a'
  surface-variant: '#e1e3df'
typography:
  headline-xl:
    fontFamily: Epilogue
    fontSize: 44px
    fontWeight: '600'
    lineHeight: 52px
  headline-xl-mobile:
    fontFamily: Epilogue
    fontSize: 32px
    fontWeight: '600'
    lineHeight: 40px
  headline-lg:
    fontFamily: Epilogue
    fontSize: 34px
    fontWeight: '600'
    lineHeight: 42px
  headline-lg-mobile:
    fontFamily: Epilogue
    fontSize: 26px
    fontWeight: '600'
    lineHeight: 34px
  headline-md:
    fontFamily: Epilogue
    fontSize: 24px
    fontWeight: '500'
    lineHeight: 32px
  headline-sm:
    fontFamily: Epilogue
    fontSize: 20px
    fontWeight: '500'
    lineHeight: 28px
  body-lg:
    fontFamily: Plus Jakarta Sans
    fontSize: 18px
    fontWeight: '400'
    lineHeight: 28px
  body-md:
    fontFamily: Plus Jakarta Sans
    fontSize: 15px
    fontWeight: '400'
    lineHeight: 24px
  body-sm:
    fontFamily: Plus Jakarta Sans
    fontSize: 13px
    fontWeight: '400'
    lineHeight: 20px
  label-lg:
    fontFamily: Plus Jakarta Sans
    fontSize: 14px
    fontWeight: '600'
    lineHeight: 20px
  label-md:
    fontFamily: Plus Jakarta Sans
    fontSize: 12px
    fontWeight: '600'
    lineHeight: 16px
  label-sm:
    fontFamily: Plus Jakarta Sans
    fontSize: 11px
    fontWeight: '700'
    lineHeight: 14px
rounded:
  sm: 0.25rem
  DEFAULT: 0.5rem
  md: 0.75rem
  lg: 1rem
  xl: 1.5rem
  full: 9999px
spacing:
  gutter: 1.25rem
  gutter-mobile: 0.75rem
  margin: 2rem
  margin-mobile: 1rem
  space-xs: 0.25rem
  space-sm: 0.5rem
  space-md: 1rem
  space-lg: 1.5rem
  space-xl: 2.5rem
---

## Brand & Style

This design system expresses the warmth, craftsmanship, and precision of high-end cooking paired with the intuitive ease of modern ambient intelligence. It avoids clinical, sterile AI tropes in favor of an inviting, editorial kitchen aesthetic: tactile recipe paper, herbal freshness, and artisanal earthenware tones.

### Personality & Tone
- **Sensory & Appetizing:** Visual warmth through unbleached parchment tones, aromatic sage, and baked terracotta.
- **Thoughtful Sous-Chef:** Authoritative culinary knowledge delivered with calm encouragement, clarity, and zero patronizing friction.
- **Tactile Modernism:** Physical recipe card sensibilities translated into layered, softly contoured interactive surfaces.

### Design Movement
- **Tactile Minimalism & Warm Editorial:** Restrained layout structures anchored by rich typographic personality, delicate micro-borders, organic soft radiuses, and ambient herbal-tinted surface elevations.

## Colors

The palette grounds the interface in culinary organics: fresh sage garden greens, oven-fired terracotta accents, deep cast-iron charcoal for readable text, and layered toasted creams for welcoming surfaces.

### Palette Architecture
- **Primary (`#3A5A40` — Garden Sage):** Directs active primary flows, kitchen timers, verified culinary metrics, and key CTAs.
- **Secondary (`#C86446` — Toasted Terracotta):** Emphasizes flavor notes, interactive spice tags, warnings, timer alerts, and dynamic ingredient substitutions.
- **Tertiary (`#7A8B7B` — Muted Thyme):** Secondary metadata, inactive states, ingredient measurement notes, and nutritional badges.
- **Neutral (`#232624` — Cast Iron Charcoal):** Ultra-high legibility type hierarchy avoiding pure digital black to maintain organic harmony.
- **Background & Canvas:**
  - Base canvas: `#F9F6F0` (Unbleached Linen).
  - Elevated surfaces: `#FFFFFF` (Porcelain) and `#F2EDE4` (Warm Dough).
  - Ambient borders: `#E5DEC9` (Toasted Edge).

## Typography

The type system blends the editorial authority of culinary recipe publications with the clean, legible utility demanded while cooking hands-free.

- **Headlines (`Epilogue`):** Expressive, subtly warm geometric serif-adjacent posture. Delivers editorial presence across recipe titles, AI culinary summaries, and seasonal features.
- **Body & Labels (`Plus Jakarta Sans`):** Clean, rounded, open aperture sans-serif. Optimized for rapid scanning of prep instructions, fractions, and ingredient quantities at arm's length.

## Layout & Spacing

The layout is built on a responsive 12-column grid for wide viewports and a single/dual dynamic column layout on mobile devices.

### Form Factor Behavior
- **Mobile (<768px):** Single-column workflow with sticky bottom action trays for culinary execution (step timers, unit converters). Gutter defaults to `gutter-mobile` (`0.75rem`) with outer padding set to `margin-mobile` (`1rem`).
- **Tablet (768px - 1024px):** 8-column layout. Split view dividing AI culinary conversation on the left from live interactive recipe cards on the right.
- **Desktop (>1024px):** 12-column grid with a maximum content canvas of 1280px. Preserves comfortable white space resembling an open kitchen workbench.

## Elevation & Depth

Visual hierarchy leverages warm physical sheet stacking and diffused ambient drop shadows rather than stark digital blurs.

### Surface Hierarchy
- **Base Ground (`#F9F6F0`):** Kitchen counter level. Completely flat.
- **Recipe Canvas Tier (`#FFFFFF`):** 1px subtle boundary border in `#E5DEC9` paired with an ambient shadow: `0 4px 20px -2px rgba(35, 38, 36, 0.05)`.
- **Floating Overlays & AI Prompt Panels:** Stacked card elevation with directional warmth: `0 12px 32px -4px rgba(58, 90, 64, 0.08)`, creating depth through sage-tinted shadows.
- **Active Kitchen Focus / Modals:** `0 20px 48px -8px rgba(35, 38, 36, 0.14)`.

## Shapes

The shape grammar is soft and organic, echoing hand-thrown ceramics, smooth river stones, and baker’s dough.

- **Standard Containers (`0.5rem` / `8px`):** Input fields, inline tags, nutrition micro-badges.
- **Cards & Conversational Trays (`1rem` / `16px`):** Recipe cards, instruction blocks, meal planners.
- **Interactive Control Elements (`1.5rem` - `9999px`):** Fully rounded pills for weekly calendar days, ingredient check-pills, and primary action buttons.

## Components

### Buttons
- **Primary:** Background `#3A5A40`, text `#F9F6F0`, full pill rounding (`rounded-full`), height 48px. Active scale down (0.98) with subtle sage glow.
- **Secondary / Craft:** Background `#F2EDE4`, text `#3A5A40`, 1px `#E5DEC9` border.
- **Accent (Terracotta):** Reserved for destructive, stop-timer, or instant substitutions (`#C86446` with white text).

### Culinary Recipe Cards
- **Structure:** Porcelain white fill, 16px radius, inset 1px toasted border (`#E5DEC9`).
- **Header:** Epilogue headline over prep/cook time chips.
- **Footer:** Macro nutritional strip with herbal badges.

### Step-by-Step Tags & Instruction Counters
- **Step Pill:** Terracotta tinted background (`#F9EBE7`), text `#C86446`, font `label-md` bold.
- **Active Step Card:** Left border highlight (3px solid `#3A5A40`), elevated surface with soft green glow.

### Interactive Ingredient Checklist
- **Unchecked State:** Left-aligned circular pill, border 1.5px solid `#7A8B7B`, body text in cast iron `#232624`.
- **Checked State:** Fill `#3A5A40` with white checkmark icon, strikethrough text in `#7A8B7B`, muted background.

### Conversational AI Bubbles
- **User Prompt:** Deep sage fill `#3A5A40`, crisp text `#F9F6F0`, rounded 16px with bottom-right corner pinched (4px).
- **Culinary AI Response:** Warm dough fill `#F2EDE4`, border `#E5DEC9`, charcoal text `#232624`, rounded 16px with bottom-left corner pinched (4px).

### Weekly Calendar Meal Pills
- **State Inactive:** Outline 1px `#E5DEC9`, vertical stack (Day abbreviation + Date), muted background.
- **State Planned:** Background `#F9F6F0`, active terracotta dot marker beneath date.
- **State Active/Selected:** Sage fill `#3A5A40`, white text, soft drop elevation.

### Nutritional Badges
- Compact pill elements (`rounded-full`), neutral sage tint (`#EEF2EE`), typography `label-sm`, color `#3A5A40`.