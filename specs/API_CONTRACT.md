# API Contract (Current)

Moved out of AGENT.md so it is not loaded every session. Read it before changing or calling an endpoint.

Base prefix: /api/v1

Public:
- GET /health

LLM-calling — optional auth (identified if a Bearer token is present, anonymous
otherwise), rate-limited and budget-capped per the "Rate limiting + budget" note above:
- POST /transcribe
- POST /chat
	- The agent loop (specs/AGENT_CHAT.md §2). It is the **only** LLM entry point for
	  recipe work: `/refine-recipe` and `/cooking-chat` went in BACKLOG 3.13 (refining is
	  the `transform_recipe` tool, a cooking question is `answer_cooking_question`), and
	  `/generate` + `/generate/stream` went in 3.9 once nothing called them.
	- JSON: `{"conversation_id":"<uuid>"|omitted,"message":"...","language":"de",`
	  `"attachments":[…]}` → `text/event-stream`.
	- **Or** `{"conversation_id":"…","approval":{"id":"…","approve":true}}` with **no
	  message** — the answer to an `approval_request` from the previous turn (BACKLOG
	  10.2). See the approval note below.
	- `language` is optional (ISO 639-1, the caller's device locale): it is appended to the
	  **system prompt for that turn only**, never stored on the thread, so a thread resumed
	  on another device follows that device. It is the only locale signal the backend has
	  left — the model uses it when the message is too short to detect a language from
	  ("cacio e pepe"), and passes it to `generate_recipe`'s `language` arg.
	- An attachment is either `{"type":"url","url":"…"}` (folded
	  into the message as `[attached link] <url>`; the model decides whether that is a
	  recipe to extract or a page to read) or `{"type":"image","data":"<data: URL or bare
	  base64>"}` — decoded at the handler, refused unless the bytes sniff as an image,
	  parked on the turn's session and announced as `[attached photo] image_ref=imageN`
	  for `generate_recipe`'s `image_ref`. Images are **turn-scoped**: a later turn works
	  from the `draft_ref` the photo produced, not the pixels. Body capped at 12 MiB
	  (base64 inflates by ~4/3) and 3 images per turn. The client sends **one message**, not a transcript: the
	  thread lives server-side (`conversations` / `conversation_messages`, migration
	  00017) for signed-in callers and in-memory with a 2h TTL, keyed by IP like the rate
	  limiter, for anonymous ones. Omit `conversation_id` to start a thread; the id comes
	  back on `event: done`.
	- **Transport is picked from `Accept`**: `application/json` returns one buffered
	  `{"events":[{"type":…,"payload":…},…]}` body when the turn ends — the same events in
	  the same order, for React Native, whose `fetch` has no readable body (BACKLOG 3.8).
	  Anything else streams SSE. The agent loop is identical either way.
	- Events: `token` `{text}` (assistant prose, streamed) · `tool_start` `{name,args}` ·
	  `tool_end` `{name,ok}` · `artifact` `{kind:"recipe"|"week_plan",data}` (the card the
	  client renders inline, and the reason the reply itself does not repeat a recipe) ·
	  `approval_request` `{id,name,args,replaces}` · `done` `{conversation_id}` · `error`
	  `{code}` — a code, never `err.Error()`.
	  Error codes: `conversation_not_found`, `budget_exceeded`, `iteration_limit`,
	  `timeout`, `model_error`, `stopped`, `approval_expired`. `stopped` is the user pressing Stop (BACKLOG
	  10.1): the request context is cancelled, the loop halts **before** the next model
	  call or tool call rather than after it, and whatever the turn produced so far is
	  still persisted. The client has hung up by then, so the code is for the log and the
	  funnel counter (`outcome="abandoned"`), not for anyone to read.
	- A draft carries the provenance of the ask that produced it: `toolGenerateRecipe` parks
	  a `GenerationOrigin` on `draftInfo` (it is the only place that still knows whether the
	  user gave a URL, a photo or a description), `transform_recipe` inherits it from its
	  target draft, and `saveDraft` writes it as the recipe's `sources` row.
	- A recipe artifact born of a **transform** (`transform_recipe`, `review_cook_flow`)
	  carries `derived_from`: the `draft_ref` or `recipe:<id>` it was made out of (BACKLOG
	  10.3). Those tools park their result as a *new* draft, so without it the client has a
	  new card and no way to know which of its existing ones it replaced. It is a string,
	  never a document or a diff — the client already holds the before-card and does the
	  comparison itself. A fresh `generate_recipe` omits the field entirely.
	- `save_recipe` re-emits the draft's `artifact` carrying its new `recipe_id`, so the
	  client's card flips from "unsaved draft" to saved. The client keys recipe artifacts on
	  `draft_ref` across the whole thread, so this updates the existing card rather than
	  appending a second copy of the same recipe.
	- `save_recipe` checks for a look-alike first (BACKLOG 8.3): a live recipe of the
	  caller's with the same source URL, or failing that the same normalised title, comes
	  back as `{saved:false, duplicate_of:{recipe_id,title,matched_on}}` and **nothing is
	  written** — the model offers "a variant of that one" or "a separate recipe" and calls
	  again with `confirm:true` (plus `variant_of:<id>` for the variant). A save is
	  therefore deferred, never refused. The lookup is advisory: `storage.FindDuplicate`
	  caps itself at 500 ms and treats any error or timeout as "no duplicate". Only this
	  tool checks — `apply_week_plan` saves drafts through the same `saveDraft` and must
	  not stop a week to ask.
	- `get_pantry` and `mark_meal_cooked` (BACKLOG 16.3) are the first pantry tools in the
	  catalog. `mark_meal_cooked` is `Mutating`: it empties part of the user's fridge, so its
	  `approvalPreview` runs the **real** deduction in a rolled-back transaction rather than a
	  second implementation of the same arithmetic — a preview computed by different code than
	  the action is a lie waiting to happen. Only a *cook* can be marked; a leftover ate a pot
	  that was already deducted.
	- `search_my_recipes` filters on title text, tags, **and `ingredients`** — a recipe
	  matches if it uses at least one of the named ones, and each hit says which
	  (`uses`). That last filter is what "find recipes using these" on the Pantry screen
	  becomes (BACKLOG 7.4); it is a second indexed lookup because the list payload
	  carries no ingredient names.
	- **Households (BACKLOG 15.1) widen the tools for free.** Every tool resolves its user id
	  from `auth.FromContext` and hands it to a storage call, and the scope widening lives in the
	  SQL — so inside a household the whole catalog already reads the shared library, pantry and
	  week with no per-tool change. Two things the loop adds on top: a line in the system prompt
	  naming the household and its size (and that another member's recipe can be cooked and
	  planned but not edited), and `plan_week`'s context rendering the **union of every member's**
	  dislikes and dietary prefs — a plan nobody can eat is worse than no plan. Writes still
	  record the caller as owner.
	- Tool catalog in `internal/service/chat_tools.go`. Two invariants: the user id comes
	  from `auth.FromContext`, never from tool args (a recipe id in args only ever reaches
	  a storage call already scoped to that user, so another user's id is not-found); and
	  every tool returns JSON, never prose. Tools that touch durable per-user state refuse
	  anonymous callers; `generate_recipe`, `fetch_url`, `answer_cooking_question` and
	  `review_cook_flow` do not, so the anonymous acquisition funnel still works.
	- `review_cook_flow` (BACKLOG 12.0) re-plans how a recipe is cooked and nothing else:
	  it merges steps too thin to be worth a screen, labels each `prep`/`cook`/`wait`, and
	  marks the ones that run at the same time (`recipe_steps.phase` / `parallel_group`,
	  migration `00032`). It is a steps-only rewrite over the full recipe schema, so
	  `service.ReviewCookFlow` **verifies** the ingredients came back unchanged
	  (`sameIngredients`) and errors if they moved — a flow review that quietly rewrote the
	  shopping list would be a much worse bug than a bad flow. Like every other proposal it
	  returns a draft; the user saves it.
	- `plan_week` only proposes. Applying is **one** `apply_week_plan` call listing every
	  day (`storage.ApplyMealPlanDays` — one transaction, all days or none), which is also
	  the only apply path since the planner UI lost its Accept button (`../BACKLOG.md`
	  4.2/4.3). A proposed day that is a new *variant* recipe exists nowhere yet, so
	  `plan_week` parks each variant as a draft and returns `variant_drafts` (date →
	  `draft_ref`) for `apply_week_plan` to name. Three properties the per-day loop
	  lacked: it is atomic; it is idempotent (saving a draft twice returns the first
	  recipe, and days upsert), so re-applying duplicates nothing; and a day already
	  holding a *different* recipe comes back as
	  `{applied:false, confirm_required:true, would_overwrite:[…]}` instead of being
	  silently replaced — the model shows that list and calls again with `confirm:true`.
	  `set_plan_day` remains for a single day the user named explicitly (same code path,
	  pre-confirmed). Saved variants carry `variant_of_recipe_id` back to their parent.
	- **Approval is structural, not prosaic** (BACKLOG 10.2). A tool marked
	  `Mutating` in the catalog does not run when the model asks for it: the loop emits
	  `approval_request` and **ends the turn**, and the call runs only when the client
	  posts back `{"approval":{"id","approve"}}`. Gated on *scope*, not on "does it
	  write" — `apply_week_plan` (multi-day), `clear_plan_day` (destructive) and
	  `set_preferences` (changes every later turn) ask; `save_recipe` and `set_plan_day`
	  write silently, because a single row the user named in their own message is its own
	  confirmation and asking twice is noise.
	  - `replaces` on the event is what those days already hold
	    (`approvalPreview`, read-only — it must not resolve drafts, since saving one is
	    itself a write). Empty means the call replaces nothing.
	  - The parked assistant message is **not** appended to the thread while it waits: a
	    stored reply whose `tool_calls` have no tool results is a thread the model API
	    rejects. Approving appends the reply and one tool result per call together, so the
	    thread is only ever valid; never resuming simply drops the reply.
	  - Only the approved call runs. Calls after it in the same reply come back `not run`
	    — the model wrote them expecting the gated write to have happened.
	  - Approving an `apply_week_plan` forces its own `confirm` flag true
	    (`approvedArgs`): that flag exists to make the *model* ask before replacing a
	    planned day, which the approval just did, in a form the user actually saw.
	  - Declining returns `{"approved":false}` to the model and writes nothing.
	  - The pending call lives on the in-memory session (one per thread, same TTL and
	    single-process caveat as drafts), so a restart answers `approval_expired`.
	  - Anonymous callers are never gated: every mutating tool is `NeedsAuth`, so there
	    is nothing to approve.
	- Bounded three ways: `maxTurnIterations` (6) model calls per turn, a 4-minute
	  wall-clock deadline off `r.Context()`, and a budget re-check **between** iterations
	  — an agent turn is N model calls, so a runaway loop trips the cap mid-turn rather
	  than after the whole turn has been paid for. One `llm_usage` row is written per
	  model call in the turn, not per request.
- POST /meal-plan/variants/{recipe_id}
	- optional JSON `{"hint":"make it Thai-style"}` → 200
	  `{"recipename":"...","recipe":"...","structured":<RecipeDocument>,"variant_of_recipe_id":7}`
	  — a new, **unsaved** recipe proposed as a twist on `recipe_id` (which must belong to
	  the caller and have structured data). Persist it via `POST /add-recipe` with
	  `variant_of_recipe_id` set only if the caller accepts it. 404 if the recipe isn't
	  found/owned; 422 if it has no structured data to vary.

Authenticated (Bearer token required):
- POST /chat/drafts/save
	- `{"conversation_id":"…","draft_ref":"draft_1","tags":[…]}` → the saved `Recipe` (201).
	  The client's "Save to my recipes" button on a chat recipe card: it resolves the draft
	  out of the conversation's warm session and saves it through the **same** `saveDraft`
	  the `save_recipe` tool calls, so a second tap — or a later "save it" — returns the
	  recipe already written rather than a duplicate. It calls no model, which is why it
	  sits here and not behind the LLM stack; unlike `/chat` it refuses anonymous callers,
	  because it writes. A draft the session no longer holds (2h TTL, or a restart) is
	  `404 {"code":"draft_expired"}` — its own answer, so the client can fall back to asking
	  the assistant instead of offering a button that cannot work.
- GET /user-info
- GET /get-recipes
	- Each recipe carries its provenance read-only: `source_type` (`recipes.source_type`),
	  `source_url` (its first `sources` row, URL saves only) and `created_at` — what the
	  client renders as "From seriouseats.com · 8 Sep" (BACKLOG 3.7). `source_type` is NOT
	  NULL with a `'text'` default, so a bare `'text'` means "nothing was recorded" as often
	  as it means "typed", and the client shows nothing for it.
	- `pantry_have` / `pantry_total` are the pantry match (BACKLOG 7.3): how many of the
	  recipe's ingredients the caller already has, out of how many it lists. Matched on the
	  normalised ingredient **name** only — comparing amounts would report a miss on
	  "250 g flour" vs "2 cups flour", the very case the match exists for. Both 0 means
	  "show no badge", not 0%.
- POST /add-recipe
	- JSON: `{"recipename":"...","recipe":"...","structured":<RecipeDocument>,`
	  `"origin":{...},"history":[{"change_prompt":"","structured":<RecipeDocument>}, ...],`
	  `"variant_of_recipe_id":7}`
	- **`structured` is required** (400 without it). The untyped branch that wrote a
	  markdown-only recipe with no `RecipeDocument` — the only thing that could create a
	  row cooking mode cannot cook — is gone with `classifyTags`, its only tagger
	  (BACKLOG 3.11). The one remaining client is the meal-plan variant accept path;
	  everything else saves server-side through the agent's `save_recipe` tool.
	- `variant_of_recipe_id` is optional — set when persisting a `POST
	  /meal-plan/variants/{id}` result the caller accepted; stored on `recipes` for
	  grouping, not otherwise surfaced yet.
	- `origin`/`history` are optional. When present (structured saves only), `history[0]`
	  is the initial generation (`change_prompt: ""`); each entry becomes a
	  `recipe_versions` row (`change_kind` `'extraction'` then `'ai_edit'`, `change_note`
	  = the instruction) and a matching `recipe_timeline_events` row, and `origin` is
	  recorded as a `sources` row. `current_version` ends up `len(history)` — so
	  `current_version == 1` means "saved the first generation unedited."
	- 201 with the created recipe
- DELETE /delete-recipe/{id}
	- 204 on success. **Soft** since BACKLOG 8.2: it stamps `recipes.deleted_at`, and every
	  read path filters `deleted_at IS NULL` — except `GetMealPlanRange`'s join, deliberately,
	  so a week that already references the recipe still renders. `internal/worker` hard-deletes
	  rows trashed longer than 30 days (`storage.PurgeDeletedRecipes`), and the FK cascades take
	  the ingredients, steps, versions, timeline and meal-plan items with them.
- GET /recipes/trash
	- 200 with the caller's soft-deleted recipes (id, title, content, `deleted_at`), newest
	  deletion first. Deliberately thinner than `/get-recipes` — nothing in the trash can be
	  cooked, voted on or refined.
- POST /recipes/{id}/restore
	- 204, clearing `deleted_at`. 404 when the id isn't a *trashed* recipe of the caller's —
	  another user's id and an already-restored one are indistinguishable on purpose. The
	  recipe comes back intact, version history included, and reappears in its collections.
- GET /collections
	- 200 with `[{"id":1,"name":"Sunday","recipe_ids":[7,3]}, ...]`, by name. `recipe_ids` is
	  the full membership, so the client filters its already-loaded library — there is
	  deliberately **no** per-collection list endpoint. Soft-deleted recipes are left out of
	  `recipe_ids` but keep their membership row, so restoring puts them back.
- POST /collections
	- JSON: `{"name":"Sunday"}` → 201 with the collection. The name is trimmed, and
	  re-creating an existing name returns *that* collection (upsert on `(user_id, name)`) —
	  not a duplicate and not an error. A blank name is 400. There is no rename endpoint yet.
- DELETE /collections/{id}
	- 204. Drops the collection and its membership rows; the recipes are untouched
	  (`recipe_collections` cascades, `recipes` does not). 404 if not the caller's.
- PUT | DELETE /collections/{id}/recipes/{recipeId}
	- 204. Adds/removes one recipe; a recipe may sit in several collections. Adding twice is
	  a no-op. 404 when either id isn't the caller's, or when removing a non-member.
- GET /pantry
	- 200 with `[{"id":1,"name":"flour","quantity":1500,"unit":"g","category":"spices_dry",`
	  `"expires_on":null,"staple":false}, ...]` — what the caller has in the house (BACKLOG 7.1), expiring
	  items first and undated ones last. `name` and `unit` come back **normalised**
	  (`model.NormalizeItem` / `NormalizeUnit`: lowercased, kg folded into g and l into ml,
	  the quantity scaled to match), which is the same key the grocery list consolidates on
	  and the only reason 7.3's match works at all.
	- `staple` (BACKLOG 16.1) marks a thing the kitchen always has — salt, pepper, oil. It
	  stays an ordinary row, so the pantry match still counts it (that is the point of
	  marking one); what it stops is the grocery list shopping for it and a cooked meal
	  deducting it. A staple normally carries no quantity and no expiry.
- POST /pantry
	- JSON: `{"name":"Flour","quantity":1.5,"unit":"kg","category":"fridge",`
	  `"expires_on":"2026-10-01"}` → 200 with the saved item. An **upsert** on
	  `(user_id, normalised name, unit family)`: re-adding something you already have
	  updates its quantity, category and expiry rather than creating a second line, so
	  there is no PATCH. `category` is one of `fridge|freezer|produce|spices_dry` and
	  anything else silently becomes `spices_dry` (same reasoning as an unknown grocery
	  aisle becoming "other"). A blank name or an `expires_on` that isn't `YYYY-MM-DD` is
	  400. `staple` is optional and defaults false; it is a flag on the row, not part of
	  its identity, so the upsert key is unchanged.
- DELETE /pantry/{id}
	- 204. 404 when the id isn't the caller's — indistinguishable from a missing one.
- PATCH /update-recipe
	- JSON: `{"id":1,"structured":<RecipeDocument>,"ai_sourced":false,"change_prompt":""}` —
	  overwrites the recipe's header, ingredients, steps, and tags with `structured` (tags
	  are always fully replaced, no more "empty tags = unchanged"). `ai_sourced` defaults to
	  `false`; when `false` the recipe is flagged `manually_edited = true`, when `true`
	  (persisting an applied agent refine) the flag is cleared to `false`. Every
	  call also appends one `recipe_versions` snapshot (`change_kind` `'manual'` or
	  `'ai_edit'` to match) and a matching `recipe_timeline_events` row, bumping
	  `current_version` — so a saved recipe's edit trail keeps growing after the initial
	  save, not just during it. `change_prompt` is optional and becomes that version's
	  `change_note`; it's the natural way to persist the AI instruction behind an
	  `ai_sourced` update.
- GET /recipes/{id}
	- 200 with the full recipe (header + structured document). 404 if not owned by the caller.
- PUT /recipes/{id}/vote
	- JSON: `{"vote": 1 | -1}` → the caller's like/dislike on their own recipe
	  (`recipe_votes`, migration `00011`). Surfaced back as `Recipe.my_vote`. Intended as
	  recommendation signal; nothing consumes it yet.
- DELETE /recipes/{id}/vote
	- Clears the caller's vote.
- GET /recipes/{id}/history
	- 200 with `[<RecipeVersion>, ...]`, newest first:
	  `{"version":2,"change_kind":"ai_edit","change_note":"make it vegan","created_at":"...",`
	  `"data":<RecipeDocument>}`. `change_note` is omitted for `"manual"`/`"extraction"`
	  entries with no note. A legacy recipe that predates structured saves returns `[]`
	  (nothing can create one any more — BACKLOG 3.11). 404 if the recipe doesn't exist or
	  isn't owned by the caller.
- GET /usage
	- 200 with `{"spent_usd":...,"cap_usd":...,"period_start":"2026-09-01T00:00:00Z","pct_used":...}`
	  — the caller's own current-calendar-month LLM spend and effective budget cap (their
	  `monthly_budget_usd_override` if set, else `LLM_BUDGET_DEFAULT_USD`). Poll this to
	  show an 80%/100% warning before the caller hits the `budget_exceeded` 429 below.
