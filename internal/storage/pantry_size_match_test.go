package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// BACKLOG 17.1's acceptance, and the half no Go unit test reaches: the pantry match is a
// SQL join on normalize_item(), so Go stripping the size word is worth nothing unless the
// function in migration 00040 strips it too. A kitchen holding "onion" must cover a
// recipe written as "2 medium onions" -- before this, it covered none of them.
func TestPantryMatchIgnoresSize(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-size-match') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	doc := model.RecipeDocument{
		Title: "Sized Soup", Language: "en", Servings: i(2),
		Ingredients: []model.RecipeIngredient{
			{Item: "medium onion", Quantity: f(2)},
			{Item: "große Zwiebel", Quantity: f(1)},
			{Item: "Quince", Quantity: f(1)},
		},
		Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Simmer"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Sized Soup", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	// Bare names, exactly as a person would type them into the pantry.
	for _, item := range []model.PantryItem{
		{Name: "Onion", Quantity: f(3), Category: "produce"},
		{Name: "Zwiebel", Quantity: f(2), Category: "produce"},
	} {
		if _, err := UpsertPantryItem(ctx, userID, item); err != nil {
			t.Fatalf("UpsertPantryItem %s: %v", item.Name, err)
		}
	}

	recipes, err := GetRecipes(userID)
	if err != nil {
		t.Fatalf("GetRecipes: %v", err)
	}
	var got *model.Recipe
	for i := range recipes {
		if recipes[i].ID == recipe.ID {
			got = &recipes[i]
		}
	}
	if got == nil {
		t.Fatal("recipe not returned")
	}
	// Three ingredients; the two sized onions are covered, the quince is not.
	if got.PantryTotal != 3 || got.PantryHave != 2 {
		t.Errorf("pantry match = %d of %d, want 2 of 3 (sized names must match bare ones)",
			got.PantryHave, got.PantryTotal)
	}
}
