# AGENT.md

Technical reference for the recipe-generator-frontend — **what exists today**. Primary workspace
for changes is `recipe-generator-mobile/src/`.

> **Where this is going.** Chat is the app — BACKLOG 3.6 deleted `GenerateScreen` and
> `screens/generate/`, folding their four input modes into the chat composer and dropping the
> pre-save review step. One persistent conversation surface with inline
> recipe/plan artifact cards, backed by the agent loop in
> [`../recipe-generator/specs/AGENT_CHAT.md`](../recipe-generator/specs/AGENT_CHAT.md). The
> planner collapses to seven rows and one button, and the theme gets a real token set
> ([specs/DESIGN_SYSTEM.md](specs/DESIGN_SYSTEM.md)). Ordered work:
> [../BACKLOG.md](../BACKLOG.md).

## Tech Stack

| Layer | Library / Version |
|-------|-------------------|
| Runtime | Expo SDK 54, React Native 0.81, React 19 |
| Language | TypeScript (strict) |
| Navigation | `@react-navigation/native-stack` |
| HTTP | Axios (singleton in `apiService.ts`) |
| Auth | `expo-auth-session` PKCE + Keycloak OIDC |
| Storage | `@react-native-async-storage/async-storage` |
| Image | `expo-image-picker` |
| Clipboard | `expo-clipboard` |
| Audio | `expo-av` (`Audio.Recording`) |
| Icons | `@expo/vector-icons` (Ionicons) |
| Markdown | `react-native-markdown-display` |
| i18n | `i18n-js` v4 + `expo-localization` (English / German — BACKLOG Phase 14) |

## Local Runbook

```bash
cd recipe-generator-mobile
npm install
npm start          # Expo dev server
npm run ios        # iOS simulator
npm run android    # Android emulator / device
npm run web        # http://localhost:8081
```

Backend must be running at `http://localhost:8080/api/v1`. See `recipe-generator/CLAUDE.md`.

## Configuration Source of Truth

`recipe-generator-mobile/src/constants/index.ts` — the only place for runtime config. The app does **not** read `process.env`; there is no `.env` wiring.

| Export | Purpose |
|--------|---------|
| `API_BASE_URL` | Backend base URL (`__DEV__` switches) |
| `KEYCLOAK_CONFIG` | url, realm, clientId, redirectUri, googleIdpHint |
| `API_ENDPOINTS` | All path strings (no leading slash) |
| `STORAGE_KEYS` | AsyncStorage key names |
| `MAX_RECIPE_NAME_LENGTH` | Display truncation limit |

## File Map

