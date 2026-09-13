package model

import (
	"io"
	"strings"
	"time"
)

type GenerateRecipeInput struct {
	Prompt string
	URL    string
	// Image is any reader over the image bytes: the multipart file on /generate, a
	// bytes.Reader over a /chat attachment. Nil when there is no image.
	Image             io.Reader
	PreferredLanguage string
}

type RecipeIngredient struct {
	Section      *string  `json:"section"`
	Item         string   `json:"item"`
	Quantity     *float64 `json:"quantity"`
	QuantityText *string  `json:"quantity_text"`
	Unit         *string  `json:"unit"`
	Optional     bool     `json:"optional"`
	// Category is the shopping aisle this ingredient belongs to, emitted by the
	// generation call so the grocery list can group by aisle without a
	// hand-maintained ingredient→aisle table (BACKLOG 6.1). One of GroceryCategories;
	// nil on anything generated before 6.1 and grouped as "other".
	Category *string `json:"category"`
}

type RecipeStep struct {
	SortOrder         int    `json:"sort_order"`
	StepText          string `json:"step_text"`
	TimerSeconds      *int   `json:"timer_seconds"`
	TemperatureC      *int   `json:"temperature_c"`
	IngredientIndices []int  `json:"ingredient_indices,omitempty"`
	// Phase is which part of the cook this step belongs to: "prep" (mise en place),
	// "cook" (the heat is on and it wants attention) or "wait" (passive — marinating,
	// proofing, resting, cooling). nil on everything generated before this existed;
	// cooking mode then renders one unlabelled run of steps, exactly as it always did.
	Phase *string `json:"phase"`
	// ParallelGroup marks steps a cook does at the same time: consecutive steps sharing a
	// non-nil value are one concurrent block. nil means the step runs on its own.
	ParallelGroup *int `json:"parallel_group"`
}

type RecipeDocument struct {
	Title        string   `json:"title"`
	Summary      *string  `json:"summary"`
	Language     string   `json:"language"`
	Tags         []string `json:"tags"`
	Servings     *int     `json:"servings"`
	TotalMinutes *int     `json:"total_minutes"`
	PrepMinutes  *int     `json:"prep_minutes"`
	CookMinutes  *int     `json:"cook_minutes"`
	Difficulty   *string  `json:"difficulty"`
	// Per-serving macros. Nullable on purpose: these are model estimates, and a blank
	// is better than a confidently wrong number (BACKLOG 6.6).
	Calories    *int               `json:"calories"`
	ProteinG    *int               `json:"protein_g"`
	CarbsG      *int               `json:"carbs_g"`
	FatG        *int               `json:"fat_g"`
	Ingredients []RecipeIngredient `json:"ingredients"`
	Steps       []RecipeStep       `json:"steps"`
}

