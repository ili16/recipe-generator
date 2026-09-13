package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// TestSoftDeleteAndRestore is BACKLOG 8.2's acceptance, against a real DB (skipped
// unless DB_URL is set): deleting hides the recipe from the list and from the
// meal-plan picker's ownership check, the week that already references it still
// renders, restoring brings it back with its version history intact, and another
// user's trashed id is not-found rather than restorable.
func TestSoftDeleteAndRestore(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID, otherUserID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-softdel-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-softdel-other') RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherUserID) })

	doc := model.RecipeDocument{
		Title:       "Test Soup",
		Language:    "en",
		Ingredients: []model.RecipeIngredient{{Item: "Water"}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Boil it"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Test Soup", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	if _, err := AddMealPlanItem(ctx, userID, recipe.ID, monday, "monday", "", nil); err != nil {
		t.Fatalf("AddMealPlanItem: %v", err)
	}

	if err := DeleteRecipe(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("DeleteRecipe: %v", err)
	}

	// Gone from the library...
	list, err := GetRecipes(userID)
	if err != nil {
		t.Fatalf("GetRecipes: %v", err)
	}
	for _, r := range list {
		if r.ID == recipe.ID {
			t.Fatal("deleted recipe still in GetRecipes")
		}
	}
	if _, err := GetRecipeByID(ctx, userID, recipe.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows from GetRecipeByID, got %v", err)
	}
	// ...and unassignable to a new day (the picker's ownership check).
	if _, err := AddMealPlanItem(ctx, userID, recipe.ID, monday.AddDate(0, 0, 1), "monday", "", nil); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows planning a deleted recipe, got %v", err)
	}

	// ...but the week that already holds it still renders.
	items, err := GetMealPlanRange(ctx, userID, monday, monday.AddDate(0, 0, 6))
	if err != nil {
		t.Fatalf("GetMealPlanRange: %v", err)
	}
	if len(items) != 1 || items[0].RecipeID != recipe.ID {
		t.Fatalf("deleted recipe broke the week render: %+v", items)
	}

	// It is in the trash, and restoring returns it with its history.
	trash, err := GetTrashedRecipes(ctx, userID)
	if err != nil {
		t.Fatalf("GetTrashedRecipes: %v", err)
	}
	if len(trash) != 1 || trash[0].ID != recipe.ID || trash[0].DeletedAt == nil {
		t.Fatalf("unexpected trash: %+v", trash)
	}

	if err := RestoreRecipe(ctx, otherUserID, recipe.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows restoring another user's recipe, got %v", err)
	}
	if err := RestoreRecipe(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("RestoreRecipe: %v", err)
	}
	// Restoring twice is not-found, not a silent success.
	if err := RestoreRecipe(ctx, userID, recipe.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows restoring twice, got %v", err)
	}

	back, err := GetRecipeByID(ctx, userID, recipe.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID after restore: %v", err)
	}
	if back.Recipename != "Test Soup" || back.Structured == nil || len(back.Structured.Steps) != 1 {
		t.Fatalf("restored recipe not intact: %+v", back)
	}
	versions, err := GetRecipeVersions(ctx, userID, recipe.ID)
	if err != nil || len(versions) != 1 {
		t.Fatalf("version history lost: %v, %+v", err, versions)
	}

	// Purge only takes rows past the retention window.
	if err := DeleteRecipe(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("DeleteRecipe (second): %v", err)
	}
	if n, err := PurgeDeletedRecipes(ctx, 30*24*time.Hour); err != nil || n != 0 {
		t.Fatalf("purge took a fresh row: n=%d err=%v", n, err)
	}
	if _, err := Pool.Exec(ctx, `UPDATE recipes SET deleted_at = NOW() - INTERVAL '31 days' WHERE id = $1`, recipe.ID); err != nil {
		t.Fatalf("age the row: %v", err)
	}
	if n, err := PurgeDeletedRecipes(ctx, 30*24*time.Hour); err != nil || n != 1 {
		t.Fatalf("purge missed an aged row: n=%d err=%v", n, err)
	}
}