```
src/
├── components/
│   ├── Loading.tsx             — full-screen modal spinner on a `scrim`. Themed since BACKLOG
│   │                             5.5; also the gate App.tsx holds the first render behind
│   │                             while the brand fonts load (5.6)
│   ├── PreferencesPanel.tsx    — the prefs editor. One caller (PreferencesScreen): preferences
│   │                             live in Profile only since BACKLOG 4.1
│   ├── RecipeView.tsx          — THE structured-recipe renderer + recipeMarkdownStyles, and
│   │                             since 17.3 the servings stepper, behind an opt-in
│   │                             `scalable` prop: on the library card, the shared recipe
│   │                             and cooking mode, deliberately NOT on RefinedPhase or the
│   │                             chat artifact card, where a stepper beside an AI's edits
│   │                             invites "did it scale, or did the model rewrite this?".
│   │                             Cooking mode owns the number (useCookingSession) so the
│   │                             overview and the step cards cannot disagree,
│   │                             since BACKLOG 5.4 the only one: RecipeCard, ChatScreen's
│   │                             artifact card, OverviewPhase (showSteps={false}) and
│   │                             RefinedPhase all go through it. Never render a recipe
│   │                             any other way
│   ├── FeedbackSheet.tsx       — the report-a-problem form (BACKLOG 9.16). Mounted ONCE in
│   │                             AppShell so the top bar and the rail open one instance.
│   │                             Works logged out; the email field appears only then, and
│   │                             is never required. Takes an optional screenshot — paste
│   │                             on web, expo-image-picker elsewhere. Also exports
│   │                             collectContext() and attachedSummary()
│   ├── FeedbackPulse.tsx       — the occasional "how is this going" card (9.16): inline at
│   │                             the top of chat, never a modal. The thumb POSTs before the
│   │                             follow-up box appears — do not reorder that
│   ├── Sidebar.tsx             — the wide-web nav rail. Fully themed; the model to copy for
│   │                             new components. Its drawer variant died with BACKLOG 5.7
│   └── ui/index.tsx            — the primitives (5.3) and the shared pills (5.8): Button, Card,
│                                 Text, Chip, Badge, Screen, Sheet + SheetRow, useIsWide() and
│                                 useIsDesktopNav(). Import from here instead of hand-rolling a
│                                 button, a pill or a colour. Chip is the tappable pill (selected
│                                 = soft accent), Badge the read-only one (accent | neutral)
├── constants/
│   ├── index.ts                — config, endpoint strings, storage keys
│   ├── mealPlanPrefs.ts        — WEEKDAYS / BATCH_DAYS_OPTIONS
│   ├── tags.ts                 — the 40-slug tag taxonomy, mirrors the Go constant, plus
│   │                             mealTypeSlug() — the card's category badge. Slugs only:
│   │                             the words are in `i18n` under `tags.slug.*`
│   └── tags.check.ts           — assert script for mealTypeSlug
├── i18n/
│   ├── index.ts                — the `i18n-js` instance, `deviceLanguage()` and
│   │                             `currentLocale()`. **Every user-facing string in the app
│   │                             lives in the catalogs — never hardcode one in a component**
│   ├── en.ts                   — the English catalog, and the shape `de.ts` must satisfy
│   ├── de.ts                   — typed `typeof en`, so a missing German key fails `tsc`
│   └── catalog.check.ts        — assert script: key parity, no empty values, matching
│                                 `%{placeholders}`, plural forms in pairs
├── context/
│   ├── AlertContext.tsx        — promise-based showAlert/confirmAction modal
│   ├── LanguageContext.tsx     — `'system' | 'en' | 'de'`, persisted under
│   │                             `STORAGE_KEYS.LANGUAGE`; gives `{locale, mode, setMode, t}`.
│   │                             Components MUST take `t` from here (a module-level `t()`
│   │                             would freeze the first language). Non-components take `t`
│   │                             as an argument, or read `currentLocale()` at call time
│   ├── MealPlanContext.tsx     — itemsByDate/recipes cache shared by the planner views
│   └── ThemeContext.tsx        — 13 colour tokens; see "Theme System" below
├── hooks/
│   ├── useCookingSession.ts    — the cooking run: phase, the current step GROUP (12.0 —
│   │                             it walks groupSteps, not steps), notes (still keyed on the
│   │                             original step index), and the one agent thread its AI
│   │                             refine, "Ask AI" and planFlow share (3.13)
│   ├── useRecipeLibrary.ts     — the saved-recipe list, its cache, and every mutation on it,
│   │                             plus the trash (8.2) and collections (8.1) that hang off it
│   ├── useEscapeBack.ts        — web: Escape key → goBack
│   ├── useVoiceInput.ts        — the three voice implementations behind one interface
│   └── useHandsFree.ts         — cooking hands-free (12.0): expo-speech reads the card
│                                 aloud everywhere; next/back/repeat by voice is web-only
│                                 (continuous SpeechRecognition), and `listening` says
│                                 which of the two you actually got
├── navigation/
│   ├── AppNavigator.tsx        — native stack; RootStackParamList
│   ├── AppShell.tsx            — layout shell above the navigator: the Sidebar rail on wide
│   │                             web, the three bottom tabs on narrow (BACKLOG 5.7)
│   └── navItems.ts             — the 5 real destinations + Profile. `route` is required, so a
│                                 route-less "Soon" entry no longer typechecks
├── screens/
│   ├── ChatScreen.tsx          — the ONLY creation surface: one agent thread (POST /chat),
│   │                             tool activity, artifact cards rendered via RecipeView
│   ├── chat/Composer.tsx       — the composer: text + photo attachments + voice, the four
│   │                             former GenerateScreen input modes as one input
│   ├── RecipesScreen.tsx       — list/search/filter/expand, the collection filter and the
│   │                             trash view; the pieces live in screens/recipes/ and
│   │                             hooks/useRecipeLibrary (BACKLOG 5.0)
│   ├── recipes/                — RecipeToolbar, RecipeCard, RecipeEditForm, RecipePanels
│   │                             (refine · variant · history), plus the styles they share
│   ├── CookingModeScreen.tsx   — phase switch only; the 6 phases live in screens/cooking/
│   │                             and hooks/useCookingSession (BACKLOG 5.0). 84 lines
│   ├── cooking/                — OverviewPhase, StepPhase (+ StepPanels), DonePhase,
│   │                             RefinedPhase, steps.ts (getStepIngredients + formatTimer,
│   │                             plus 12.0's groupSteps/phaseSummary — THE logic here, see
│   │                             steps.check.ts; 5.4 deleted the two markdown parsers that
│   │                             used to live here, hence the rename from parse.ts),
│   │                             styles.ts (chrome)
│   ├── ProfileScreen.tsx       — user info + logout; the way into Preferences and Household
│   ├── HouseholdScreen.tsx     — one shared kitchen (BACKLOG 15.1): create or join by code,
│   │                             the member list, copy/rotate the code, Leave and Disband.
│   │                             Two states in one screen; mount + focus reload, because
│   │                             somebody else joining changes it without this device acting
│   ├── PreferencesScreen.tsx   — wraps PreferencesPanel
│   ├── LoginScreen.tsx         — Keycloak login trigger
│   ├── GroceryListScreen.tsx   — the week's shopping list (BACKLOG 6.1), grouped by aisle.
│   │                             Derived from the plan on every load; ticks live in
│   │                             AsyncStorage per user+week, and a tick also writes the
│   │                             line into the pantry (7.2)
│   ├── PantryScreen.tsx        — what the user has in the house (7.1): add/remove, expiry
│   │                             badges, a "use first" group derived from the dates, and
│   │                             "find recipes using these" as a chat turn (7.4). An
│   │                             "Always in stock" toggle marks a staple (16.1) — it hides
│   │                             amount and expiry, gets its own section, and there is a
│   │                             one-tap localized starter list when none exist
│   ├── MealPlanScreen.tsx      — auth gate + MealPlanProvider; renders WeekView, the only view
│   └── mealplan/
│       ├── WeekView.tsx        — the planner, rebuilt by BACKLOG 4.7: seven DayCards, a
│       │                         caption-sized week nav, one "Plan my week" button. All AI
│       │                         work still happens in Chat (4.2)
│       ├── DayCard.tsx         — one day: coloured header carrying the day's kind, then
│       │                         title, summary, time/servings badges, Cook · Swap · ⋯.
│       │                         A leftover row has one action instead (9.11) and a cook
│       │                         whose portions miss the household says so inline (9.12).
│       │                         A cooked meal (16.3) dims, badges, and collapses to Undo
│       ├── planDays.ts         — buildWeek(): reads source_item_id to mark a meal
│       │                         cook/leftover/batch/empty, and works out its portion
│       │                         number. THE logic in the planner — see planDays.check.ts.
│       │                         `cooked` is read from the *source* item, so a batch run
│       │                         goes quiet together rather than the cook day alone (16.3)
│       ├── planDays.check.ts   — assert script for buildWeek (10 cases); run command in its header
│       ├── pickerRows.ts       — orders the picker for the slot being filled (9.9): tagged
│       │                         recipes first, the rest under "Other recipes". Ranked,
│       │                         never filtered
│       ├── pickerRows.check.ts — assert script for pickerRows (9 cases)
│       ├── TodayBanner.tsx     — the hero: "TONIGHT", the dish, badges, Start cooking. Also
│       │                         owns the local prep-time notification (see 4.6)
│       └── RecipePickerModal.tsx  — shared "choose a saved recipe" modal: search box, and
│                                 slot-aware ordering via pickerRows.ts
├── services/
│   ├── apiService.ts           — Axios client + one shared raw-fetch SSE reader (streamSSE);
│   │                             every method rejects with the exported `ApiError`
│   └── authService.ts          — OIDC PKCE, token storage, refresh
├── types/index.ts              — all shared TypeScript interfaces
└── utils/
    ├── mealPlanDates.ts        — plain-Date helpers
    ├── pantryExpiry.ts         — days-until/label maths for the pantry (7.4), with
    │                             pantryExpiry.check.ts as its assert script
    ├── recipeOrigin.ts         — "From seriouseats.com · 8 Sep" for a saved recipe (3.7)
    ├── chatArtifacts.ts        — foldArtifact(): keys recipe artifacts by draft_ref across the
    │                             whole thread, so a save updates the card instead of adding one
    │                             (chatArtifacts.check.ts is its assert script)
    ├── recipeDiff.ts           — diffRecipes(): what a transform changed (10.3). Keyed on the
    │                             normalised ingredient name, so "250 g → 125 g flour" is a
    │                             CHANGED line and only a different food is an add/remove;
    │                             steps compared by position (recipeDiff.check.ts is its
    │                             assert script). Rendered by ChatScreen's ChangeSummary,
    │                             collapsed, off the artifact's `derived_from`
    ├── recipeIngredient.ts     — fmtIngredient(): one ingredient as "250 g flour". Moved out
    │                             of components/RecipeView by 10.3 so non-rendering code and
    │                             its plain-node check script can use it. 17.3 added
    │                             scaleIngredient() + servingScale() — quantity scales,
    │                             quantity_text NEVER does ("a pinch" is not 1.5 pinches) —
    │                             and an opt-in gram hint, off by default because the version
    │                             diff renders through the same function
    │                             (recipeIngredient.check.ts is its assert script)
    ├── chatApproval.ts         — describeApproval(): what a pending write does, read off the
    │                             gated tool call's own args (10.2). Picks a catalog key rather
    │                             than writing a sentence — the summary has to exist in every
    │                             language (chatApproval.check.ts is its assert script)
    ├── feedbackPrompt.ts       — WHEN to show the pulse card (9.16). Pure, no imports, so
    │                             feedbackPrompt.check.ts runs under plain node. The numbers
    │                             are published vendor/platform defaults — the file says which
    ├── feedbackPromptStore.ts  — its AsyncStorage half. The key is deliberately NOT in
    │                             STORAGE_KEYS: clearSession() wipes those, which would make
    │                             logging out a reset button on the nag
    ├── recipesCache.ts         — saved-recipe cache
    └── recipeTime.ts           — total/prep/cook minute display logic
```

