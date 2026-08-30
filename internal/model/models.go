package model

import "mime/multipart"

type GenerateRecipeInput struct {
	Prompt string
	URL    string
	Image  multipart.File
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
	Category    string             `json:"category"`
	Servings    *int               `json:"servings"`
	PrepMinutes *int               `json:"prep_minutes"`
	CookMinutes *int               `json:"cook_minutes"`
	Difficulty  *string            `json:"difficulty"`
	Ingredients []RecipeIngredient `json:"ingredients"`
	Steps       []RecipeStep       `json:"steps"`
}

type Recipe struct {
	Recipename string          `json:"recipename"`
	Recipe     string          `json:"recipe"`
	ID         int             `json:"id"`
	Transcript string          `json:"transcript,omitempty"`
	Category   string          `json:"category,omitempty"`
	Structured *RecipeDocument `json:"structured,omitempty"`
}
