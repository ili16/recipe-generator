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
