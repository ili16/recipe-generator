package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestNutritionRoundTrip covers BACKLOG 6.6's persistence half against a real DB
// (skipped unless DB_URL is set): per-serving macros survive insert and update, and a
// missing estimate stays NULL rather than becoming 0 — "unknown" must never render as
// a confident zero.
func TestNutritionRoundTrip(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-nutrition') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	i := func(v int) *int { return &v }
	doc := model.RecipeDocument{
		Title:       "Macro Soup",
		Language:    "en",
		Calories:    i(480),
		ProteinG:    i(42),
		CarbsG:      nil, // the model couldn't estimate this one
		FatG:        i(11),
		Ingredients: []model.RecipeIngredient{{Item: "Water"}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Boil it"}},
	}
	created, err := AddRecipeStructured(ctx, userID, doc, "# Macro Soup", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, created.ID) })

	got, err := GetRecipeByID(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID: %v", err)
	}
	if got.Structured.Calories == nil || *got.Structured.Calories != 480 ||
		got.Structured.ProteinG == nil || *got.Structured.ProteinG != 42 ||
		got.Structured.FatG == nil || *got.Structured.FatG != 11 {
		t.Errorf("macros lost on insert: %+v", got.Structured)
	}
	if got.Structured.CarbsG != nil {
		t.Errorf("unestimated carbs_g must stay nil, got %d", *got.Structured.CarbsG)
	}

	// BACKLOG 6.8: calories (and only calories) rides the list payload so the card can
	// render a kcal badge without fetching the document.
	list, err := GetRecipes(userID)
	if err != nil {
		t.Fatalf("GetRecipes: %v", err)
	}
	var listed *model.Recipe
	for i := range list {
		if list[i].ID == created.ID {
			listed = &list[i]
		}
	}
	if listed == nil {
		t.Fatalf("recipe %d missing from list payload", created.ID)
	}
	if listed.Calories == nil || *listed.Calories != 480 {
		t.Errorf("calories missing from list payload: %+v", listed.Calories)
	}

	// An edit that drops an estimate must clear the column, not keep the stale value.
	edited := doc
	edited.Calories = i(500)
	edited.ProteinG = nil
	if _, err := UpdateRecipeStructured(ctx, userID, created.ID, edited, "# Macro Soup", true, ""); err != nil {
		t.Fatalf("UpdateRecipeStructured: %v", err)
	}
	got, err = GetRecipeByID(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID after update: %v", err)
	}
	if got.Structured.Calories == nil || *got.Structured.Calories != 500 || got.Structured.ProteinG != nil {
		t.Errorf("macros wrong after update: %+v", got.Structured)
	}
}
