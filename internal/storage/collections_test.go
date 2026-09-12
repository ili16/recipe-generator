package storage

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// TestCollections is BACKLOG 8.1's acceptance, against a real DB (skipped unless DB_URL
// is set): a recipe belongs to two collections and appears under both, deleting a
// collection leaves its recipes alone, and another user's collection id is not-found.
func TestCollections(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID, otherUserID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-coll-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-coll-other') RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherUserID) })

	doc := model.RecipeDocument{
		Title:       "Test Roast",
		Language:    "en",
		Ingredients: []model.RecipeIngredient{{Item: "Potato"}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Roast it"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Test Roast", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	sunday, err := CreateCollection(ctx, userID, "  Sunday  ")
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if sunday.Name != "Sunday" {
		t.Fatalf("name not trimmed: %q", sunday.Name)
	}
	weeknight, err := CreateCollection(ctx, userID, "Weeknight")
	if err != nil {
		t.Fatalf("CreateCollection (second): %v", err)
	}
	// Re-creating by name is idempotent, not a duplicate.
	if again, err := CreateCollection(ctx, userID, "Sunday"); err != nil || again.ID != sunday.ID {
		t.Fatalf("re-create should return the same collection: %+v %v", again, err)
	}
	if _, err := CreateCollection(ctx, userID, "   "); !errors.Is(err, ErrEmptyCollectionName) {
		t.Fatalf("expected ErrEmptyCollectionName, got %v", err)
	}

	// One recipe, two collections, visible under both.
	for _, c := range []model.Collection{sunday, weeknight} {
		if err := SetCollectionMembership(ctx, userID, c.ID, recipe.ID, true); err != nil {
			t.Fatalf("add to %s: %v", c.Name, err)
		}
	}
	// Adding twice is a no-op, not an error.
	if err := SetCollectionMembership(ctx, userID, sunday.ID, recipe.ID, true); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	list, err := ListCollections(ctx, userID)
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 collections, got %+v", list)
	}
	for _, c := range list {
		if len(c.RecipeIDs) != 1 || c.RecipeIDs[0] != recipe.ID {
			t.Fatalf("%s should hold the recipe once: %+v", c.Name, c.RecipeIDs)
		}
	}

	// Another user's ids are not-found on every write path.
	if err := SetCollectionMembership(ctx, otherUserID, sunday.ID, recipe.ID, true); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows adding to another user's collection, got %v", err)
	}
	if err := DeleteCollection(ctx, otherUserID, sunday.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows deleting another user's collection, got %v", err)
	}
	if cs, err := ListCollections(ctx, otherUserID); err != nil || len(cs) != 0 {
		t.Fatalf("other user should see no collections: %+v %v", cs, err)
	}

	if err := SetCollectionMembership(ctx, userID, weeknight.ID, recipe.ID, false); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := SetCollectionMembership(ctx, userID, weeknight.ID, recipe.ID, false); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows removing a non-member, got %v", err)
	}

	// A soft-deleted recipe drops out of its collections, and comes back on restore.
	if err := DeleteRecipe(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("DeleteRecipe: %v", err)
	}
	list, _ = ListCollections(ctx, userID)
	for _, c := range list {
		if len(c.RecipeIDs) != 0 {
			t.Fatalf("%s still lists a trashed recipe: %+v", c.Name, c.RecipeIDs)
		}
	}
	if err := RestoreRecipe(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("RestoreRecipe: %v", err)
	}

	// Deleting a collection takes the memberships, never the recipes.
	if err := DeleteCollection(ctx, userID, sunday.ID); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	if _, err := GetRecipeByID(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("recipe died with its collection: %v", err)
	}
	list, err = ListCollections(ctx, userID)
	if err != nil || len(list) != 1 || list[0].ID != weeknight.ID {
		t.Fatalf("unexpected collections after delete: %+v %v", list, err)
	}
}