**`MealPlanContext`**: `itemsByDate: Record<YYYY-MM-DD, MealPlanItem>` + `recipes: Recipe[]`,
hydrated from AsyncStorage on mount then background-refreshed via `ensureRange(startISO,
endISO)`; `upsertItem`/`removeItem` update it immediately after a successful add/patch/
delete so every mounted view reflects the change without waiting for a refetch. This is
what makes moving between weeks instant (no loading spinner) — the view reads synchronously
from context instead of fetching on its own mount.

Known caveats: the cache keys are **global constants, not per-user** — on a shared device user B
sees user A's plan until the refetch lands (`../BACKLOG.md` 0.5). There is no TTL, so a month-old
cache renders immediately as if current. `persistItems` is called from inside a `setState`
updater, which React may invoke more than once. And `recipes` is fetched exactly once per provider
mount, so a recipe saved elsewhere in the same session is missing from the picker.

## Authentication Flow

`authService.ts` owns all auth logic.

1. `loginWithKeycloak()` — opens `expo-auth-session` browser session with `code_challenge` (PKCE). Redirect URI is `com.recipegenerator://oauth/callback`.
2. Code exchange returns `access_token`, `refresh_token`, `expires_in`. All four values (plus decoded profile) are stored in AsyncStorage under `STORAGE_KEYS.*`.
3. `getAccessToken()` checks expiry; if within threshold, calls `refreshAccessToken()` first.
4. `ApiService` request interceptor calls `getAccessToken()` before every request. Token is attached only when present (anonymous calls pass through).
5. On a `401` response, the response interceptor sets `_retry = true`, calls `refreshAccessToken()`, and replays the original request once.

