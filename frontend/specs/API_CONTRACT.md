# Frontend API Contract

Moved out of AGENT.md so it is not loaded every session. Read it before changing `apiService.ts` or a call site.

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

