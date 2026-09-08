# Backend Refactor Plan (Go API)

Date: 2026-08-16 — updated 2026-08-21
Scope: recipe-generator backend implementation details

Related plans:
- ../REFACTOR_PLAN_API_AUTH_CRUD.md
- ../recipe-generator-frontend/REFACTOR_PLAN_FRONTEND.md

---

## ✅ Done (2026-08-21)

- Removed Azure and MinIO from code, go.mod, and runtime entirely.
- `subdomain` column dropped; clean schema from scratch (no legacy DB).
- `internal/auth` package: JWKS validation, issuer/audience checks, typed context, introspection feature-flag.
- `storage.Users.EnsureUser` just-in-time upsert keyed on OIDC `sub` claim.
- Replaced `RequireAuth` + `LoginMiddleware` with `authenticator.Require`; removed `model.UserContext` / `model.AuthContext`.
- Clean initial migrations `00001`–`00004`: users, recipes + ingredients/steps/sources, versions/notes/timeline, reference tables + planning.
- `DATA_MODEL.md` written.
- Contract drift fixed: `DELETE /delete-recipe/{id}` path param, `recipe` JSON key, `add-recipe` returns 201 + created recipe.
- `PORT` env var; startup warning when `CORS_ORIGIN` is unset.
- Debug logging with timing for every request, LLM call, DB query, and auth step.
- MIME detection for uploaded images (`http.DetectContentType` replaces hardcoded `image/jpeg`).
- Weekly meal planning MVP: `GET /api/v1/meal-plan`, `POST /api/v1/meal-plan/items`,
  `DELETE /api/v1/meal-plan/items/{id}` (`internal/storage/mealplan.go`, `cmd/mealplan.go`) —
  one recipe per day, `meal_plans` row created implicitly on first write.
- (2026-09-08) Meal planner v2 — AI-first: `POST /meal-plan/suggest` composes a plan from
  the caller's saved recipes (LLM-calling, structured JSON like `/refine-recipe`);
  `POST /meal-plan/chat` refines a proposal conversationally (same stateless,
  client-resends-history pattern); `POST /meal-plan/variants/{id}` proposes a new,
  unsaved recipe as a twist on an existing one (`recipes.variant_of_recipe_id`,
  migration `00014`) for when saved-recipe variety is low (<10). Items now carry
  `start_time` (migration `00013`) and `PATCH /meal-plan/items/{id}` moves one — the Day
  view's drag interaction. `GET /meal-plan` generalized to an arbitrary date range
  (`ends_on` optional, defaults to a week) so Day/Week/Month share one endpoint.
  `AddMealPlanItem` now upserts by day. The "recommendation query" (ranking saved
  recipes by cook history for a non-AI suggestion) stays unscheduled (see Backlog) — the
  AI suggest flow above supersedes the immediate need for it.

---

## 🔨 Active — Next Up

### Structured write path (extraction → DB rows)

The schema is ready; the write path still persists raw markdown into `recipes.content`.

Steps:
1. Change the LLM prompt to emit a strict JSON document:
   `{title, servings, language, ingredients:[{section,item,quantity,quantity_text,unit,optional}], steps:[{section,step_text,timer_seconds,temperature_c}]}`
2. Unmarshal and validate the response; fall back to content-only if parsing fails.
3. Persist in one transaction: `recipes` row + `recipe_ingredients` rows + `recipe_steps` rows + `recipe_versions` snapshot (`change_kind='extraction'`) + `sources` row.
4. Render `content` from the structured rows on write so the current mobile client keeps working.
5. Expose `GET /api/v1/recipes/:id` returning the full structured recipe.

All three extract modes (text/url/image) must go through this path. Same output schema, same persistence logic.

### Voice extraction route

- `POST /api/v1/generate` already handles multipart; add `voice` field.
- Transcribe with Whisper → feed transcript into the text extraction path above.
- Return the same `{recipename, recipe}` envelope.
- Frontend already has the recording UI; no backend-specific voice contract needed.

---

## 🌟 Prime Features (planned, not yet scheduled)

### Cookbook sharing
- `GET /api/v1/cookbook/static` renders a deterministic HTML/markdown view of a user's recipes at a fixed version.
- Share link generated from `recipes.public_id`.

### AI diff editor
- `POST /api/v1/recipes/:id/ai-edit` accepts a step/ingredient context + instruction.
- Returns proposed diff (not a direct mutation).
- On user accept: write new `recipe_versions` row + apply to live tables + append `recipe_timeline_events`.

---

## 🗂 Backlog (future)

- Meal plan recommendation query ("what to eat today") — needs cooked/like/dislike signal first.
- Creator/blog follows and feed candidates.
- Notes and timeline read/write endpoints.
- Techniques and flavor profiles wired to extraction output.
- `GET /recipes?page=&limit=` pagination.
- Idempotent delete (second DELETE returns 204, not 404).
- Auth test matrix: valid/expired/wrong-audience/missing-claims.
- Request correlation ID middleware (`X-Request-ID`).
- Structured error envelope `{"error":{"code","message"}}`.
- Ingredient scaling endpoint (multiply quantities server-side).

---

## ❌ Dropped

- OAS / OpenAPI spec — not worth the maintenance overhead at this stage.
- Verifier LLM pass — over-engineered; a single well-prompted extraction call is sufficient.
- Backfill parser — no legacy DB exists.
- S3/object storage — explicitly out of scope.
- Redis / job queues — explicitly out of scope.

---

## Scope Guardrails

- No S3/object storage in active phases.
- No Redis/job queues in active phases.
- Modular monolith; synchronous extraction for MVP.
- Breaking API changes acceptable until a public release is cut.