Do not bypass this pattern when adding new authenticated calls.

## API Contract

`apiService.ts` wraps all backend calls. All paths are relative to `API_BASE_URL`.

| Method | Path | Body / Params | Auth | Return |
|--------|------|---------------|------|--------|
| POST | `chat` | `{conversation_id, message, language?, attachments?}` — `language` is the app's current UI language (`i18n.currentLocale()`, which starts at the device locale and follows the Profile switch), sent on every turn (BACKLOG 3.9: the only locale signal the backend has left); an attachment is an attachment is `{type:'url',url}` or `{type:'image',data}` (data URL or bare base64, inline; max 3, 12 MiB body) | optional | SSE — `token` / `tool_start` / `tool_end` / `artifact` / `done`; the thread lives server-side, the client keeps only `conversation_id`. `ChatScreen` also accepts a `{prompt}` route param and sends it as a turn on arrival — how the planner hands a day or a week over |
| POST | `chat/drafts/save` | `{conversation_id, draft_ref, tags?}` — saves a recipe the agent drafted in the thread, with no model call. Idempotent per `draft_ref` (same path as the `save_recipe` tool), `404 {code:'draft_expired'}` once the server has forgotten the draft (2h TTL, or a restart) | required | `Recipe` |
| PATCH | `update-recipe` | `{id, structured, ai_sourced?, change_prompt?}` | required | `Recipe` |
| GET | `get-recipes` | — | required | `Recipe[]` |
| POST | `add-recipe` | `{recipename, recipe, structured, origin?, history?, variant_of_recipe_id?}` — `structured` is **required** since BACKLOG 3.11; the only caller left is `acceptVariant` | required | `Recipe` |
| DELETE | `delete-recipe/:id` | — | required | `204` |
| GET | `recipes/:id` | — | required | `Recipe` |
| PUT / DELETE | `recipes/:id/vote` | `{vote: 1 \| -1}` / — | required | like / dislike, surfaced as `Recipe.my_vote` |
| POST | `transcribe` | audio `FormData` | optional | `{text}` — voice input |
| POST | `feedback` | `{kind:'bug'\|'idea'\|'pulse', message?, sentiment?, contact?, context?, screenshot?}` — the app's only **optional-auth write**: an anonymous report stores `user_id NULL` rather than 401ing. `screenshot` is a data: URL, sniffed server-side and capped at 5 MB (`413`). 8 MB body cap, its own rate limiter (BACKLOG 9.16) | optional | `204` |
| GET | `usage` | — | required | `{spent_usd, cap_usd, period_start, pct_used}` |
| GET | `recipes/:id/history` | — | required | `RecipeVersion[]` (newest first; `change_note` holds the AI prompt for `ai_edit` entries) |
| GET | `meal-plan?starts_on=&ends_on=` | `ends_on` optional (defaults to `starts_on+6d`) | required | `MealPlanWeek` — `{starts_on, ends_on, items[]}`, one recipe per day, up to 42-day range |
| POST | `meal-plan/items` | `{recipe_id, planned_on, servings?, meal_slot?}` | required | `MealPlanItem` — upserts by (day, slot); an omitted `servings` now defaults to the user's `household_size` server-side (BACKLOG 9.12) |
| POST | `meal-plan/items/:id/covers` | `{days: string[], servings?}` — the **complete** set of days this pot covers, so a shorter list is the undo | required | `MealPlanItem[]` — the cook plus its leftovers, written in one transaction (BACKLOG 9.13) |
| POST | `meal-plan/items/:id/cooked` | `{rating?}` | required | `{pantry:{applied,skipped}}` — marks the meal cooked, takes its ingredients out of the pantry in one transaction (BACKLOG 16.3). **`applied` and `skipped` must both be shown**: `skipped` names what the server refused to guess at, and an app that edits your fridge silently is one you stop trusting. `404` when already marked — re-tapping never deducts twice |
| DELETE | `meal-plan/items/:id/cooked` | — | required | `204` — undo. The meal returns to the plan and the grocery list; the pantry is **not** restored, which `WeekView` says out loud |
| DELETE | `meal-plan/items/:id` | — | required | `204` |
| POST | `meal-plan/variants/:recipe_id` | `{hint?}` | required* | `RecipeResponse & {variant_of_recipe_id}` — unsaved; persist via `add-recipe` |
| GET | `household` | — | required | `Household` — or **204** when the caller is in none, which is a normal state and renders the create/join form (BACKLOG 15.1) |
| POST | `household` | `{name}` | required | `Household` (201). `409` if already in one |
| POST | `household/join` | `{join_code}` | required | `Household`. `404` for a code that does not resolve — wrong and rotated are indistinguishable |
| POST | `household/leave` | — | required | `204`. Your recipes and pantry lines come with you; the week stays |
| POST | `household/code` | — | required | `{join_code}` — rotates it, killing the old one |
| DELETE | `household` | — | required | `204`. Disband; any member may. Recipes go back to their owners, planned days after today are dropped |
| GET | `preferences` | — | required | `UserPreferences` |
| PATCH | `preferences` | full `UserPreferences` (server overwrites, not a merge — always send the complete object) | required | `UserPreferences` |

