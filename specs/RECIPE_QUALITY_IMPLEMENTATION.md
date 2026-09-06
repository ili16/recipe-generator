# Recipe Quality — Current Implementation

Date: 2026-09-06
Scope: what we actually do this iteration — OpenAI only, zero-shot only, cost-aware
rather than cost-minimizing (see §1.1). Generic principles this follows:
RECIPE_QUALITY.md. Read that first; this file only covers where today's implementation
sits against it and what changes now vs. later.

---

## 1. Constraints for this iteration

- **Zero-shot only.** No verification pass, no RAG, no tool-use/logic layer on top of
  the LLM calls. `specs/REFACTOR_PLAN.md:87` already rejected a verifier LLM pass as
  over-engineered for a single well-prompted extraction call — that decision stands.
  Reason to keep it that way for now: we don't yet have data on which quality failure
  modes actually show up in practice, and adding a verification layer before knowing
  that is exactly the kind of premature machinery RECIPE_QUALITY.md §3 warns against
  building speculatively.
- **OpenAI only.** No multi-provider abstraction; routing changes below are model-name
  changes at existing call sites, not a new abstraction layer.

### 1.1 Model cost reasoning

Per-recipe token volume is small — a description or URL/image in, one structured
document out, a few hundred tokens either side — so the sticker-price gap between a
cheap and a frontier model is a fraction of a cent per call either way. At this volume
that gap doesn't dominate the decision the way it would at high-token-count workloads.
What does dominate is total cost per *completed* recipe, not the cost of one call in
isolation: RECIPE_QUALITY.md §2 now makes this explicit — a recipe's real cost is the
initial generation plus every refine turn the user needs before accepting the result.
A frontier model that gets servings math, units, and steps right more often needs fewer
refine round-trips, and can come out cheaper end-to-end than a cheap model that's wrong
often enough to cost two or three follow-up edits. This is why the structured-generation
tier below runs on a frontier model rather than the cheapest model that technically
works, while judgment/gating and UX-latency calls — binary/short-form, run on every
request regardless of generation quality — stay on the smallest model reliable for that
narrow task, since being wrong there doesn't create extra refine cost, it's just a
misrouted request.

## 2. Model routing for today's call sites

Two models, matched to the tiers in RECIPE_QUALITY.md §2:

- `recipeModel` — frontier tier, currently `gpt-5.6-luna`, env-overridable via
  `LLM_MODEL` (`generate_recipe.go:135`).
- `assistModel` — small/cheap tier, currently `gpt-5.4-nano`, env-overridable via
  `LLM_ASSIST_MODEL` (`generate_recipe.go:141`).

| Operation | Tier (RECIPE_QUALITY.md §2) | Model |
|---|---|---|
| `/generate`, `/refine-recipe` structured output | Structured generation & refinement | `recipeModel` (`gpt-5.6-luna`) — highest-stakes calls, and the tier where a stronger model pays for itself via fewer refine turns (§1.1) |
| `isRecipeRelated` (on-topic gate) | Judgment / gating | `assistModel` (`gpt-5.4-nano`) — a binary topic classification doesn't need generation-grade reasoning |
| `classifyCategory` (free-form save categorization) | Judgment / gating | `assistModel` (`gpt-5.4-nano`) — same reasoning |
| Refine edit-intent judgment (applied/rejected/needs_choice) | Judgment / gating, but tightly coupled to the refine call itself | `recipeModel` (part of the same call as regeneration) — keep as-is, it's judged in the same call as the regeneration, splitting it out would add a call, not remove one |
| `/suggest`, `/suggest-prefs`, `/cooking-chat` | UX-latency-critical assist | `assistModel` (`gpt-5.4-nano`) |

**Net effect**: `/generate` makes 1 call on the frontier tier (generate) and 1 on the
assist tier (judge) — the expensive tier is reserved for the call that actually produces
the saved artifact. Same shape for any save path that hits `classifyCategory`.

**Not changing**: the refine path's edit-intent judgment stays fused with the
regeneration call — that's a deliberate design already (one call, not two), not a gap.

## 3. Prompt-caching posture

This is a validation of existing behavior, not a rebuild — no caching infrastructure
exists or is needed yet; OpenAI caches shared prompt prefixes automatically.

- `buildSystemPrompt` (`generate_recipe.go:46-53`) already puts the static instruction
  block (`structuredSystemPromptBase`) first and appends the variable language suffix
  after it. This is the right shape for automatic prefix caching — keep it that way
  when editing the prompt (append, don't prepend or interleave variable content).
- `buildRefineMessages` (`generate_recipe.go:94-127`) replays the full conversation
  (origin ask, initial document, every prior edit) on every call. This grows the cached
  prefix as a refine session goes on — that's an accepted cost tradeoff for multi-turn
  correctness, not a bug to fix.
- **Non-goal**: no app-level response cache. Generated recipes are effectively unique
  per request (different input, different history) — caching full outputs wouldn't hit.

No code change is required for this section; it's a constraint to preserve when the
prompt is next edited (see §4).

## 4. Zero-shot-only quality improvements (this iteration)

Prompt-text-only changes to `structuredSystemPromptBase` — no schema change, no new
call, no logic layer. Bump `promptVersion` (`generate_recipe.go:140`) when these land,
per the existing convention.

- **Plausibility instruction**: before finalizing, sanity-check that ingredient
  quantities are reasonable for the stated `servings`, and if genuinely uncertain (an
  obscure dish, thin input), say so via `summary` rather than emitting a
  confident-looking invented number. Directly implements RECIPE_QUALITY.md §1's
  "uncertainty should be visible" principle at the internal-consistency tier — no
  grounded/RAG check involved.
- **Technique-in-prose instruction**: `step_text` should name the technique explicitly
  (sear, deglaze, fold, proof, rest) when the action has one, and state the reason for
  a specified duration/temperature when it isn't obvious. This rides entirely in the
  existing free-text `step_text` field — no schema change.

### Explicitly deferred (not silently dropped)

- A dedicated `technique` field on `RecipeStep` — would require a schema change
  (`internal/model/models.go`, `recipeJSONSchema()`) and is listed in
  `specs/REFACTOR_PLAN.md:74` as an unscheduled backlog item already.
- An actual quality-judge pass (RECIPE_QUALITY.md §2's fourth tier) — needs concrete
  criteria and evidence of real failure rates before it's worth an extra call.
- Any grounding/RAG (RECIPE_QUALITY.md §3) — needs a corpus and a reason to believe
  zero-shot recall is the actual bottleneck, not a hunch.

Each of these is a candidate for the *next* iteration once the prompt-only changes
above ship and we have saved-recipe data to tell us whether they're still needed.
