package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestMarkCooked is BACKLOG 6.3's storage half against a real DB (skipped unless DB_URL
// is set): the mark round-trips onto the list payload, clearing it works, and another
// user's recipe is not-found rather than markable.
func TestMarkCooked(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID, otherUserID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-cooked-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-cooked-other') RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherUserID) })

	doc := model.RecipeDocument{
		Title:       "Test Stew",
		Language:    "en",
		Ingredients: []model.RecipeIngredient{{Item: "Water"}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Simmer"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Test Stew", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	lastCooked := func() *string {
		list, err := GetRecipes(userID)
		if err != nil {
			t.Fatalf("GetRecipes: %v", err)
		}
		for _, r := range list {
			if r.ID == recipe.ID {
				if r.LastCookedAt == nil {
					return nil
				}
				s := r.LastCookedAt.String()
				return &s
			}
		}
		t.Fatalf("recipe %d missing from list", recipe.ID)
		return nil
	}

	if lastCooked() != nil {
		t.Fatal("a fresh recipe should have no cooked mark")
	}
	if found, err := MarkCooked(ctx, userID, recipe.ID); err != nil || !found {
		t.Fatalf("MarkCooked: found=%v err=%v", found, err)
	}
	if lastCooked() == nil {
		t.Fatal("cooked mark did not reach the list payload")
	}

	// Someone else's recipe is not-found, and does not clear the owner's mark.
	if found, err := MarkCooked(ctx, otherUserID, recipe.ID); err != nil || found {
		t.Fatalf("expected not-found for a foreign recipe: found=%v err=%v", found, err)
	}
	if err := UnmarkCooked(ctx, otherUserID, recipe.ID); err != nil {
		t.Fatalf("UnmarkCooked (foreign): %v", err)
	}
	if lastCooked() == nil {
		t.Fatal("a foreign unmark cleared the owner's mark")
	}

	if err := UnmarkCooked(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("UnmarkCooked: %v", err)
	}
	if lastCooked() != nil {
		t.Fatal("unmark left the cooked mark in place")
	}
}