\* `meal-plan/variants` is wired under the same "optional auth" middleware as `/chat` etc., but its handler 401s an anonymous caller (it operates on the caller's own saved recipes). The three other AI meal-plan routes (`suggest`, `chat`, `chat/stream`) were deleted by BACKLOG 3.5 — planning goes through `/chat`'s `plan_week` + `apply_week_plan` tools.

The backend provisions users just-in-time from the JWT `sub` claim; there is no separate signup endpoint.

## Key Types (`types/index.ts`)

- `Recipe` — `{id, recipename, recipe, tags?, structured?, manually_edited?, my_vote?, variant_of_recipe_id?, source_type?, source_url?, created_at?}` — persisted DB record; the last three are read-only provenance, rendered by `utils/recipeOrigin.originLabel`
- `RecipeDocument` — structured parse: `{title, summary, language, tags, servings, total_minutes, prep_minutes, cook_minutes, difficulty, ingredients[], steps[]}` (`total_minutes` is elapsed time until the food is ready to eat — not necessarily `prep_minutes + cook_minutes`; see `utils/recipeTime.ts`). A step carries `phase` (`'prep'|'cook'|'wait'`) and `parallel_group` since BACKLOG 12.0 — **both are optional and null on every recipe saved before it**, which `groupSteps` renders as the old flat one-step-per-screen walk
- `RecipeResponse` — `{recipename, recipe, structured?}` — generation endpoint response
- `GenerationOrigin` / `EditTurn` — the resent-conversation types; both disappear once the agent loop holds threads server-side
- `PatchRecipePayload` — PATCH body for manual edits
- `RecipeVersion` — `{version, change_kind, change_note?, created_at, data}` — one entry from `GET recipes/:id/history`
- `UserProfile` — `{name, email?, username?}`
- `Household` / `HouseholdMember` — `{id, name, join_code, members[]}` / `{id, name, email, is_me}`
  (BACKLOG 15.1). There are no roles and no admin, so a member is only ever who they are. The
  join code is the whole access mechanism and is rotatable — the same shape as 8.4's share token,
  with a revoke
- `Recipe.owned_by_me` / `Recipe.owner_name` — a household's shared library (15.3). A recipe is
  personal property lent to the household: everyone can cook and plan it, only the owner can
  rewrite it. **Absent means mine** — outside a household every visible recipe is the caller's,
  and an older payload must not read as somebody else's
- `MealPlanItem` — `{id, recipe_id, recipe_title, planned_on, meal_slot, servings?, source_item_id?}`; `MealPlanWeek` — `{starts_on, ends_on, items: MealPlanItem[]}`. `source_item_id` names the item whose pot this meal eats again (BACKLOG 9.11)
- `UserPreferences` — `{skill_level, dietary_prefs, disliked_ingredients, meal_plan_no_food_days: Weekday[], meal_plan_no_cook_days: Weekday[], meal_plan_batch_days: 1|2|3, week_start_day: Weekday, household_size: number|null}` — the scheduling fields feed the meal-plan AI's system prompt server-side (`no_food_days` skips the day entirely, `no_cook_days` is a "leftover day" that repeats the prior cooked day's dish). Edited in `PreferencesScreen` only — preferences are global, with no per-week scope (BACKLOG 4.4 deleted the override, and `cooking_cadence` with it). The two day lists are mutually exclusive, enforced in `PreferencesPanel` and again by `PATCH /preferences`
- `MealPlanAssignment` — one proposed day: `{recipe_id, variant, variant_of_recipe_id, planned_on, start_time}` — exactly one of `recipe_id`/`variant` is non-null
- `MealPlanSuggestion` — `{status: 'applied'|'needs_clarification', message, low_variety, assignments[]}` — the AI suggest/chat response envelope