type Recipe struct {
	Recipename     string          `json:"recipename"`
	Recipe         string          `json:"recipe"`
	ID             int             `json:"id"`
	Transcript     string          `json:"transcript,omitempty"`
	Tags           []string        `json:"tags,omitempty"`
	Structured     *RecipeDocument `json:"structured,omitempty"`
	ManuallyEdited bool            `json:"manually_edited"`
	// MyVote is the caller's own thumbs up(+1)/down(-1) on this recipe, if any.
	MyVote *int16 `json:"my_vote,omitempty"`
	// VariantOfRecipeID links this recipe back to the saved recipe it was generated
	// as a variant of (POST /meal-plan/variants/{id}), if any.
	VariantOfRecipeID *int `json:"variant_of_recipe_id,omitempty"`
	// SourceType and SourceURL are where this recipe came from: recipes.source_type
	// plus the sources row AddRecipeStructured writes. Read-only provenance.
	SourceType string  `json:"source_type,omitempty"`
	SourceURL  *string `json:"source_url,omitempty"`
	// Servings and TotalMinutes are denormalised onto the list payload so the library can
	// filter on batch size and cooking time without fetching every document (BACKLOG 6.5).
	// Set by GetRecipes only; nil means unknown, never zero.
	Servings     *int       `json:"servings,omitempty"`
	TotalMinutes *int       `json:"total_minutes,omitempty"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
	// Calories is the per-serving estimate the card shows (BACKLOG 6.8). Only this one
	// macro is denormalised — the other three have nothing rendering them. nil means the
	// model had no estimate; the card must then show no badge, not "0 kcal".
	Calories *int `json:"calories,omitempty"`
	// PantryHave and PantryTotal are the pantry match (BACKLOG 7.3): how many of this
	// recipe's ingredients the caller already has, out of how many it lists. Matched on
	// the ingredient's normalised name only, so "250 g flour" in the recipe matches "2
	// cups flour" in the pantry. Set by GetRecipes only; both 0 when the recipe lists no
	// ingredients or the pantry is empty, which is the client's cue to show no badge.
	PantryHave  int `json:"pantry_have"`
	PantryTotal int `json:"pantry_total"`
	// OwnedByMe and OwnerName describe a household's shared library (BACKLOG 15.3): a
	// recipe is personal property lent to the household, so every member can open, cook
	// and plan it and only the owner can rewrite it. The client uses these to name the
	// owner on the card and to leave out the actions it must not offer. Set by GetRecipes
	// only; outside a household OwnedByMe is always true and the name goes unused.
	OwnedByMe bool   `json:"owned_by_me"`
	OwnerName string `json:"owner_name,omitempty"`
	// LastCookedAt is when the owner last marked this cooked (BACKLOG 6.3). nil means
	// never. Recent ones are held back from meal-plan candidates for CookedCooldown.
	LastCookedAt *time.Time `json:"last_cooked_at,omitempty"`
	// CookedRating is how the owner rated their last cook, 1–5 (BACKLOG 6.7). nil means
	// they never rated it — never 0. It rides into the meal-plan prompt as a soft signal.
	CookedRating *int16 `json:"cooked_rating,omitempty"`
	// ShareToken is the read-only share link's token (BACKLOG 8.4), or nil when the
	// recipe isn't shared. Owner-only: the public read strips it.
	ShareToken *string `json:"share_token,omitempty"`
	// DeletedAt is set only on the trash listing (GET /recipes/trash); every other
	// read path filters soft-deleted rows out, so it is always nil there.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// Collection is a user-named grouping of their own recipes (BACKLOG 8.1). RecipeIDs is
// the membership, so the client filters its already-loaded library rather than asking
// the server for a second, filtered list.
type Collection struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	RecipeIDs []int  `json:"recipe_ids"`
}

// Household is a closed group sharing one kitchen (BACKLOG 15.1): one pantry, one week,
// one grocery list, and a library everyone can cook from but only the owner can rewrite.
//
// ID is stable identity; JoinCode is the credential and is rotatable, so a member who
// leaves does not keep a permanent key back in. Both are sent only to members.
type Household struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	JoinCode string            `json:"join_code"`
	Members  []HouseholdMember `json:"members"`
}

// HouseholdMember is one person in it. Deliberately thin — a household has no roles and
// no admin, so there is nothing to say about a member beyond who they are.
type HouseholdMember struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	IsMe  bool   `json:"is_me"`
}

// UserPreferences is the explicit profile settings a user can set about themselves:
// cooking skill level, dietary preferences, disliked ingredients, and meal-plan
// scheduling. Fed back to the client via GET/PATCH /preferences.
type UserPreferences struct {
	SkillLevel          *string  `json:"skill_level"`
	DietaryPrefs        []string `json:"dietary_prefs"`
	DislikedIngredients []string `json:"disliked_ingredients"`
	// MealPlanNoFoodDays are lowercase weekday names ("wednesday") on which the user
	// needs no food planned at all — the meal-plan AI must leave them with no
	// assignment.
	MealPlanNoFoodDays []string `json:"meal_plan_no_food_days"`
	// MealPlanNoCookDays are lowercase weekday names on which the user doesn't want to
	// cook but still eats — the meal-plan AI should repeat the most recent prior
	// cooked day's dish (a leftover day) instead of skipping it or inventing a new one.
	MealPlanNoCookDays []string `json:"meal_plan_no_cook_days"`
	// MealPlanBatchDays: 1 = a fresh recipe every cooking day (default); 2 or 3 = the
	// same recipe repeats across that many consecutive non-skipped, non-no-cook days
	// (batch cooking).
	MealPlanBatchDays int `json:"meal_plan_batch_days"`
	// WeekStartDay is the lowercase weekday name the user's meal-plan week begins on
	// (default "monday").
	WeekStartDay string `json:"week_start_day"`
	// HouseholdSize is how many people the user cooks for (BACKLOG 9.12) — a property of
	// the household, not of a recipe, and the default a newly planned day's servings
	// starts from. nil means they never said, which is not 1: nothing to compare a
	// recipe's yield against, so no mismatch is claimed.
	HouseholdSize *int `json:"household_size"`
}

// CookingProfile is a behavioral profile derived from a user's saved recipe history by
// the background worker (internal/worker), stored in user_cooking_profiles.data. It is
// internal-only for now — not exposed via any HTTP route — but shaped so a future
// read-only screen or a future /generate prompt-personalization step could consume it
// without a schema change.
type CookingProfile struct {
	RecipeCount         int      `json:"recipe_count"`
	AvgRecipesPerWeek   float64  `json:"avg_recipes_per_week"`
	TopTags             []string `json:"top_tags,omitempty"`
	PreferredDifficulty *string  `json:"preferred_difficulty,omitempty"`
}

// GenerationOrigin records what the user originally handed the generator, so later
// refinements (and the eventual save) can be traced back to the initial ask.
type GenerationOrigin struct {
	Prompt     string `json:"prompt"`
	SourceType string `json:"source_type"` // text | url | image
	SourceURL  string `json:"source_url,omitempty"`
}

// EditTurn is one accepted edit in a refinement conversation: the instruction and the
// resulting document. By convention, the first EditTurn in a history slice
// (ChangePrompt == "") represents the initial generation output, not an edit.
type EditTurn struct {
	ChangePrompt string         `json:"change_prompt"`
	Structured   RecipeDocument `json:"structured"`
}

// RecipeVersion is one snapshot in a saved recipe's edit trail (table recipe_versions).
// ChangeNote carries the AI instruction for ChangeKind "ai_edit"; it's empty for "manual"
// and "extraction". Data is the full document as it stood after this edit, so the caller
// can diff consecutive entries to show what changed.
type RecipeVersion struct {
	Version    int            `json:"version"`
	ChangeKind string         `json:"change_kind"`
	ChangeNote string         `json:"change_note,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Data       RecipeDocument `json:"data"`
}

