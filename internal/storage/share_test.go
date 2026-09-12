package storage

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// TestShareLink is BACKLOG 8.4's acceptance against a real DB (skipped unless DB_URL is
// set): a shared token resolves without any user id, revoking kills it, an unshared
// recipe has no token to resolve, and a non-owner cannot share someone else's recipe.
func TestShareLink(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID, otherUserID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-share-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-share-other') RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherUserID) })

	doc := model.RecipeDocument{
		Title:       "Shared Stew",
		Language:    "en",
		Ingredients: []model.RecipeIngredient{{Item: "Beans"}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Simmer"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Shared Stew", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	// Unshared: nothing resolves, and an empty token is not a wildcard.
	if _, err := GetSharedRecipe(ctx, ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("empty token: want ErrNoRows, got %v", err)
	}

	// Another user cannot share it.
	if _, err := ShareRecipe(ctx, otherUserID, recipe.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("non-owner share: want ErrNoRows, got %v", err)
	}

	token, err := ShareRecipe(ctx, userID, recipe.ID)
	if err != nil || token == "" {
		t.Fatalf("ShareRecipe: token=%q err=%v", token, err)
	}

	// Re-sharing keeps the link already sent working.
	again, err := ShareRecipe(ctx, userID, recipe.ID)
	if err != nil || again != token {
		t.Fatalf("re-share: want %q, got %q (err %v)", token, again, err)
	}

	shared, err := GetSharedRecipe(ctx, token)
	if err != nil {
		t.Fatalf("GetSharedRecipe: %v", err)
	}
	if shared.ID != recipe.ID || shared.Recipename != "Shared Stew" {
		t.Fatalf("shared recipe mismatch: %+v", shared)
	}
	if shared.Structured == nil || len(shared.Structured.Steps) != 1 {
		t.Fatalf("shared recipe lost its structure: %+v", shared.Structured)
	}

	if err := UnshareRecipe(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("UnshareRecipe: %v", err)
	}
	if _, err := GetSharedRecipe(ctx, token); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked token: want ErrNoRows, got %v", err)
	}

	// A trashed recipe stops resolving even while shared.
	newToken, err := ShareRecipe(ctx, userID, recipe.ID)
	if err != nil {
		t.Fatalf("re-share after revoke: %v", err)
	}
	if newToken == token {
		t.Fatalf("revoke then share reissued the same token")
	}
	if err := DeleteRecipe(ctx, userID, recipe.ID); err != nil {
		t.Fatalf("DeleteRecipe: %v", err)
	}
	if _, err := GetSharedRecipe(ctx, newToken); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("trashed share: want ErrNoRows, got %v", err)
	}
}
