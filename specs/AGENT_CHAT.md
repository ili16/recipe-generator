# Agentic Chat — Target Architecture

Date: 2026-09-09
Status: **design, not implemented.** Nothing in this document exists in code yet.
Implementation tasks: `../../BACKLOG.md` Phases 1–3.

Companion specs: `RECIPE_QUALITY.md` (what "good" means), `RECIPE_QUALITY_IMPLEMENTATION.md`
(model routing, prompt-caching posture — both carry forward unchanged), `DATA_MODEL.md`.

---

## 1. Why the current shape blocks the product

The product goal is one conversation that can create a recipe, transform an input, plan a week,
and just talk about food. The backend cannot express that today, for three structural reasons.

### 1.1 There is no tool-calling anywhere

```
$ grep -rn "Tools:\|ToolCall\|tool_choice\|ToolChoice\|FunctionDefinition" --include="*.go" .
(no matches)
```

Every LLM call is a one-shot `client.Chat.Completions.New` / `NewStreaming` with a
`ResponseFormat` JSON schema. Seven call sites: `internal/service/llm_chat.go:31`,
`generate_recipe.go:274, 340, 405`, `mealplan.go:207, 360`. The model never chooses an action —
**the user chooses it by navigating to a screen**, and the screen hardcodes an endpoint. "Make it
vegan" only works if you are already on the review screen; "plan my week around this" is
unsayable anywhere.

### 1.2 Four incompatible chat protocols, none of them stateful

| Endpoint | History shape | Notes |
|---|---|---|
| `POST /refine-recipe` | `origin` + `initial: RecipeDocument` + `history: []EditTurn` | real multi-turn replay |
| ~~`POST /meal-plan/chat`~~ | `initial: MealPlanSuggestion` + `history: []{message, plan}` | deleted, BACKLOG 3.5 |
| ~~`POST /suggest-prefs`~~ | `history: []ChatTurn` | deleted, BACKLOG 3.5 |
| `POST /cooking-chat` | none | no history at all |

All are stateless-resend: the client re-uploads the entire conversation, including every full
`RecipeDocument`, on every turn, with **no cap on `len(history)`** — only `change_prompt` is
length-limited (`handlers.go:563-565`). A ten-turn refinement of a thirty-ingredient recipe
re-uploads ten full documents at frontier-model prompt pricing. `HandleRefineRecipe` then
serializes the same history a *second* time, as prose, to feed the judge (`handlers.go:570-574`).

Everything in the bottom half of `internal/model/models.go` — `GenerationOrigin`, `EditTurn`,
`MealPlanChatTurn`, `RefineResult` — exists only to support this protocol.

### 1.3 Prompts are string concatenation, not composition

**22 distinct prompt literals** across three files, joined by `+` and `fmt.Sprintf`, forming two
independent inheritance chains:

```
structuredSystemPromptBase (generate_recipe.go:28)
  ├─ + language suffix            → /generate
  ├─ + refineJudgmentInstructions → /refine-recipe
  └─ + variantInstructions        → /meal-plan/variants

mealPlanSuggestSystemPromptBase (mealplan.go:62)
  ├─ + mealPlanLowVarietyAddendum → /meal-plan/suggest
  └─ + chat rules                 → /meal-plan/chat
```

Nothing is externalized, templated, or testable as data. A single hand-bumped
`promptVersion = "v4"` (`generate_recipe.go:169`) covers only two of the 22.

Context is likewise rendered as **English prose** where JSON is the natural fit:
`buildMealPlanContextText` (`mealplan.go:104-140`) makes ~15 `fmt.Fprintf` calls to produce
sentences the model must re-parse — that is a tool result written by hand.

---

## 2. The target: one endpoint, one loop

```
POST /api/v1/chat        (Server-Sent Events)

request  { conversation_id?, message, attachments?: [{type: 'image'|'url', ...}] }

stream   event: token       incremental assistant prose
         event: tool_start  {name, args}     → client renders "Looking at that link…"
         event: tool_end    {name, ok}
         event: artifact    {kind: 'recipe'|'week_plan'|'grocery_list', data}
         event: done        {conversation_id, message_id}
         event: error       {code}           ← code, never raw err.Error()
```

The loop is the standard one: send the thread + tool definitions, execute any tool calls the
model returns, append results, repeat until it answers in prose. Bound it — a hard cap on
iterations per turn, and a wall-clock deadline from `r.Context()`.

Artifacts are the key UX primitive: a recipe is not prose in the transcript, it is a **card
rendered inline** that later turns mutate in place. That is what makes "make it vegan" work in
the same surface as "what's a good side for this?".

### 2.1 Server-held threads

```sql
conversations         (id, user_id, title, created_at, updated_at)
conversation_messages (id, conversation_id, seq, role, content,
                       tool_calls JSONB, tool_call_id, created_at)
```

