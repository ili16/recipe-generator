export interface Recipe {
  id: number;
  recipename: string;
  recipe: string;
  tags?: string[];
  structured?: RecipeDocument;
  manually_edited?: boolean;
  my_vote?: 1 | -1 | null;
  // Links back to the saved recipe this was generated as a variant of (POST
  // /meal-plan/variants/:id, persisted at save time), if any.
  variant_of_recipe_id?: number | null;
  // Where this recipe came from, recorded at save time. `source_url` is set for
  // 'url' only; both are absent on recipes saved before BACKLOG 3.7.
  source_type?: 'text' | 'url' | 'image' | 'voice' | 'manual' | 'import';
  source_url?: string | null;
  // Denormalised onto the list payload (GET /get-recipes) so the library can filter on
  // cooking time and batch size without loading every document. Undefined = unknown.
  servings?: number | null;
  total_minutes?: number | null;
  // Per-serving calorie estimate, the one macro the card shows (BACKLOG 6.8). Absent =
  // not estimated, and must render as no badge at all rather than "0 kcal".
  calories?: number | null;
  // How many of this recipe's ingredients the user already has, out of how many it lists
  // (BACKLOG 7.3). Matched on the ingredient name alone, so a different unit form still
  // counts. Both 0 — or absent, on a payload from before 7.3 — means no badge.
  pantry_have?: number;
  pantry_total?: number;
  // Ingredients the pantry could cover with a swap — same class, different food, e.g.
  // penne for spaghetti (BACKLOG 17.4a). Never added to pantry_have: the swap is the
  // cook's call. Absent on any payload from before 17.4a.
  pantry_close?: number;
  // A household's shared library (BACKLOG 15.3). A recipe is personal property lent to
  // the household: everyone can open, cook and plan it, only the owner can rewrite it.
  // `owned_by_me` is false only for another member's recipe — outside a household every
  // visible recipe is the caller's, and `owner_name` goes unused.
  owned_by_me?: boolean;
  owner_name?: string;
  created_at?: string;
  // When the user last marked this cooked (BACKLOG 6.3). Absent = never. Recently
  // cooked recipes are held back from meal-plan suggestions server-side.
  last_cooked_at?: string | null;
  // How the user rated their last cook, 1–5 (BACKLOG 6.7). Absent = never rated.
  cooked_rating?: number | null;
  // The read-only share link's token (BACKLOG 8.4), or absent when not shared.
  // Owner-only: the public read never carries it.
  share_token?: string | null;
  // Set only on the trash listing (GET /recipes/trash); absent everywhere else.
  deleted_at?: string;
}

// A user-named grouping of their own recipes (BACKLOG 8.1). `recipe_ids` is the whole
// membership, so the library filters itself client-side — there is no per-collection
// list endpoint.
export interface Collection {
  id: number;
  name: string;
  recipe_ids: number[];
}

export interface RecipeGeneratePayload {
  description?: string;
  url?: string;
  image?: File;
}

// The three parts of a cook: mise en place, the heat being on, and waiting. Mirrors the
// backend's recipe_steps.phase check constraint.
export type CookPhase = 'prep' | 'cook' | 'wait';