## Chat Surface Internals

Since `../BACKLOG.md` 3.6 this is the only way into the app's core loop. `ChatScreen.tsx` (273
lines) owns the thread, the SSE turn and the artifact cards; `screens/chat/Composer.tsx` (181
lines) owns everything the user can hand over.

| Unit | Owns |
|------|------|
| `ChatScreen` | the message list, `streamChat`'s event fold, the `derived_from` → document lookup behind the change summary (10.3), `TOOL_LABELS`/`ERROR_MESSAGES`, the source cards read off `tool_start`'s args, the `{prompt}` route param the planner hands over |
| `screens/chat/Composer` | the text field, photo attachments (picker + web paste, max 3), the mic button, and the send gate |
| `hooks/useVoiceInput` | the three implementations behind `{isRecording, soundBars, toggle, abort}` |
| `components/VoiceOverlay` | the listening modal (moved out of `screens/generate/` by 3.6) |

**There is no review step and no `inputMode`.** A generated recipe arrives as an artifact card
with the agent's own "say save it to keep it" line; refinement is a later turn in the same
thread, and version history (`GET recipes/:id/history`) is what makes editing a saved recipe
safe. `inputText`'s three former meanings are one composer value.

**Photo input**: `expo-image-picker` with `base64: true` on native and web; on web a global
`paste` listener attaches a clipboard image as a data URL. Both travel inline in the `/chat` JSON
body as `{type:'image', data}` — the server decodes, sniffs and hands the model an `image_ref`.

**URL input has no affordance by design**: a link is typed or pasted as text, and
`chat_agent_system.txt` (not the client) decides whether it is a recipe to extract or a page to
read. GenerateScreen's silent regex switch to the URL endpoint died with it.

**Voice input**: three parallel implementations behind `hooks/useVoiceInput` — web
`SpeechRecognition`, web `MediaRecorder` → `/transcribe`, and native `expo-av` → `/transcribe`.
It dictates into the composer; it is not a separate mode.