// GroceryCategories are the shopping aisles, in the order a list renders them. The
// model picks one per ingredient; anything else (or nothing) is "other".
var GroceryCategories = []string{"produce", "protein", "dairy", "pantry", "other"}

// NormalizeItem is the one spelling of an ingredient name that everything keys on: the
// grocery list consolidates on it (BACKLOG 6.1) and a pantry item is stored as it (7.1),
// so "Flour" in a recipe and "flour" in the pantry are the same thing.
func NormalizeItem(item string) string {
	return strings.ToLower(strings.TrimSpace(item))
}

// NormalizeUnit canonicalises a unit to the one it is summed in — kg sums with g, l with
// ml — and returns the factor to multiply the quantity by. Anything else is its own
// family, and a missing unit ("2 eggs") is the empty family. The generation prompt
// already normalises to metric-by-weight, so this only has to close the gap between a
// recipe written in g and one written in kg.
//
// Used by both the grocery list and the pantry: a pantry item and a shopping line must
// agree on units or nothing will ever match (BACKLOG 7.1, 7.3).
func NormalizeUnit(unit *string) (family string, factor float64) {
	u := ""
	if unit != nil {
		u = strings.ToLower(strings.TrimSpace(*unit))
	}
	switch u {
	case "kg":
		return "g", 1000
	case "l":
		return "ml", 1000
	default:
		return u, 1
	}
}

// PantryCategories are where a pantry item lives, in render order (BACKLOG 7.1).
// Anything else is treated as the default, "spices_dry".
var PantryCategories = []string{"fridge", "freezer", "produce", "spices_dry"}

// PantryItem is one thing the user has in the house. Name and Unit are normalised
// (model.NormalizeItem / NormalizeUnit) on the way in, so they are directly comparable
// with a recipe's ingredients. ExpiresOn is a date, not a timestamp — food expires on a
// day, not at an instant (BACKLOG 7.4).
type PantryItem struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Quantity  *float64 `json:"quantity"`
	Unit      *string  `json:"unit"`
	Category  string   `json:"category"`
	ExpiresOn *string  `json:"expires_on"` // YYYY-MM-DD
}

// GroceryIngredient is one ingredient of one planned meal, as read for a week's grocery
// list — the join of meal_plan_items × recipe_ingredients, before consolidation.
type GroceryIngredient struct {
	PlannedOn   string
	RecipeTitle string
	// PlannedServings is meal_plan_items.servings and RecipeServings is what the recipe
	// itself yields; quantities are scaled by their ratio when both are known (BACKLOG 6.2).
	PlannedServings *int
	RecipeServings  *int
	Item            string
	Quantity        *float64
	Unit            *string
	QuantityText    *string
	Category        *string
}