export interface RecipeDocument {
  title: string;
  summary?: string | null;
  language: string;
  tags: string[];
  servings?: number | null;
  total_minutes?: number | null;
  prep_minutes?: number | null;
  cook_minutes?: number | null;
  difficulty?: string | null;
  // Per-serving macros, all nullable: they are model estimates, so null means "not
  // estimated" and must render as nothing at all, never 0 (BACKLOG 6.6).
  calories?: number | null;
  protein_g?: number | null;
  carbs_g?: number | null;
  fat_g?: number | null;
  ingredients: Array<{
    section?: string | null;
    item: string;
    quantity?: number | null;
    quantity_text?: string | null;
    unit?: string | null;
    optional: boolean;
    // Shopping aisle, emitted by the generation call for the grocery list (BACKLOG
    // 6.1). Absent on anything generated before it.
    category?: GroceryCategory | null;
    // How much one of this line's own unit weighs, read from ingredient_grams on the
    // way out (BACKLOG 17.3). Never stored on the recipe; it exists so the servings
    // scaler can turn "1.5 onions" into something a cook can act on. Absent means
    // nothing knows, and the scaler must then show the bare number.
    grams_per_unit?: number | null;
    // The caller's kitchen already holds this line (BACKLOG 16.7). Read-only.
    in_pantry?: boolean;
    // The cook's own substitution on this line (BACKLOG 17.4c). `swapped_from` is what
    // the recipe actually says and is present only on a swapped line — `item` then
    // already carries the replacement, with the amount converted by weight.
    // `swap_options` is what their kitchen holds that could stand in here.
    //
    // Both are read-only: a swap belongs to the cook, not the recipe, so neither may be
    // sent back on a save.
    swapped_from?: string | null;
    swap_options?: string[] | null;
  }>;
  steps: Array<{
    sort_order: number;
    step_text: string;
    timer_seconds?: number | null;
    temperature_c?: number | null;
    ingredient_indices?: number[] | null;
    // Which part of the cook this step is: mise en place, heat-is-on, or passive
    // waiting. Absent on everything generated before the cook flow existed, which
    // cooking mode renders as one unlabelled run of steps, exactly as it always did.
    phase?: CookPhase | null;
    // Consecutive steps sharing a non-null value happen at the same time and are walked
    // as one card. null means the step runs on its own.
    parallel_group?: number | null;
  }>;
}

export interface RecipeResponse {
  recipename: string;
  recipe: string;
  structured?: RecipeDocument;
}

// What the user originally handed the generator — carried through the review chat so
// later edits (and the eventual save) stay traceable to the initial ask.
export interface GenerationOrigin {
  prompt: string;
  source_type: 'text' | 'url' | 'image';
  source_url?: string;
}

// One accepted edit in a refinement conversation: the instruction and the resulting
// document. When persisted at save time, index 0 (change_prompt: '') stands for the
// initial generation, not an edit.
export interface EditTurn {
  change_prompt: string;
  structured: RecipeDocument;
}

// PATCH /update-recipe - persist edits to a saved recipe. ai_sourced omitted/false means a
// manual edit (flags the recipe manually_edited); true clears that flag (an applied AI
// refine result now backs the recipe instead). change_prompt is optional and, for an
// ai_sourced update, is recorded as that edit's change_note in the recipe's history.
export interface PatchRecipePayload {
  id: number;
  structured: RecipeDocument;
  ai_sourced?: boolean;
  change_prompt?: string;
}

// GET /recipes/:id/history - one snapshot in a saved recipe's edit trail, newest first.
// change_note carries the AI instruction for change_kind "ai_edit"; absent otherwise.
export interface RecipeVersion {
  version: number;
  change_kind: 'extraction' | 'manual' | 'ai_edit' | 'import';
  change_note?: string;
  created_at: string;
  data: RecipeDocument;
}

export interface CookingNote {
  stepIndex: number;
  stepText: string;
  note: string;
}

export interface UserProfile {
  name: string;
  email?: string;
  username?: string;
}

export type Weekday = 'monday' | 'tuesday' | 'wednesday' | 'thursday' | 'friday' | 'saturday' | 'sunday';

// GET/PATCH /preferences - explicit profile settings the user can set about themselves.
export interface UserPreferences {
  skill_level: 'beginner' | 'intermediate' | 'advanced' | null;
  dietary_prefs: string[];
  disliked_ingredients: string[];
  // Meal-plan scheduling: days the user needs no food planned at all (a true skip), days
  // they don't want to cook but still eat (a leftover day, repeats the prior cooked
  // day's dish), and how many consecutive days one recipe should cover (1 = fresh every
  // day).
  meal_plan_no_food_days: Weekday[];
  meal_plan_no_cook_days: Weekday[];
  meal_plan_batch_days: 1 | 2 | 3;
  // Which weekday the user's meal-plan week begins on.
  week_start_day: Weekday;
  // How many people the user cooks for (BACKLOG 9.12) — a property of the household, not
  // of a recipe, and where a newly planned day's servings starts from. null means they
  // never said, which is not 1: with no number there is nothing to compare a recipe's
  // yield against, so the planner claims no mismatch.
  household_size: number | null;
  // The UI language the user picked, or null when they never picked one — which is not
  // English: the client follows the device locale instead (BACKLOG 14.1).
  language: 'en' | 'de' | null;
}

