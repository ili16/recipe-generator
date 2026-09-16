package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestPlannedIngredientsExclusions covers the two WHERE clauses the grocery list gained,
// plus StapleNames — the SQL the unit tests for BuildGroceryList cannot reach.
//
// The leftover case is a regression test for a real bug: a dish batch-cooked across three
// days was putting 3x its ingredients on the shopping list, because each covered day is
// its own row while the cook's servings already cover all of them.
func TestPlannedIngredientsExclusions(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-grocery-scope') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	newRecipe := func(title, ingredient string) model.Recipe {
		doc := model.RecipeDocument{
			Title: title, Language: "en", Servings: i(2),
			Ingredients: []model.RecipeIngredient{
				{Item: ingredient, Quantity: f(100), Unit: u("g")},
				{Item: "Salt", Quantity: f(5), Unit: u("g")},
			},
			Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Cook"}},
		}
		r, err := AddRecipeStructured(ctx, userID, doc, "# "+title, nil, nil, "test-model", "v1", nil)
		if err != nil {
			t.Fatalf("AddRecipeStructured %s: %v", title, err)
		}
		t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, r.ID) })
		return r
	}

	stew := newRecipe("Scope Stew", "Beef")
	pie := newRecipe("Scope Pie", "Apple")
	soup := newRecipe("Scope Soup", "Leek")

	// A fixed future week, so nothing here depends on what day the suite runs.
	monday := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return monday.AddDate(0, 0, n) }

	cook, err := AddMealPlanItem(ctx, userID, stew.ID, day(0), "monday", "dinner", i(2))
	if err != nil {
		t.Fatalf("plan stew: %v", err)
	}
	if _, err := AddMealPlanItem(ctx, userID, pie.ID, day(1), "monday", "dinner", i(2)); err != nil {
		t.Fatalf("plan pie: %v", err)
	}
	cooked, err := AddMealPlanItem(ctx, userID, soup.ID, day(2), "monday", "dinner", i(2))
	if err != nil {
		t.Fatalf("plan soup: %v", err)
	}
	// Monday's pot also covers Thursday — one cook, two days, one shopping trip.
	if _, err := SetBatchCook(ctx, userID, cook.ID, []time.Time{day(3)}, i(4)); err != nil {
		t.Fatalf("SetBatchCook: %v", err)
	}

	items := func() map[string]int {
		ings, err := GetPlannedIngredients(ctx, userID, day(0), day(6))
		if err != nil {
			t.Fatalf("GetPlannedIngredients: %v", err)
		}
		counts := map[string]int{}
		for _, ing := range ings {
			counts[model.NormalizeItem(ing.Item)]++
		}
		return counts
	}

	before := items()
	// Beef appears once, not twice: Thursday is the same pot as Monday.
	if before["beef"] != 1 {
		t.Errorf("a batch-cooked dish must be shopped for once, got %d beef rows", before["beef"])
	}
	if before["apple"] != 1 || before["leek"] != 1 {
		t.Errorf("both other meals should be on the list: %+v", before)
	}

	// Cooking the soup takes it off the list; the others are untouched.
	if _, _, err := MarkMealCooked(ctx, userID, cooked.ID, nil); err != nil {
		t.Fatalf("MarkMealCooked: %v", err)
	}
	after := items()
	if after["leek"] != 0 {
		t.Errorf("a cooked meal must leave the grocery list, got %d leek rows", after["leek"])
	}
	if after["apple"] != 1 || after["beef"] != 1 {
		t.Errorf("cooking one meal must not disturb the others: %+v", after)
	}

	// Staples are read separately and dropped by BuildGroceryList; salt is on every
	// recipe above precisely so this has something to find.
	if _, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "Salt", Category: "spices_dry", Staple: true}); err != nil {
		t.Fatalf("UpsertPantryItem: %v", err)
	}
	if _, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "Beef", Quantity: f(500), Unit: u("g"), Category: "fridge"}); err != nil {
		t.Fatalf("UpsertPantryItem: %v", err)
	}
	staples, err := StapleNames(ctx, userID)
	if err != nil {
		t.Fatalf("StapleNames: %v", err)
	}
	if !staples["salt"] {
		t.Error("salt was marked a staple and must come back as one")
	}
	if staples["beef"] {
		t.Error("an ordinary pantry line is not a staple")
	}
}
