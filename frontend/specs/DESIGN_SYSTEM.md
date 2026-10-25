# Design System — Target

Date: 2026-09-09
Status: **design, not implemented.** Today the "design system" is 13 color tokens in
`recipe-generator-mobile/src/context/ThemeContext.tsx`.
Implementation tasks: `../../BACKLOG.md` Phase 5.

---

## 1. What is wrong today, measured

### One accent, used for everything

`accent: '#cc2222'` is the **only** accent colour and it is **byte-identical in dark and light
mode** (`ThemeContext.tsx:31` and `:47`). It is referenced **122 times across 19 files**, plus
`accentFaded` another 20 times.

Every call to action in the app is that same red: `generateButton`, `saveButton`, `primaryButton`,
`voiceConfirmBtn`, the AlertContext OK button, the AppShell FAB, the PreferencesPanel add button —
and also every selected chip, every list bullet, every active nav item, the review screen's border,
and `headerTintColor`. When the primary action, the decorative bullet, and the destructive action
are the same colour, none of them mean anything. **This is the "black and red buttons everywhere"
complaint, and it is literally one hex value.**

### The dark palette is too narrow to read as anything but flat black

```
bg #16161a → surface #1f1f23 → card #252529 → hairline #2c2c31 → border #38383f
```

Five greys spanning roughly 14% lightness. There is no elevation, because there is not enough
range to express one.

### 80 colour literals live outside the theme

In **17 of 36 files**: 64 hex + 16 `rgba()`.

| Value | Count | What it is |
|---|---|---|
| `#fff` | 42 | on-accent text, hardcoded because there is no `onAccent` token |
| `#4caf50` | 4 | an ad-hoc success green |
| `#e53935` | 3 | a **second** red (errors) |
| `#cc4444` | 3 | a **third** red |
| `#000` | 3 | shadows (`theme.shadow` exists and is used exactly once) |
| `#ff3333` | 2 | a **fourth** red (`Loading.tsx`) |
| `#cc2222` | 1 | the accent, copy-pasted |
| `#c0392b` | 1 | a **fifth** red (destructive button) |
| `#5c9cf5` | 1 | an info blue |
| `#2d1010` `#0d2316` `#121a2d` | 1 each | dark-only icon backgrounds — **unreadable in light mode** |
| `#111` | 1 | a dark-only card (`Loading.tsx`) |
| `rgba(...)` | 16 occurrences, **14 distinct values** | overlays from 0.3 to 0.95 — no two agree |

Worst per file: `GenerateScreen.tsx` 21 lines, `CookingModeScreen.tsx` 7, `AlertContext.tsx` 7,
`WeekView.tsx` 6.

**Five different reds. One green. One blue. No scale, no semantic slots.**

### Two components are not themed at all

- **`components/Loading.tsx`** — module-level `StyleSheet.create` with no theme access:
  `#ff3333` spinner, `#111` card, `rgba(0,0,0,0.95)` scrim, `#fff` text. It has six call sites and
  is **visually broken in light mode**.
- **`context/AlertContext.tsx`** — `iconFor()` picks the icon *and* a hardcoded dark-only colour
  pair by **regex-matching the alert's English text**
  (`/error|fail|invalid|no recipe|permission|unsupported|sorry|could not/`). Every caller already
  knows the severity; this is a guess, and it is English-only.

### The theme has no other axes, and does not persist

13 tokens, all colour. **No spacing, radius, typography, or motion scale** — which is exactly why
`PreferencesPanel` forks ~15 style values off a `compact` boolean, and why breakpoints disagree
(`AppShell` uses 900, `GenerateScreen` uses 720).

The provider is `useState(true)` (`ThemeContext.tsx:64`). There is **no `useColorScheme()` anywhere
in the app** and no persistence, so the toggle resets to dark on every reload and the OS
light/dark preference is ignored entirely.

---

## 2. Target palette — warm, food-first

The direction is already in the repo: `inspiration/1.png` is a warm amber-on-warm-dark mockup, and
the shipped app is a regression from it. Grey-black plus fire-engine red reads like a developer
tool; food should read warm.