export type GenerateMethod = 'description' | 'link' | 'image' | 'voice';

// Which meal of the day a plan item fills. A day holds at most one item per slot
// (BACKLOG 6.4); anything that doesn't say is a dinner.
export type MealSlot = 'breakfast' | 'lunch' | 'dinner' | 'snack';

// The order a day is eaten, which is the order the API returns and the UI renders.
export const MEAL_SLOTS: MealSlot[] = ['breakfast', 'lunch', 'dinner', 'snack'];

// GET /meal-plan - one recipe assigned to one slot of one day (and time) of a meal plan.
export interface MealPlanItem {
  id: number;
  recipe_id: number;
  recipe_title: string;
  planned_on: string; // YYYY-MM-DD
  meal_slot: MealSlot;
  // Portions wanted that day (BACKLOG 6.2). Absent means "as the recipe is written";
  // when set it scales that day's grocery quantities server-side.
  servings?: number | null;
  // The item whose cooking produced this meal: this is that pot, eaten again here
  // (BACKLOG 9.11). Absent/null means it is cooked on the day. Deleting the cook deletes
  // its leftovers server-side (ON DELETE CASCADE).
  source_item_id?: number | null;
  // When this meal was cooked and eaten. Set means it has left the grocery list and the
  // pantry has already been deducted for it — a different fact from Recipe.last_cooked_at,
  // which is about the recipe rather than this particular pot.
  cooked_at?: string | null;
}

// One pantry line a cooked meal changed. `removed` means the item is gone entirely;
// otherwise `remaining` is what is left, in the row's own unit. The app shows these
// rather than editing someone's fridge silently.
export interface PantryChange {
  name: string;
  used: number;
  unit?: string | null;
  remaining?: number | null;
  removed?: boolean;
}

// What marking a meal cooked did to the pantry. `skipped` names the ingredients the
// server would not guess at — no known conversion, or no amount recorded — so the app
// can say so instead of implying they were deducted.
export interface CookedResult {
  pantry: { applied: PantryChange[]; skipped: string[] };
}

// Where a pantry item lives, mirroring model.PantryCategories (BACKLOG 7.1).
export type PantryCategory = 'fridge' | 'freezer' | 'produce' | 'spices_dry';

// A household: a closed group sharing one kitchen (BACKLOG 15.1) — one pantry, one week,
// one grocery list, and a library everyone can cook from. `join_code` is the credential
// and is rotatable, so a member who leaves does not keep a working key.
export interface Household {
  id: string;
  name: string;
  join_code: string;
  members: HouseholdMember[];
}

// There are no roles and no admin, so a member is only ever who they are — plus the two
// preferences the agent asserts about the whole table rather than about them (BACKLOG 15.6).
// The backend unions these into what it plans against; per member is how the screen can put
// a name on every line of that union.
export interface HouseholdMember {
  id: number;
  name: string;
  email: string;
  is_me: boolean;
  dietary_prefs: string[];
  disliked_ingredients: string[];
}

// One thing the user has in the house (GET /pantry). `name` and `unit` come back
// normalised by the server — lowercased, and kg/l folded into g/ml — which is what lets a
// pantry item be compared with a recipe's ingredient at all (BACKLOG 7.1).
export interface PantryItem {
  id: number;
  name: string;
  quantity?: number | null;
  unit?: string | null;
  category: PantryCategory;
  expires_on?: string | null; // YYYY-MM-DD
  // "This kitchen always has this" — salt, pepper, oil. It still counts toward a recipe's
  // pantry match, which is the point of marking one; what it stops is the grocery list
  // shopping for it and a cooked meal deducting it.
  staple?: boolean;
  // "I am nearly out of this." The only thing that ever changes about a staple, and what
  // puts it back on the grocery list (BACKLOG 16.6).
  running_low?: boolean;
  // When the user last said this line is still true (ISO timestamp). A staple that has
  // gone STOCK_CHECK_DAYS unconfirmed is what the Pantry screen's stock check asks about.
  confirmed_at?: string;
}