The client sends **one message**, not a transcript. This deletes the resend protocol, the
unbounded prompt growth, the four incompatible history shapes, and most of
`internal/model/models.go`'s second half.

Anonymous callers get an in-memory thread with a TTL, keyed like the rate limiter keys them (by
IP), so the anonymous generate path — a real acquisition funnel — survives unchanged.

History still needs a strategy as threads get long: keep the last N turns verbatim, summarize
older ones into a rolling system note. Do this **when a thread actually gets long**, not on day
one; the point of server-held state is that the policy becomes changeable without a client
release.

### 2.2 Tool catalog

Each tool returns **JSON**, never prose.

| Tool | Args | Replaces |
|---|---|---|
| `generate_recipe` | `description?, url?, image_ref?, language?` | `POST /generate` |
| `transform_recipe` | `target, instruction` | `POST /refine-recipe` |
| `search_my_recipes` | `query?, tags?` | client-side filtering in `RecipesScreen` |
| `get_recipe` | `id` | `GET /recipes/{id}` |
| `save_recipe` | `draft_ref, tags?` | `POST /add-recipe` |
| `plan_week` | `starts_on, constraints?` | `POST /meal-plan/suggest` |
| `set_plan_day` | `date, recipe_id \| draft_ref` | `POST /meal-plan/items` |
| `clear_plan_day` | `date` | `DELETE /meal-plan/items/{id}` |
| `get_preferences` / `set_preferences` | — / `patch` | `GET`/`PATCH /preferences` |
| `fetch_url` | `url` | the inline `utils.GetWebsite` pre-step |
| `answer_cooking_question` | `recipe_ref?, step?, question` | `POST /cooking-chat` |

Two rules for the catalog:

- **Ownership never comes from tool args.** The `user_id` for every tool is taken from
  `auth.FromContext`, exactly as the handlers do today. A tool arg naming a recipe id is
  validated against the caller, never trusted.
- **Write tools are the confirmation boundary.** `save_recipe`, `set_plan_day`, and
  `set_preferences` change durable state. Either the model calls them only on an explicit user
  instruction (prompt-level), or the client confirms them (UI-level). Prefer the second for
  anything destructive — see §4.

### 2.3 What disappears

**The judge gate.** `isRecipeRelated` (`generate_recipe.go:700`) is an extra blocking LLM
round-trip before every generate/suggest/cooking-chat/refine, asking "yes or no", parsed with
`strings.Contains(strings.ToLower(result), "yes")` (`:718`), failing open on error. In an agent
loop, off-topic input is simply a turn where no tool gets called. Delete it and the latency it
adds to every p50.

**The second tagger.** `classifyTags` (`generate_recipe.go:672`) asks for a comma-separated list
in plain text and then does `strings.Contains(result, slug)` over all 38 slugs — substring
matching, so any prose mentioning a slug false-positives. The structured schema already returns
`tags` as a constrained enum (`:212-215`). One tagger.

**The streaming parallel universe.** Every generate function exists twice
(`generateFromText`/`generateFromTextStream`, and the same for URL and image — `:474-513` vs
`:546-585`, byte-identical except the final call). Worse, the streamed deltas are **raw JSON
schema fragments**: the client streams half-formed JSON it cannot use until `event: done`. It is
a progress bar dressed as a stream.

The meal-plan design got this right and is the pattern to keep: stream a *separate cheap
conversational* reply (`StreamMealPlanChatReply`, `mealplan.go:349`) while the structured call
runs. In the agent loop this becomes free — prose tokens stream, artifacts arrive when ready.

> One caveat carried over from the existing implementation: that streamed reply currently gets
> **no access to the real plan** (`mealplan.go:351`, `priorMeal: nil`), so it can cheerfully
> announce "Swapping Wednesday for something vegetarian!" while the structured call does
> something else. In the agent loop the prose and the tool results come from the same thread, so
> this class of contradiction goes away by construction — do not reintroduce a second, blind
> model.

---

## 3. One LLM abstraction: `internal/llm`

Six functions each hand-roll the same ~50 lines of span / timing / usage / cost / parse /
validate:

| Function | File:line |
|---|---|
| `goopenAIChatCompletion` | `llm_chat.go:16` |
| `generateRefineFromMessages` | `generate_recipe.go:264` |
| `generateStructuredFromMessages` | `generate_recipe.go:330` |
| `generateStructuredStream` | `generate_recipe.go:391` |
| `generateMealPlanSuggestion` | `mealplan.go:197` |
| `StreamMealPlanChatReply` | `mealplan.go:349` |

Collapse to one:

```go
package llm

type Spec[T any] struct {
    Model     Model            // Frontier | Assist — see RECIPE_QUALITY_IMPLEMENTATION.md §2
    CallType  string           // telemetry + cost dimension
    Messages  []Message
    Schema    *Schema          // nil ⇒ free-form text
    Tools     []ToolDef        // nil ⇒ no tool-calling
    OnDelta   func(string)     // nil ⇒ non-streaming
}

func Call[T any](ctx context.Context, spec Spec[T]) (T, Usage, error)
```

