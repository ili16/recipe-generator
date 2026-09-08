package model

import (
	"mime/multipart"
	"time"
)

type GenerateRecipeInput struct {
	Prompt            string
	URL               string
	Image             multipart.File
	PreferredLanguage string
}

type RecipeIngredient struct {
	Section      *string  `json:"section"`
	Item         string   `json:"item"`
	Quantity     *float64 `json:"quantity"`
	QuantityText *string  `json:"quantity_text"`
	Unit         *string  `json:"unit"`
	Optional     bool     `json:"optional"`
}

type RecipeStep struct {
	SortOrder         int    `json:"sort_order"`
	StepText          string `json:"step_text"`
	TimerSeconds      *int   `json:"timer_seconds"`
	TemperatureC      *int   `json:"temperature_c"`
	IngredientIndices []int  `json:"ingredient_indices,omitempty"`
}

type RecipeDocument struct {
	Title       string             `json:"title"`
	Summary     *string            `json:"summary"`
	Language    string             `json:"language"`
	Tags        []string           `json:"tags"`
	Servings    *int               `json:"servings"`
	PrepMinutes *int               `json:"prep_minutes"`
	CookMinutes *int               `json:"cook_minutes"`
	Difficulty  *string            `json:"difficulty"`
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
	// GenerationID identifies the /generate call that produced this recipe, so a
	// later /add-recipe or /refine-recipe call can report it back for funnel
	// tracking (see generation_events). Only set on /generate's own response.
	GenerationID string `json:"generation_id,omitempty"`
	// VariantOfRecipeID links this recipe back to the saved recipe it was generated
	// as a variant of (POST /meal-plan/variants/{id}), if any.
	VariantOfRecipeID *int `json:"variant_of_recipe_id,omitempty"`
}

// UserPreferences is the explicit profile settings a user can set about themselves:
// cooking skill level, dietary preferences, disliked ingredients, and how often they
// cook. Fed back to the client via GET/PATCH /preferences.
type UserPreferences struct {
	SkillLevel          *string  `json:"skill_level"`
	DietaryPrefs        []string `json:"dietary_prefs"`
	DislikedIngredients []string `json:"disliked_ingredients"`
	CookingCadence      *string  `json:"cooking_cadence"`
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
}

// MealPlanWeekPreferences overrides UserPreferences' 3 scheduling fields for one
// specific week. Each pointer nil means "inherit the user's global default"; a non-nil
// pointer (including one pointing at an empty slice) is an explicit override for that
// week. Fed back to the client via GET/PATCH /meal-plan/week-preferences.
type MealPlanWeekPreferences struct {
	NoFoodDays *[]string `json:"no_food_days"`
	NoCookDays *[]string `json:"no_cook_days"`
	BatchDays  *int      `json:"batch_days"`
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

// MealPlanItem is one recipe assigned to one day (and time) in a user's meal plan.
type MealPlanItem struct {
	ID          int    `json:"id"`
	RecipeID    int    `json:"recipe_id"`
	RecipeTitle string `json:"recipe_title"`
	PlannedOn   string `json:"planned_on"` // YYYY-MM-DD
	StartTime   string `json:"start_time"` // HH:MM:SS
}

// MealPlanAssignment is one proposed day in an AI-suggested meal plan. Exactly one of
// RecipeID (an existing saved recipe) or Variant (a newly proposed recipe, not yet
// saved) is set. VariantOfRecipeID names the base recipe when Variant is set.
type MealPlanAssignment struct {
	RecipeID          *int            `json:"recipe_id"`
	Variant           *RecipeDocument `json:"variant"`
	VariantOfRecipeID *int            `json:"variant_of_recipe_id"`
	PlannedOn         string          `json:"planned_on"`
	StartTime         string          `json:"start_time"`
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

// MealPlanChatTurn is one exchange in an in-progress /meal-plan/chat conversation: the
// user's message and the plan proposal it produced. By convention the first turn the
// client sends alongside `initial` is not included here — see EditTurn's convention.
type MealPlanChatTurn struct {
	Message string             `json:"message"`
	Plan    MealPlanSuggestion `json:"plan"`
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