// The shopping aisles a grocery line groups under, mirroring model.GroceryCategories.
export type GroceryCategory = 'produce' | 'protein' | 'dairy' | 'pantry' | 'other';

// One shopping line: an item summed across every meal in the week that needs it
// (GET /grocery-list). `note` carries amounts that could not be summed ("to taste");
// `for` names the meals the line serves, so dropping a meal means dropping its lines.
export interface GroceryLine {
  item: string;
  category: GroceryCategory;
  quantity?: number | null;
  unit?: string | null;
  note?: string;
  for: string[];
  // The week's full demand, sent only when the pantry already covers part of it
  // (BACKLOG 16.5). `quantity` is then the shortfall and `need - quantity` is what the
  // kitchen holds. Ticking the line stores `need`, not `quantity`: after the trip you
  // hold what the week asked for, not only what you carried home.
  need?: number | null;
}

export interface GroceryList {
  starts_on: string; // YYYY-MM-DD
  ends_on: string; // YYYY-MM-DD
  lines: GroceryLine[];
}

/**
 * One ticked-off shopping line, shared across the household (BACKLOG 15.9). `line_key` is
 * the item|unit identity the list is keyed on; `pantry_item_id` is the pantry row the tick
 * created, and `mine` is false for a housemate's tick — which is how a shopper tells their
 * own from the ones that appeared while they were in the aisle. `mine` is resolved on the
 * server because the client authenticates with an OIDC subject and never sees its row id.
 */
export interface GroceryTick {
  line_key: string;
  pantry_item_id?: number;
  mine: boolean;
}

export interface MealPlanWeek {
  starts_on: string; // YYYY-MM-DD
  ends_on: string; // YYYY-MM-DD
  items: MealPlanItem[];
  // The week's read-only link token, "" when it is not shared (BACKLOG 13.2).
  share_token?: string;
}

// What a /p/<token> visitor sees: the week and what it needs, nothing owner-only.
export interface SharedPlan extends MealPlanWeek {
  lines: GroceryLine[];
}

// One proposed day in an AI-suggested meal plan (POST /meal-plan/suggest,
// /meal-plan/chat). Exactly one of recipe_id or variant is set.
export interface MealPlanAssignment {
  recipe_id: number | null;
  variant: RecipeDocument | null;
  variant_of_recipe_id: number | null;
  planned_on: string; // YYYY-MM-DD
  servings: number | null;
}

export interface MealPlanSuggestion {
  status: 'applied' | 'needs_clarification';
  message: string | null;
  low_variety: boolean;
  assignments: MealPlanAssignment[];
}

// ---- POST /chat (the agent loop) -------------------------------------------

// A structured tool result the transcript renders as a card instead of prose.
// `draft_ref` names an unsaved draft the agent can still transform; `recipe_id` a saved one.
export type ChatArtifact =
  // `derived_from` is the draft_ref (or `recipe:<id>`) this document was transformed out
  // of — set by transform_recipe and review_cook_flow, absent on a fresh generation. It
  // is how the card finds its own before-state in the thread and shows what changed
  // (BACKLOG.md 10.3) rather than silently replacing the recipe.
  | { kind: 'recipe'; data: { draft_ref?: string; recipe_id?: number; derived_from?: string; document: RecipeDocument } }
  | { kind: 'week_plan'; data: { starts_on: string; ends_on: string; plan: MealPlanSuggestion } };