// GroceryLine is one shopping line: the same item across every meal in the week that
// needs it, with the quantities summed where they are comparable. Note carries the
// amounts that cannot be summed ("to taste") — they are folded in here rather than
// dropped. For names the meals the line serves, so a line can be ignored when its meal
// is (e.g. "Mon Roast chicken").
type GroceryLine struct {
	Item     string   `json:"item"`
	Category string   `json:"category"`
	Quantity *float64 `json:"quantity"`
	Unit     *string  `json:"unit"`
	Note     string   `json:"note,omitempty"`
	For      []string `json:"for"`
}

// MealPlanItem is one recipe assigned to one meal slot of one day in a user's meal plan.
type MealPlanItem struct {
	ID          int    `json:"id"`
	RecipeID    int    `json:"recipe_id"`
	RecipeTitle string `json:"recipe_title"`
	PlannedOn   string `json:"planned_on"` // YYYY-MM-DD
	// MealSlot is which meal of the day this is: breakfast|lunch|dinner|snack. It is the
	// second half of the plan's upsert key, so a day holds up to one item per slot
	// (BACKLOG 6.4). Anything that doesn't say defaults to dinner.
	MealSlot string `json:"meal_slot"`
	// Servings is how many portions of this recipe are wanted on this day (BACKLOG 6.2).
	// nil means "as the recipe is written" — it scales the grocery list only when both it
	// and the recipe's own servings are known.
	Servings *int `json:"servings,omitempty"`
	// SourceItemID, when set, names the item whose cooking produced this meal: this is
	// that pot, eaten again here (BACKLOG 9.11). nil means it is cooked on the day. The
	// row is deleted with its source (ON DELETE CASCADE) — no cook, no leftover.
	SourceItemID *int `json:"source_item_id,omitempty"`
}

// MealPlanAssignment is one proposed day in an AI-suggested meal plan. Exactly one of
// RecipeID (an existing saved recipe) or Variant (a newly proposed recipe, not yet
// saved) is set. VariantOfRecipeID names the base recipe when Variant is set.
type MealPlanAssignment struct {
	RecipeID          *int            `json:"recipe_id"`
	Variant           *RecipeDocument `json:"variant"`
	VariantOfRecipeID *int            `json:"variant_of_recipe_id"`
	PlannedOn         string          `json:"planned_on"`
	Servings          *int            `json:"servings"`
	// LeftoverOf, when set, is the YYYY-MM-DD day whose pot this day eats again
	// (BACKLOG 9.11). Filled server-side by enforceNoCookCarryover, not by the model —
	// it is not part of the suggestion's JSON schema.
	LeftoverOf *string `json:"leftover_of,omitempty"`
}

// MealPlanSuggestion is the outcome of POST /meal-plan/suggest or /meal-plan/chat.
// Status "needs_clarification" asks the caller a question via Message instead of
// proposing assignments (mirrors RefineResult's envelope shape).
type MealPlanSuggestion struct {
	Status      string               `json:"status"`
	Message     *string              `json:"message"`
	LowVariety  bool                 `json:"low_variety"`
	Assignments []MealPlanAssignment `json:"assignments"`
}

// RefineResult is the outcome of a /refine-recipe call. Status is one of:
//   - "applied": the change was made; Recipe holds the new document.
//   - "rejected": the change would break the dish's essential identity; Message
//     explains why, Recipe is nil.
//   - "needs_choice": the change is viable but ambiguous; Message is a short prompt
//     and Options lists 2-4 concrete choices for the caller to offer the user.
type RefineResult struct {
	Status  string          `json:"status"`
	Message *string         `json:"message"`
	Options []string        `json:"options"`
	Recipe  *RecipeDocument `json:"recipe"`
}

// ToolCall is one tool invocation the model asked for in an agent turn: the provider's
// call id, the tool name, and its raw JSON arguments (kept as text — arguments are
// model-generated and may not match the declared schema, so they are validated at
// dispatch, not at deserialization).
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// ConversationMessage is one message in a server-held chat thread (table
// conversation_messages, specs/AGENT_CHAT.md §2.1). Role "assistant" may carry
// ToolCalls; role "tool" carries the matching ToolCallID and the tool's JSON result as
// Content.
type ConversationMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
