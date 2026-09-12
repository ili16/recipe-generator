package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestGetPlannedIngredients covers the read half of BACKLOG 6.1: the join reaches the
// ingredients of the recipes planned in the range, carries the category the generation
// call emitted, and stops at the range's edges and the user's own rows.
func TestGetPlannedIngredients(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-grocery') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	s := func(v string) *string { return &v }
	f := func(v float64) *float64 { return &v }
	doc := model.RecipeDocument{
		Title:    "Grocery Test Roast",
		Language: "en",
		Ingredients: []model.RecipeIngredient{
			{Item: "Flour", Quantity: f(250), Unit: s("g"), Category: s("pantry")},
			{Item: "Salt", QuantityText: s("to taste")},
		},
		Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Roast it"}},
	}
	created, err := AddRecipeStructured(ctx, userID, doc, "# Roast", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, created.ID) })

	day := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	if _, err := AddMealPlanItem(ctx, userID, created.ID, day, "18:00:00", "monday", "", nil); err != nil {
		t.Fatalf("AddMealPlanItem: %v", err)
	}

	got, err := GetPlannedIngredients(ctx, userID, day, day)
	if err != nil {
		t.Fatalf("GetPlannedIngredients: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want both ingredients, got %d: %+v", len(got), got)
	}
	if got[0].Item != "Flour" || got[0].Category == nil || *got[0].Category != "pantry" || got[0].Quantity == nil || *got[0].Quantity != 250 {
		t.Errorf("first row wrong: %+v", got[0])
	}
	if got[0].PlannedOn != "2026-09-14" || got[0].RecipeTitle != "Grocery Test Roast" {
		t.Errorf("provenance wrong: %+v", got[0])
	}
	if got[1].Quantity != nil || got[1].QuantityText == nil || *got[1].QuantityText != "to taste" {
		t.Errorf("unparseable amount should survive as text: %+v", got[1])
	}

	// A day outside the range is not on this week's list.
	outside, err := GetPlannedIngredients(ctx, userID, day.AddDate(0, 0, 1), day.AddDate(0, 0, 2))
	if err != nil {
		t.Fatalf("GetPlannedIngredients (outside): %v", err)
	}
	if len(outside) != 0 {
		t.Errorf("range should exclude other days, got %+v", outside)
	}
}