// A mutating tool call the agent wants to make, held until the user approves it
// (BACKLOG.md 10.2). `args` is the tool's own JSON, rendered per tool; `replaces` is what
// is already planned on the days it would write, read server-side before anything ran.
export interface ChatApproval {
  id: string;
  name: string;
  args: string;
  replaces?: { date: string; meal_slot: string; current_recipe: string }[];
  /** Set once answered, so the card renders its outcome instead of its buttons. */
  decision?: 'approved' | 'declined';
}

// What the composer can hang on a turn. An image travels inline as a data URL or bare
// base64 — /chat is JSON, and the server sniffs the bytes before they reach the model.
export type ChatAttachment =
  | { type: 'url'; url: string }
  | { type: 'image'; data: string };

// The SSE frames one turn emits, in the order they can arrive.
export type ChatStreamEvent =
  | { type: 'token'; payload: { text: string } }
  | { type: 'tool_start'; payload: { name: string; args: string } }
  | { type: 'tool_end'; payload: { name: string; ok: boolean } }
  | { type: 'artifact'; payload: ChatArtifact }
  | { type: 'approval_request'; payload: ChatApproval }
  | { type: 'done'; payload: { conversation_id: string } };

// What the agent is extracting from, read off the tool call's own arguments and shown
// before the recipe arrives (BACKLOG.md 3.7). `value` is the URL, the photo's data URL,
// or the description the agent worked from.
export interface ChatSource {
  kind: 'url' | 'photo' | 'text';
  value: string;
}

// One rendered turn in the thread. Tool names are kept so a turn that only ran tools
// still shows what happened.
export interface ChatMessage {
  role: 'user' | 'assistant';
  text: string;
  /** How many photos the user attached to this turn; the bytes themselves are not kept. */
  attachmentCount?: number;
  tools?: string[];
  sources?: ChatSource[];
  artifacts?: ChatArtifact[];
  error?: string;
  /** The user pressed Stop mid-turn (BACKLOG.md 10.1); whatever arrived is kept. */
  stopped?: boolean;
  /** The write this turn is waiting on the user to approve (BACKLOG.md 10.2). */
  approval?: ChatApproval;
}

// A thread in the conversation list (BACKLOG.md 10.4). `title` is named server-side from
// the first message and can be empty while that call is still in flight — the preview (the
// thread's last readable message) stands in for it.
export interface ConversationSummary {
  id: string;
  title: string;
  preview: string;
  updated_at: string;
}

// One reopened thread. The server has already folded each turn's messages and cards into
// the same bubbles the stream produces, so this drops straight into the transcript.
export interface ConversationThread {
  id: string;
  title: string;
  messages: {
    role: 'user' | 'assistant';
    text: string;
    attachment_count?: number;
    artifacts?: ChatArtifact[];
  }[];
}

/* ── Feedback (BACKLOG 9.16) ───────────────────────────────────────────────── */

// 'bug' and 'idea' come from the report sheet; 'pulse' is the one-tap thumb on the
// occasional prompt. One shape, because they end up in one morning read.
export type FeedbackKind = 'bug' | 'idea' | 'pulse';

export interface FeedbackPayload {
  kind: FeedbackKind;
  /** Required for 'bug'/'idea'; optional for a 'pulse', which can be a thumb alone. */
  message?: string;
  /** +1/-1, 'pulse' only. */
  sentiment?: 1 | -1;
  /** Only ever collected from an anonymous reporter, and never required of them. */
  contact?: string;
  /** Auto-attached: the screen they were on, platform, app version, locale. */
  context?: Record<string, string | number | undefined>;
  /**
   * An optional screenshot as a data: URL (or bare base64). The server decodes it, checks
   * it really sniffs as an image, and stores the bytes — a written report is ambiguous
   * twice and a picture settles both halves.
   */
  screenshot?: string;
}

// This month's LLM spend against the cap that stops a turn (BACKLOG.md 10.5). The cap is
// real money the user never agreed to, so it is stated in Profile rather than only
// appearing as a refusal. `period_start` is the first of the current month; the cap resets
// a month after it.
export interface Usage {
  spent_usd: number;
  cap_usd: number;
  period_start: string;
  pct_used: number;
}