```
                    LIGHT                  DARK
bg                  #FBF8F4  warm paper    #1A1816  warm near-black
surface             #FFFFFF                #232020
surfaceRaised       #FFFFFF + shadow       #2C2927
border              #E7E1D8                #3A3532
hairline            #F0EBE4                #2C2927
text                #1C1917                #F5F1EC
subtext             #57534E                #A8A29E
muted               #8A8078                #78716C

accent              #E5691F   onAccent #FFFFFF     amber / terracotta
accentFaded         #FDF0E6                #2E1D11
danger              #DC2626   onDanger #FFFFFF
success             #16A34A   onSuccess #FFFFFF
warning             #D97706   onWarning #1C1917
info                #2563EB   onInfo   #FFFFFF
overlay             rgba(28,25,23,0.45)    rgba(0,0,0,0.6)
```

Note the dark ramp spans `#1A1816`→`#3A3532` — deliberately wider than today's, so elevation is
expressible.

### Non-negotiable rules

1. **Red means destructive. Nothing else.** The five current reds collapse into one `danger`,
   used only for delete/discard. Primary actions are `accent`.
2. **Every semantic colour ships with its `on*` pair.** This is what retires the 42 hardcoded
   `#fff`.
3. **No colour literal outside `src/theme/`.** Including `rgba()` — overlays are tokens.
4. **Follow the OS by default.** `useColorScheme()` for the initial value, an explicit user
   override persisted to AsyncStorage, and `'system' | 'light' | 'dark'` as the stored states.
5. **Light mode is a first-class target, not a fallback.** Two components are currently broken in
   it; nothing new ships that has only been looked at in dark.

### Contrast

Body text against its background must clear WCAG AA (4.5:1); large text and UI borders 3:1. The
pairs above are chosen to satisfy this — `#1C1917` on `#FBF8F4` and `#F5F1EC` on `#1A1816` both
exceed 14:1. **`onAccent` white on `#E5691F` is roughly 3.3:1**, which is fine for large/bold
button labels but *not* for small text — so accent-coloured text on a plain background must use a
darkened accent in light mode, not the button fill colour. Verify with a contrast checker when the
tokens land; do not eyeball it.

---

## 3. Scales

```ts
space  = { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 }
radius = { sm: 6, md: 10, lg: 16, full: 999 }
type   = {
  display: { size: 30, lineHeight: 36, weight: '700' },
  title:   { size: 20, lineHeight: 26, weight: '600' },
  body:    { size: 15, lineHeight: 22, weight: '400' },
  label:   { size: 13, lineHeight: 18, weight: '600' },
  caption: { size: 12, lineHeight: 16, weight: '400' },
}
motion = { fast: 120, base: 200, slow: 320 }
layout = { breakpointWide: 900 }   // ONE breakpoint, not two
```

Line heights are part of the type scale, not an afterthought — recipe steps are multi-line prose
and are currently unreadable at default leading.

---

## 4. Primitives

New: `src/components/ui/`. None of these exist today, which is *why* there are 80 literals — every
screen hand-rolls its own button.

| Component | API |
|---|---|
| `Button` | `variant: 'primary' \| 'secondary' \| 'ghost' \| 'danger'`, `size: 'sm' \| 'md' \| 'lg'`, `loading`, `icon`, `fullWidth` |
| `Card` | `elevation: 'flat' \| 'raised'`, `padding` from the space scale |
| `Text` | `variant` from the type scale, `tone: 'default' \| 'subtle' \| 'muted' \| 'accent' \| 'danger'` |
| `Chip` | `selected`, `onPress`, `icon` |
| `Screen` | safe-area + max-width wrapper; owns the one breakpoint |
| `Sheet` | bottom sheet — the planner's row action needs it (§5 of `AGENT_CHAT.md` companion) |

`components/Sidebar.tsx` is the model to copy: fully themed, `makeStyles(theme)`, zero literals.

**Alert severity becomes a prop, not a regex.** `showAlert`/`confirmAction` take
`severity: 'info' | 'success' | 'warning' | 'error'` and the caller passes it. Delete `iconFor`.

---

## 5. Consolidate the recipe renderer

`components/RecipeView.tsx` is the canonical structured-recipe renderer — meta badges,
section-grouped ingredients, step cards with timer/temperature badges, plus the shared
`recipeMarkdownStyles(t)`. It is used in **two of the four** places that render a recipe.