**Deleted by 3.5** (2026-09-11), having been left caller-less by 3.6: `apiService`'s
`generateRecipe`/`generateRecipeStream` and the six `generateBy*` wrappers, `suggestInput`,
`suggestPrefs`, and the `GENERATE_RECIPE*`/`SUGGEST*` endpoint constants — **the client no
longer calls `/generate` at all**; recipes are created only by `/chat`'s `generate_recipe`
tool. `RecipeSkeleton` and `partialRecipeDoc` were deleted by 3.7: `/chat` emits no partial
document.

**Deleted by 3.9/3.10/3.11** (2026-09-11): `declineGeneration` and the
`DECLINE_GENERATION` constant (the whole `generation_events` funnel went with them
server-side), `saveRecipe`'s `generationId` parameter, and `Recipe.generation_id`. The
locale that 3.5 stopped sending is back, but on `/chat` rather than `/generate`. Since
Phase 14 it is read **per turn** from `src/i18n` rather than once at module load, so the
Profile language switch changes what the model writes back as well as the chrome;
`expo-localization` is still what supplies the default.

## Theme System

`src/theme/index.ts` holds the tokens since `../BACKLOG.md` 5.1 — the sage-primary /
terracotta-accent palette of `specs/DESIGN_SYSTEM.md` §7.2 with an `on*` pair for every semantic
slot, plus `space`, `radius`, `type` (sizes **and** line heights), `motion`, and `layout` (one
breakpoint, `breakpointWide: 900`, plus the `contentMaxWidth: 720` reading column). Every pair was
run through a contrast checker; the three values that failed §7.2's table and the ratios that
passed are recorded in the file header. `ThemeContext.tsx` only picks between `lightTheme` and
`darkTheme` and re-exports `Theme`, which is why screens still import both from the context.

Since BACKLOG 5.2 the choice is tri-state: `useTheme()` gives `{theme, mode, isDark, setMode}`
with `mode: 'system' | 'light' | 'dark'`, persisted under `STORAGE_KEYS.THEME_MODE` and defaulting
to `system` (resolved with `useColorScheme()`; an unknown OS scheme resolves dark). The setter is
the Appearance row in `ProfileScreen`.

**There is no colour literal left outside `src/theme/`** — BACKLOG 5.5 migrated the last 43 and
deleted the `card`/`hairline`/`placeholder` aliases; `scrim`/`onScrim` and `shadow` were added for
the three cases that were not colours-in-disguise. Keep it that way: the grep in 5.5's entry is the
gate.

Since BACKLOG 5.6 the `type` scale also carries `fontFamily` — **Epilogue** for `display`/`title`,
**Plus Jakarta Sans** for `body`/`label`/`caption`, four faces loaded from `src/theme/fonts.ts` by
`useFonts` in `App.tsx`. The weight lives in the family name, so the variants carry no
`fontWeight`; `fontFamily` must not appear outside `src/theme/`.

All `StyleSheet` calls inside screens use `useMemo(() => makeStyles(theme), [theme])` — follow
this pattern. `components/Sidebar.tsx` is the cleanest example.

The replacement token set (warm palette, semantic slots, real scales, primitives) is specified in
[specs/DESIGN_SYSTEM.md](specs/DESIGN_SYSTEM.md); the migration is `../BACKLOG.md` Phase 5.

## Web-Specific Behavior

- Layout switches to wide card (max-width ~720 px) — check `isWide` in `ChatScreen`.
- Clipboard paste attaches a photo to the composer (`chat/Composer.tsx`).
- `useEscapeBack` adds `keydown` listener for Escape → `navigation.goBack()`.

## Known Issues

- **Production `API_BASE_URL`** — points at `recipe-generator-beta.ili16.de`; confirm before any
  real release build.
- **No `.env` wiring** — all config is hard-coded in `constants/index.ts`.
- **Chat streaming is web-only** — `streamChat` needs
  `response.body.getReader()`, which RN's native `fetch` does not support. On native `streamChat`
  posts with `Accept: application/json` and replays the buffered events (BACKLOG 3.8); the turn's
  prose arrives all at once instead of token by token.
- **Streaming is still web-only** (see above), but the reader itself is now shared —
  `ApiService.streamSSE` does the 401 refresh-and-replay, cancels the reader on the way out,
  tolerates a malformed frame, and takes an `AbortSignal` (`streamChat` aborts on unmount).
- **The bottom tab bar does not hide on keyboard open** — on Android (`adjustResize`) it costs
  ~56px while typing in the composer. Marked `ponytail:` in `AppShell.tsx`.
