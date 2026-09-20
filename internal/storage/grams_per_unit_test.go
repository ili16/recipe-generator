package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// BACKLOG 17.3: the client's servings scaler renders "1.5 onions (~225 g)", and the gram
// half of that has to come from the server -- the app has no conversion table. This is
// the read path carrying it, including the size modifier, and leaving it nil rather than
// guessing when nothing can weigh the line.
func TestGetRecipeCarriesGramsPerUnit(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-grams-per-unit') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	if err := UpsertGrams(ctx, []model.GramsRow{
		{ItemUnit: model.ItemUnit{Item: "onion", Unit: ""}, Grams: 150, Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertGrams: %v", err)
	}
	// ingredient_grams is global, so a row left behind is a row every other test in this
	// package sees — TestMarkMealCooked's whole point is that nothing knows what an onion
	// weighs, and this test would quietly break it.
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM ingredient_grams WHERE name = 'onion' AND source = 'test'`) })

	doc := model.RecipeDocument{
		Title: "Scaler Soup", Language: "en", Servings: i(4),
		Ingredients: []model.RecipeIngredient{
			{Item: "onion", Quantity: f(1)},
			{Item: "large onion", Quantity: f(1)},
			{Item: "quince", Quantity: f(1)},
		},
		Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Simmer"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Scaler Soup", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	got, err := GetRecipeByID(ctx, userID, recipe.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID: %v", err)
	}
	if got.Structured == nil || len(got.Structured.Ingredients) != 3 {
		t.Fatalf("want 3 structured ingredients, got %+v", got.Structured)
	}
	ings := got.Structured.Ingredients

	if ings[0].GramsPerUnit == nil || *ings[0].GramsPerUnit != 150 {
		t.Errorf("onion = %v, want 150", ings[0].GramsPerUnit)
	}
	// The size modifier rides along, so the scaler's gram hint is the large one's weight.
	if ings[1].GramsPerUnit == nil || *ings[1].GramsPerUnit != 202.5 {
		t.Errorf("large onion = %v, want 202.5", ings[1].GramsPerUnit)
	}
	// Nothing knows what a quince weighs, and nil is how the client is told not to guess.
	if ings[2].GramsPerUnit != nil {
		t.Errorf("quince = %v, want nil", *ings[2].GramsPerUnit)
	}
}