The other three re-implement it: `GenerateScreen`'s review pane, `RecipeSkeleton`, and
`CookingModeScreen`. `fmtIngredient` exists twice (`GenerateScreen.tsx:49` and
`RecipeView.tsx:11`). `CookingModeScreen` goes further and carries **two hand-written markdown
parsers** that regex structured data back out of rendered markdown, with German strings baked in
(`zubereitung`, `zutat`) — a client-side reimplementation of a backend format.

One renderer, driven by `RecipeDocument`. In the agent-chat world this same component is the
inline artifact card, so consolidating it is a prerequisite for Phase 3, not cosmetic.

---

## 6. Migration rule

Tokens land first and completely; then literals are migrated **file by file, largest first**
(`GenerateScreen` 21 → `CookingModeScreen` 7 → `AlertContext` 7 → `WeekView` 6 → the rest).

Do not migrate a file half-way. A file is done when
`grep -n "#[0-9a-fA-F]\{3,8\}\|rgba\?(" <file>` returns nothing.

The whole migration is done when that grep returns nothing across `src/` outside `src/theme/`.
That is the acceptance check; it is mechanical, so use it.

---

## 7. The Stitch mockups — `specs/stitch_smart_meal_and_recipe_hub/`

Date added: 2026-09-10. Source: a Stitch export, "Savor Culinary AI". Five screens, each with a
`screen.png` and a Tailwind-CDN `code.html`, plus `savor_culinary_ai/DESIGN.md` holding the token
frontmatter the HTML compiles from.

**Status: visual direction, not a contract.** The mockups are mobile, light-mode only, and a large
share of what they render has no data behind it (§7.5). Treat §7.2–§7.3 as decided, §7.4 as a
screen-by-screen inventory, and §7.5 as the honest gap list.

### 7.1 Screens in the export

| Directory | Screen | Maps to today |
|---|---|---|
| `ai_recipe_studio_chat/` | AI Chat — thread, artifact recipe card, quick-action chips | `ChatScreen.tsx` (Phase 3.3) |
| `weekly_meal_planner/` | Meal Plan — plan summary + 7 day cards | `mealplan/WeekView.tsx` (Phase 4) |
| `my_recipes_variants/` | My Recipes — search, filters, variant-aware cards | `RecipesScreen.tsx` |
| `grocery_pantry_inventory/` | Pantry — shopping list + inventory | **nothing** (Phase 6.1 is the list half) |
| `discover_creators/` | Discover — creators, viral feed, remix | **nothing**, and out of scope |

`savor_culinary_ai_logo/` is a wordmark render only.

### 7.2 Palette — this replaces §2

The mockups are **sage-primary with terracotta as the accent**, not the amber-primary of §2. Adopt
the mockup direction. Two reasons beyond taste:

1. §2 already flags its own problem: `onAccent` white on `#E5691F` is **~3.3:1**, below AA for
   small text, so accent-coloured text needed a second darkened token. White on sage `#3A5A40`
   is ~8.8:1 — the same token works as a fill *and* as text, and the exception disappears.
2. It keeps a warm accent (`#C86446` terracotta ≈ the amber that motivated §2) while freeing the
   primary from having to carry every CTA, chip, bullet, and nav highlight — which is the actual
   §1 complaint.

```
                    LIGHT                        DARK (derive — see 7.3)
bg                  #F9F6F0  unbleached linen    #1A1816
surface             #FFFFFF  porcelain           #232020
surfaceRaised       #F2EDE4  warm dough          #2C2927
border              #E5DEC9  toasted edge        #3A3532
text                #232624  cast iron           #F5F1EC
subtext             #424842                      #A8A29E
muted               #7A8B7B  muted thyme         #78716C

primary             #3A5A40  garden sage    onPrimary  #F9F6F0
primaryFaded        #EEF2EE                            #1E2A20
accent              #C86446  terracotta     onAccent   #FFFFFF
accentFaded         #F9EBE7                            #2E1D11
danger              #BA1A1A                 onDanger   #FFFFFF
success             #3A5A40  (= primary; sage already reads as "good")
```

**Careful — `DESIGN.md` contradicts itself.** Its YAML frontmatter (and therefore every
`code.html`) sets `primary: #23422a` with `#3a5a40` as `primary-container`; its prose §Colors
calls `#3A5A40` the primary. The rendered screens use `#23422a` for the dark header bars and
`#3a5a40`-family greens for fills. **Take the prose value `#3A5A40` as `primary` and keep
`#23422a` as a `primaryDeep` for headers/bars**, rather than inheriting the frontmatter's
inversion.