- **No user-facing string outside `src/i18n/`** (Phase 14). A component takes `t` from
  `useLanguage()`; a pure helper takes `t` as an argument (`expiryLabel`, `groupSpokenText`,
  `summarizeVersionChange`, `originLabel`) or a `locale` (`buildWeek`) so its check script
  still runs under plain node. `src/i18n/catalog.check.ts` is the gate.
- **Every screen now goes through the `type` scale** (`../BACKLOG.md` 5.9): no `fontSize` outside
  `src/theme/` and `components/ui/` stands alone — each spreads a variant and overrides the size
  only where the design needs one. Keep it that way; 5.9's grep is the gate. The one intentional
  `fontWeight` left is `RecipeView`'s markdown `strong`.
- **Nothing links to `Profile` but the shell** — the wide rail's footer and, on narrow, the
  header-right person icon added by BACKLOG 5.7. It is the only route to logout, Appearance and
  Preferences, so do not remove both.
- **A household is read-widening, not re-ownership** (BACKLOG 15.3). `RecipeCard` hides Edit,
  Refine, Share and Delete on a recipe whose `owned_by_me` is `false`, and shows whose it is.
  Cook, plan, collect, vote and **Create variant** stay — copying is the way a non-owner is meant
  to get the version they want (15.7). Do not "fix" the missing buttons by re-enabling them: the
  server returns 404 for those writes.
- **A staple's name is the join key, so the starter list is localized.** `pantry.commonStaples`
  is a German list in German and an English one in English — the backend matches a pantry line
  to an ingredient on the normalised name alone, so an English `salt` would never satisfy a
  German recipe's `salz`. Adding a language means translating that array, not just the labels.

- **The grocery list's ticks are still per-device** and per-user (`groceryCheckedKey`), so in a
  household two people shopping from one list do not see each other's check-offs — BACKLOG 15.9.
  The list itself *is* shared; only the ticks are not.
- **The meal planner carries real data-loss bugs** — see `../BACKLOG.md` 0.4, 0.5, 0.6 and Phase 4.
- **A leftover is stored, not guessed** (BACKLOG 9.11). `MealPlanItem.source_item_id` names the
  item whose pot this meal eats again; `planDays.buildWeek` reads it. Only *leftover vs batch*
  is still derived, from `meal_plan_no_cook_days` — that is a labelling question, not a data one.
  A leftover row offers exactly one action ("Not eating this"), because every other one edits a
  plan and the food already exists.
- **`WeekView` fetches `weekStart - 1`** so a Monday whose cook was Sunday can name the day it
  came from. Do not "tidy" that back to a 7-day range.

## Editing Guidance

- When the backend adds or changes a route, update `API_ENDPOINTS` in `constants/index.ts` and the corresponding method in `apiService.ts` first.
- Preserve the token refresh interceptor in `apiService.ts` when touching `authService.ts`.
- New screens must be added to `RootStackParamList` in `AppNavigator.tsx` before they can be navigated to.
- Keep `types/index.ts` as the single source of truth for shared shapes; do not redeclare them inline.
- All API calls that require auth should use `apiService` methods — never call Axios directly from screens.
- `apiService` methods never swallow errors. They reject with `ApiError` (`status` is the HTTP
  status, or `undefined` if the request never reached the server); a call site that wants to
  degrade silently writes its own `.catch()` and says why in a comment.

## Plans and Specs

- [specs/DESIGN_SYSTEM.md](specs/DESIGN_SYSTEM.md) — target token set, primitives, and the
  literal-migration rule. **§7 covers the Stitch mockups** below: the palette that supersedes §2,
  the shared component inventory, and the honest list of what the mockups render that has no data
  behind it.
- [specs/stitch_smart_meal_and_recipe_hub/](specs/stitch_smart_meal_and_recipe_hub/) — five screen
  mockups (Chat · Meal Plan · My Recipes · Pantry · Discover), each a `screen.png` plus a
  Tailwind-CDN `code.html`, with the token frontmatter in `savor_culinary_ai/DESIGN.md`. Visual
  direction, not a contract — read `screen.png` for intent, `code.html` only to settle a value.
- [`../recipe-generator/specs/AGENT_CHAT.md`](../recipe-generator/specs/AGENT_CHAT.md) — the
  backend agent loop this client will talk to.
- [../BACKLOG.md](../BACKLOG.md) — ordered implementation work, Phase 0 → 6.
- [../PRODUCT_PLAN.md](../PRODUCT_PLAN.md) — product view: use cases and where they are headed.