**This is the fix for the billing hole, structurally.** Today all six generate branches pass
`context.TODO()` (`generate_recipe.go:477, 491, 509, 549, 563, 581`), and `recordCost` reads the
caller out of the context — so `/generate` and `/generate/stream` record **zero** usage rows and
contribute **zero** to the budget cap they are nominally guarded by. With one call path that
takes `ctx` as its first argument and records cost itself, that bug cannot be written again.

Also collapsed on the way through: the duplicated `nullable` closure (`generate_recipe.go:176`
and `mealplan.go:24`, identical), the two byte-identical `writeSSE` copies (`handlers.go:173`
and `cmd/mealplan.go:375`), and the duplicated brace-scraping JSON hack
(`handlers.go:381-386` / `:483-488` — both prompts ask for JSON in prose instead of using the
structured-output support the same file already uses elsewhere).

### 3.1 Prompts become data

Move the 22 literals into `internal/llm/prompts/`, one file per prompt, loaded via `go:embed`,
composed explicitly rather than by `+`. Two constraints to preserve:

- **Prefix-caching shape.** Static block first, variable suffix appended — never prepend or
  interleave variable content (`RECIPE_QUALITY_IMPLEMENTATION.md §3`). OpenAI caches shared
  prefixes automatically; the current `buildSystemPrompt` already has the right shape.
- **Versioning.** `promptVersion` is hand-bumped and covers 2 of 22 prompts. Once prompts are
  files, hash them and record the hash on `recipes.ai_prompt_version` automatically.

### 3.2 Keep deterministic post-validation

`enforceNoCookCarryover` (`mealplan.go:257-306`) makes a rule true **by construction** instead of
by prompt cooperation. That is the right instinct and the opposite of the codebase's usual reflex
(add another sentence to the prompt). Generalize it into a post-validation step on tool results.

Two bugs to fix while moving it: it uses `d.Weekday()` in **server-local time** (`:281`) while the
client computes the same weekday locally, so badges can disagree across timezones; and it runs
*after* the chat call (`:338`), so it can silently overwrite a change the user explicitly asked
for — "make Thursday salmon" on a no-cook Thursday gets stomped back to Wednesday's dish while
the reply says "Done!".

---

## 4. Safety and cost properties to preserve

The existing middleware stack is the tidiest part of the backend and carries over:

```go
llm := func(rl *ratelimit.Limiter, h http.HandlerFunc) http.HandlerFunc {
    return authenticator.Optional(rl.Guard(billing.Guard(h)))   // cmd/main.go:68-70
}
```

`POST /chat` sits behind the same wrapper. But an agent turn is **N model calls, not one**, which
changes the cost model:

- **Rate-limit the turn, budget-cap the tokens.** One rate-limit slot per user turn is right; the
  budget guard must see the *cumulative* cost of every iteration in that turn, checked between
  iterations so a runaway loop trips the cap mid-turn rather than after.
- **Cap iterations per turn** and surface hitting the cap as an `event: error` with a code.
- **Price unknown models loudly.** `billing/pricing.go:34-36` returns **$0** for any model not in
  a two-entry map, while `generate_recipe.go:157` explicitly invites overriding `LLM_MODEL` for
  A/B tests. Today an A/B test silently disables billing. Fail or warn; never $0.
- **Never leak `err.Error()` over the wire.** `handlers.go:248` and `cmd/mealplan.go:431` send
  raw error text — including wrapped OpenAI messages — to the client. The SSE contract in §2
  carries a code.
- **Confirm destructive tool calls in the UI.** The lesson from the meal planner's accept path
  (`WeekView.tsx:250-275`): it is non-atomic, non-idempotent, silently deletes hand-picked days,
  and cannot be undone. An agent that can call `set_plan_day` needs the write to be atomic and
  reversible before it is autonomous.

---

## 5. Migration order

Endpoints retire one at a time; nothing is deleted before the client stops calling it.

1. **Phase 1** — `internal/llm` lands underneath the existing endpoints. Behavior-preserving, no
   API change. This is where the `context.TODO()` billing hole closes for good.
2. **Phase 2** — `conversations` schema + `POST /chat` + tool dispatch, running *alongside* the
   existing routes. `/generate` keeps working.
3. **Phase 3** — the client moves to `/chat`. Then, and only then, delete `/refine-recipe`,
   `/cooking-chat`, `/meal-plan/chat`, `/meal-plan/chat/stream`, `/meal-plan/suggest`, the
   `suggest-prefs` chat mode, `isRecipeRelated`, `classifyTags`, and the six-way generate fan-out.

Breaking API changes remain acceptable — there is no public release cut.