The §2 non-negotiables all still hold unchanged: red means destructive only, every semantic colour
ships its `on*` pair, no literal outside `src/theme/`, follow the OS, light mode is first-class.

### 7.3 Typography and shape

- **Headlines: Epilogue** (500/600/700). **Body and labels: Plus Jakarta Sans** (400/500/600/700).
  Both are Google Fonts — `@expo-google-fonts/epilogue`, `@expo-google-fonts/plus-jakarta-sans`,
  loaded via `expo-font`. Neither is installed today; the app uses system fonts.
- The mockup type scale is finer-grained than §3's five variants (three headline sizes with
  separate mobile cuts, three body, three label). **Keep §3's five names** and map: `display` →
  `headline-lg-mobile` (26/34), `title` → `headline-sm` (20/28), `body` → `body-md` (15/24),
  `label` → `label-lg` (14/20), `caption` → `label-md` (12/16). Adding six more variants to serve
  a mockup is how a token set stops being usable.
- Radius: `sm 4 · md 8 · lg 16 · xl 24 · full 999`. §3's scale is close; widen `lg` to 16 and add
  `xl`. Cards are 16, pills and primary buttons are `full`, inputs and micro-badges are 8.
- Elevation is **warm and diffuse**, not grey: card `0 4px 20px -2px rgba(35,38,36,0.05)`,
  floating panel `0 12px 32px -4px rgba(58,90,64,0.08)` (sage-tinted), modal
  `0 20px 48px -8px rgba(35,38,36,0.14)`. These are `rgba()` literals and so belong in
  `src/theme/` per §6.

**The mockups have no dark mode.** Every screen is light. §2's rule 5 stands but inverts here: the
dark ramp above is a derivation, not a design, and must be checked against a contrast tool before
it ships. Do not treat the sage/terracotta pair as validated in dark until it is.

### 7.4 Component inventory the mockups imply

Beyond §4's six primitives, the five screens share these. Build them once, in `src/components/ui/`
or `src/components/recipe/` — every one appears on at least two screens:

| Component | Where it appears | Notes |
|---|---|---|
| `TabBar` | all five | 5 fixed bottom tabs — see §7.6 |
| `AppHeader` | all five | logo · "Savor / <screen>" · avatar. Fixed, translucent, `pt-safe` |
| `FilterPillRow` | Chat, Recipes, Discover | horizontal-scroll chip row, one selected |
| `MetaBadge` | all five | icon + label pill; the mockups use ~8 tints — cap it at the semantic set |
| `StatStrip` | Chat, Discover | 3-column divided figures (Active / Simmer / kcal) |
| `RecipeArtifactCard` | Chat | header chips · title · badges · stat strip · modifications · actions |
| `DiffRow` | Chat | `— old  →  new`, struck-through left, coloured right. See §7.5 |
| `DayCard` | Meal Plan | coloured header bar (day + date + kind badge) over a body |
| `RecipeListCard` | Recipes, Discover | badge row · title · summary · tag chips · two actions |
| `ChecklistRow` | Pantry | circle → filled check + strikethrough |
| `SectionCard` | Meal Plan, Pantry | titled white card with a right-aligned count chip |

`RecipeArtifactCard` and `RecipeListCard` must both be driven by `RecipeDocument` through the
single renderer of §5 — they are two densities of one thing, not two components.

### 7.5 What has no data behind it

Read this before estimating anything. The mockups render a product roughly two domains ahead of
the backend.