- GET /meal-plan?starts_on=YYYY-MM-DD&ends_on=YYYY-MM-DD
	- `starts_on` required; `ends_on` optional, defaults to `starts_on+6d` (a week). Range
	  capped at 42 days; 400 if missing/unparseable/out of range.
	- 200 with `{"starts_on":"...","ends_on":"...","items":[{"id":...,"recipe_id":...,`
	  `"recipe_title":"...","planned_on":"...","meal_slot":"dinner","servings":4,`
	  `"source_item_id":null,"cooked_at":null},`
	  `...]}` — every item planned in that range, **up to one per (day, `meal_slot`)**,
	  ordered by day then by the order a day is eaten (BACKLOG 6.4). `source_item_id` names
	  the item whose pot this meal eats again (BACKLOG 9.11); null means it is cooked that
	  day. It is what makes a leftover a stored fact rather than a client-side guess from
	  "yesterday held the same recipe_id" — which was wrong whenever a dish was deliberately
	  cooked twice. `cooked_at` (BACKLOG 16.3) means this pot has been eaten — it has left the
	  grocery list and the pantry has already been deducted for it; a leftover reads its own
	  done state from the cook it came from. Used by all three calendar views: Week
	  passes a 7-day range, Day a single day, Month the full displayed grid (up to 42
	  days).
