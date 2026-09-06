# Recipe Quality & Model Routing (Generic)

Date: 2026-09-05
Scope: what "good" means for a generated recipe, and how model routing / grounding
should work as the product evolves — independent of any specific vendor or model.
Companion file: RECIPE_QUALITY_IMPLEMENTATION.md (what we actually do *this* iteration,
on OpenAI, zero-shot only).

---

## 1. What "good" means for a generated recipe

### Ingredient / serving consistency

- Quantities must scale correctly with `servings` — doubling servings should
  plausibly double (or non-linearly but sensibly adjust) quantities, not just repeat
  whatever the source said.
- Quantities must also be independently *plausible* for the dish, not just internally
  consistent with each other — e.g. ratios of flour:liquid:fat in a dough should fall
  in a sane range regardless of what servings math implies.

Two tiers of this check, from cheapest to most expensive:

1. **Internal consistency** — the model reasons from its own training knowledge about
   typical ratios for the dish. This is what zero-shot generation already does
   implicitly; it's "free" but only as good as the model's judgment on a given input.
2. **Grounded consistency** — checked against actual reference data (see §3). Reserve
   this for cases where confidence is low: obscure dishes, ambiguous/underspecified
   input, or input that already looks internally inconsistent.

**Worst-case fallback**: when a grounded check is warranted but no single strong
reference exists, take the average (or median) of quantities across several known
recipes for the same dish, rather than trusting one arbitrary source or letting the
model guess unconstrained. This is a fallback, not the default path — most requests
should resolve at tier 1.

### Step quality

- **Atomic and ordered** — one action per step, in the order they must happen.
- **Technique-explicit** — name the technique (sear, deglaze, fold, proof, rest,
  reduce) rather than vague verbs, so a step reads as instruction, not summary.
- **Timing attached to the action it belongs to**, not only surfaced as recipe-level
  aggregates. A step that says "simmer" should carry its own duration; aggregate
  prep/cook totals are a derived rollup, not a substitute for per-step timing.
- **Temperature stated wherever it affects the outcome** (oven temp, oil temp for
  frying, doneness targets), not only when the source happened to mention it.
- **Traceable to ingredients** — a step should be identifiable with the ingredients it
  consumes, so the two lists stay in sync when either is edited.

### Uncertainty should be visible, not hidden

A model asked to fill in a field it can't actually determine should be able to signal
low confidence (e.g. via a summary/notes field) rather than emitting a precise-looking
number it made up. Confident-looking wrong numbers are worse than visible uncertainty,
especially for quantities and timing.

---

## 2. Operation taxonomy → model routing principles

Recipe generation isn't one operation — it's several, with very different blast radius,
latency sensitivity, and call frequency. Model choice should follow the operation's
tier, not be a single global setting.

| Tier | Operations (generic) | Blast radius if wrong | Latency sensitivity | Routing principle |
|---|---|---|---|---|
| **Structured generation & refinement** | Extract/generate a full recipe; apply a conversational edit to one | High — becomes the saved artifact the user cooks from | Moderate (user is reviewing, not typing) | Most capable model the budget allows; this is the one place quality directly matters most |
| **Judgment / gating** | On-topic gate, edit-intent classification (applied/rejected/needs-choice), category classification | Low individually, but gates everything downstream — a wrong gate silently blocks or lets through the wrong thing | Low-moderate | Fast/cheap tier is fine *if* the task is genuinely simple; don't let "cheap" become "unreliable" for something every request passes through |
| **UX-latency-critical assist** | Inline autocomplete, preference-chip suggestions | Very low — a bad ghost-text suggestion just gets ignored | High — must feel instant | Cheapest/fastest tier available; quality bar is "good enough to glance at," not "correct" |
| **Quality-judge pass** (not yet built) | A pass that scores/flags a *generated* document against §1's criteria (servings math, unit sanity, step timing) | Medium — catches the bad outputs the other tiers can't self-detect | Can be async/deferred; doesn't need to block the response | Keep narrow and cheap by default (check a handful of concrete criteria, not "critique everything culinarily"); only worth an expensive model if it runs rarely (e.g. sampled, or only on low-confidence outputs) |

**Principle**: route by this table, not by whichever model a call site happens to be
configured with. If two operations are in different tiers, they should be free to use
different models — sharing one model/config across tiers is a sign the routing isn't
deliberate.

**Cost note**: a recipe's total LLM cost is the initial generation call *plus* every
refine turn the user needs before they accept the result — not the cost of one call
judged in isolation. A cheaper model that needs more refine turns to get quantities,
units, or steps right isn't necessarily cheaper overall, and at typical per-recipe token
volumes (low hundreds of tokens in and out) the sticker-price gap between tiers is small
in absolute terms next to the cost of an extra round trip. This favors erring toward the
stronger model on the structured-generation tier rather than optimizing that call site
for the lowest price-per-token.

---

## 3. Additional grounding resources (future, evolvable)

These are options for *how* the grounded-consistency and quality-judge concepts from
§§1-2 could be backed by something more than model knowledge. This section describes
what could exist, not when it's worth building — that's an implementation-spec call,
made against real failure-mode data, not speculatively.

- **Deterministic tools over LLM judgment for arithmetic.** Serving-scaling and unit
  conversion are pure math — a code path or callable tool will always be more reliable
  than asking a language model to multiply fractions in its head. This is a tool-use
  concern, not a "use a smarter model" concern.
- **RAG over a curated reference set** (technique glossary, standard timing/temperature
  tables, canonical ratios for common dish families) — the concrete mechanism that
  would back the "average of several known recipes" worst-case fallback in §1, instead
  of that fallback relying on the model's unaided recall.
- **Web search / lookup as a tool call**, distinct from RAG over a fixed corpus — useful
  for corroborating an obscure or unusual recipe the reference set doesn't cover. This
  is a generalization of the existing raw-webpage-scrape-for-a-given-URL capability into
  an on-demand lookup the model can invoke rather than content the user must supply.
- **Cookbook data as a knowledge source** (distinct from cookbook-as-a-shared-view,
  which is an unrelated product feature) — a structured corpus of known-good recipes
  that RAG retrieval and the averaging fallback would draw from.

None of this is required to ship reasonable quality today — it's the direction to grow
toward once zero-shot-only generation shows specific, recurring failure patterns worth
grounding against.