| Rendered | Backing today | Verdict |
|---|---|---|
| "3 Variants" count, "Create Variant" | `recipes.variant_of_recipe_id` **exists**, `POST /meal-plan/variants/:id` exists | **Cheap.** A count query and a button. Do it. |
| Favourites filter | `Recipe.my_vote` (±1) exists | **Cheap.** `my_vote === 1` is a favourite. |
| "Fast (<25m)" / "Batch Cooked" filters | `RecipeDocument.total_minutes`, `servings` exist | **Cheap.** Client-side filter. |
| Leftover / "No Cooking Required" days | `no_cook_days` carry-forward exists (`mealplan.go` `enforceNoCookCarryover`) | **Cheap and overdue** — the logic runs today and the UI never says so. |
| `480 kcal`, `42g Protein`, macro strip | **nothing.** `RecipeDocument` has no nutrition fields at all | New schema + prompt field + migration. Appears on **all five** screens. |
| `4.9 (18)`, "1.4k Loves", "342 Cooked this wk" | only per-user `my_vote`; no aggregate, no count, no 5-star, no cooked mark | Needs Phase 6.3 plus an aggregate. Community counts need Discover. |
| "94% pantry", "Pantry Ready (6/6 Staples)", "All 7 Staples Ready" | **nothing** — no pantry domain exists | Whole new domain. §7.7 |
| Expiry ("2 days left"), barcode scan, "82% Stocked" | **nothing** | Same, plus a device camera path. |
| "Intelligent Modifications" diff rows | `/refine-recipe` returns a whole new document, never a diff | Needs the agent to emit structured substitutions, or a client-side doc diff. |
| "Perfect Pair" wine pairing | nothing | A prompt field, or drop it. |
| Creators, subscriptions, viral feed, remix counts | nothing | Out of scope — §7.8 |
| "Store Delivery · Order", "Export List" | nothing | Third-party integration. Out of scope. |

### 7.6 The navigation shell changes shape

The mockups replace the current **`Sidebar` rail/drawer + FAB** (`navigation/AppShell.tsx`,
7 entries, 4 of them route-less "Soon" pills) with **five fixed bottom tabs**: AI Chat · My
Recipes · Meal Plan · Pantry · Discover.

This is a real improvement on the current shell — five real destinations beats seven of which
four are inert — but note:

- Bottom tabs need `@react-navigation/bottom-tabs`, which is **not installed**. The app is
  native-stack only.
- Two of the five tabs (Pantry, Discover) have no backend. Shipping the tab bar before them turns
  the "Soon" pill problem into a "Soon" *tab* problem, which is worse — a tab is a promise.
- The wide-web layout has no mockup. `Sidebar` is the only component in the app that is fully
  themed with zero literals (§4) and it handles wide-web. **Keep it for `isWebWide`; add tabs for
  narrow.** Do not delete `Sidebar` to chase a mobile mockup.

Recommended landing order: **three tabs first** (Chat · Recipes · Meal Plan), with Pantry added by
Phase 6.1 and Discover only if it is ever scheduled.

### 7.7 Pantry is a new domain, not a screen

`grocery_pantry_inventory/` is two features sharing a tab:

1. **Shopping list** — auto-synced from the week plan, grouped by aisle (Produce & Herbs,
   Proteins & Seafood, Pantry & Dry Goods), each line tagged with the meal it serves
   ("For Mon roast"), checkable. This **is** Phase 6.1, plus grouping and provenance.
   `recipe_ingredients` already carries `quantity NUMERIC` + `unit` + `quantity_text`, which is
   what 6.1 says. Aisle grouping is new and needs an ingredient→category mapping — the model can
   emit one on the existing generation call; do not hand-maintain a list.
2. **Pantry inventory** — what the user has, with quantities, categories (Fridge / Freezer /
   Spices & Dry), expiry dates, a stocked percentage, barcode scanning, and "find recipes using
   these". None of this exists in any form. It implies a `pantry_items` table, CRUD, an expiry
   job, and a camera/barcode dependency.

(1) is scheduled. (2) is the larger of the two by a wide margin and is what makes the "94% pantry"
badge on the chat artifact possible. Do not conflate them.

### 7.8 Discover stays unscheduled

`discover_creators/` is a full social product: creator profiles with subscriber counts, a ranked
public feed, per-recipe love/cook/remix counters, user testimonial quotes, and "Remix with AI
Sous-Chef" on someone else's recipe. Nothing in either repo is public — every recipe row is scoped
to its owner's `user_id` and every tool call validates ownership.

`BACKLOG.md` → "Deliberately not scheduled" already rules this out: *"Collaboration / creator
features. Product-plan material, not backlog material, until the single-user loop is good."* The
mockup does not change that. It is kept here as a record of where the design pointed, and the tab
bar in §7.6 lands with four tabs, not five.

### 7.9 Reading the export

`code.html` is Tailwind-CDN markup with an inline `tailwind.config` — useful for exact spacing,
radii, and which token a given element actually used. It is **not** a source to port: it is web
markup, it hardcodes Google-hosted placeholder photography, and its class strings are longer than
the React Native styles that replace them. Read `screen.png` for intent and `code.html` only to
settle a specific value.