- POST /meal-plan/items
	- JSON: `{"recipe_id":7,"planned_on":"2026-09-09","servings":4,`
	  `"meal_slot":"lunch"}` → 201 with the created item (same shape as one entry in
	  `GET /meal-plan`'s `items`). `meal_slot` optional, defaults to `dinner`, 400 if it isn't one of
	  `breakfast|lunch|dinner|snack`. 404 if `recipe_id` doesn't belong to the caller.
	  Upserts by **(day, slot)** — replaces whatever was in that slot and leaves the
	  day's other meals alone (BACKLOG 6.4), so reassigning a filled slot needs no
	  separate delete. Creates that week's `meal_plans` row implicitly on first use.
	  Omitting `servings` no longer means "as the recipe is written": a newly planned day
	  starts from the caller's `household_size` when they have set one (BACKLOG 9.12).
	  That default lives in `storage.ApplyMealPlanDays`, so every path that plans a day —
	  this endpoint, the agent's `set_plan_day`/`apply_week_plan` — gets it.
- DELETE /meal-plan/items/{id}
	- 204 on success, 404 if the item doesn't exist or isn't owned by the caller. Deleting a
	  *cook* deletes every leftover of it too (`meal_plan_items.source_item_id` is
	  `ON DELETE CASCADE`) — you did not cook it, so nothing eats it (BACKLOG 9.11).
- POST /meal-plan/items/{id}/cooked
	- Optional JSON `{"rating":4}` → 200 `{"pantry":{"applied":[…],"skipped":["onion"]}}`.
	  Records that this planned meal was cooked and eaten (BACKLOG 16.3): it stamps
	  `meal_plan_items.cooked_at` **and** `recipes.last_cooked_at`, and deducts what it used
	  from the pantry — one transaction, so a fridge emptied for a meal that did not get
	  marked cannot happen. `rating` rides along exactly as `PUT /recipes/{id}/cooked` does.
	- **The response is the point.** `applied` is what changed (`{"name","used","unit",`
	  `"remaining"}`, or `"removed":true` when the line is used up) and `skipped` names the
	  ingredients the server would **not** guess at: no known conversion on either side, or
	  a pantry row with no quantity ("I have flour" does not say how much). Staples are never
	  touched. The client shows both — silently editing someone's fridge is the failure mode.
	- 404 when the item isn't the caller's **or is already marked**: claiming `cooked_at IS
	  NULL` is the idempotency guard, so re-tapping never deducts twice. 400 for a rating
	  outside 1–5.
- DELETE /meal-plan/items/{id}/cooked
	- 204. Undo: the meal goes back on the week and back onto the grocery list. It
	  deliberately does **not** restore the pantry — the rows were updated in place, not
	  logged, and inventing a deduction log for an undo nobody asked for is a worse trade
	  than the user retyping a line. The client says so.
- POST /meal-plan/items/{id}/covers
	- JSON: `{"days":["2026-09-21","2026-09-22"],"servings":12}` → 200 with the cooking item
	  plus its leftovers. Makes one cooking day cover a set of later days in one transaction
	  (BACKLOG 9.13): each day in `days` becomes a row in the cook's own meal slot with
	  `source_item_id` set, and whatever was in that slot is replaced. **`days` is the
	  complete set**, so the same call is the undo — a day dropped from the list has its
	  leftover cleared, and `[]` takes it back to an ordinary single-day cook.
	  `servings` is optional and scales the cooking day (the caller sends
	  `household_size × (1 + len(days))`); it is the only place a leftover's quantity is
	  ever edited, on the day that cooks. 404 if the item isn't the caller's; 400 if it is
	  itself a leftover, or if a covered day is not after the cooking day.
- GET /household
	- 200 with `{"id","name","join_code","members":[{"id","name","email","is_me"}]}`, or **204**
	  when the caller is in no household — that is a normal state, not an error, and the client
	  renders a different screen for it (BACKLOG 15.1).
- POST /household
	- `{"name":"The Flat"}` → 201 with the household. Moves everything the caller already owns
	  into it. 409 if they are already in one: leave first, so the exit rules run once and
	  visibly.
- POST /household/join
	- `{"join_code":"ABCD2345"}` → 200 with the household. 404 for a code that does not resolve —
	  a wrong code and a rotated one are deliberately indistinguishable. 409 if already in one.
	  The code is upper-cased and trimmed before matching; it gets read aloud, so case is not
	  part of the credential.
- POST /household/leave
	- 204. The caller's recipes and pantry lines revert to being private; the **week stays with
	  the household**, and the plan rows they created are handed to the longest-standing
	  remaining member — otherwise the week would survive as nobody's. The last member leaving
	  **is** a disband.
- DELETE /household
	- 204. Disband, for everyone. **Any member may**: giving one person the power to hold the
	  others in would be worse than giving each of them the power to end it. Recipes and pantry
	  items revert to the `user_id` they never stopped carrying; planned days **after today** are
	  deleted and earlier ones kept.
- POST /household/code
	- 200 `{"join_code":"…"}`. Rotates it, killing the old one. The only way to revoke access
	  after somebody leaves, and the reason the code is a separate column from the household's id
	  rather than being it.
- GET /preferences
	- 200 with `UserPreferences`: `{"skill_level":...,"dietary_prefs":[...],`
	  `"disliked_ingredients":[...],"meal_plan_no_food_days":[...],`
	  `"meal_plan_no_cook_days":[...],"meal_plan_batch_days":1,"week_start_day":"monday",`
	  `"household_size":null}`. `household_size` is how many people the caller cooks for
	  (BACKLOG 9.12) — **null means they never said**, which is not 1: with no number there
	  is nothing to compare a recipe's yield against, so nothing claims a mismatch.
	  **Inside a household `household_size` and `week_start_day` are read from and written to
	  the household's creator** (`storage.kitchenOwner`, BACKLOG 15.1): they describe the table
	  rather than the cook, and left per-caller two members would disagree about which day the
	  same week starts on. Every other field stays personal.
- PATCH /preferences
	- JSON: full `UserPreferences` (server overwrites, not a merge — always send the
	  complete object). `meal_plan_no_food_days`/`meal_plan_no_cook_days`/
	  `week_start_day` entries validated against `weekdayNames`; `meal_plan_batch_days`
	  must be 1/2/3; `household_size` must be null or 1–20; a day may not appear in **both** `meal_plan_no_food_days` and
	  `meal_plan_no_cook_days` (400 — the two are contradictory, see BACKLOG 4.4).
	  → 200 with the saved object.

Admin (Bearer token required + `admin` realm role — `internal/auth.RequireAdmin`, 403 for
non-admins):
- GET /admin/users
	- 200 with `[{"id":...,"email":"...","name":"...","spent_usd":...,"cap_usd":...,`
	  `"override_usd":...,"last_seen_at":"..."}, ...]` for every user, most-spent first.
	  `override_usd` is omitted when the user has no override (effective cap is the default).
- PATCH /admin/users/{id}/budget
	- JSON: `{"monthly_budget_usd": 10.0}` (or `null` to clear the override and fall back
	  to the default cap) → 204. 404 if the user doesn't exist.

Notes:
- LLM-calling endpoints return `429` with `{"error":"rate_limited"}` (request cap hit —
  `Retry-After` header included) or `{"error":"budget_exceeded"}` (authenticated caller
  hit their monthly cap) — distinct `error` values so the frontend can tell them apart.
- The historical "service" payload key is gone; the field is "recipe" everywhere.
- Refining a saved recipe (cooking notes, or Refine in the recipes list) is now a `/chat`
  turn naming the recipe id: the agent's `transform_recipe` proposes, and the client
  persists with `PATCH /update-recipe` (`ai_sourced: true`, `change_prompt` set) — so the
  edit trail comes from `recipe_versions`, not from a resent `history` array.
- Ownership always comes from the token subject, never from the request body.

