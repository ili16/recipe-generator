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

// TestPlanShareLink is BACKLOG 13.2's plan half against a real DB (skipped unless DB_URL
// is set): sharing a week resolves the token to that owner and range with no user id,
// re-sharing keeps the link already sent, and revoking kills it.
func TestPlanShareLink(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-plan-share-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	monday := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	sunday := monday.AddDate(0, 0, 6)

	if _, _, _, err := GetSharedPlan(ctx, ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("empty token: want ErrNoRows, got %v", err)
	}

	token, err := SharePlan(ctx, userID, monday, sunday)
	if err != nil || token == "" {
		t.Fatalf("SharePlan: token=%q err=%v", token, err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM meal_plans WHERE user_id = $1`, userID) })

	again, err := SharePlan(ctx, userID, monday, sunday)
	if err != nil || again != token {
		t.Fatalf("re-share: want %q, got %q (err %v)", token, again, err)
	}

	// The week reports its own token back, so "stop sharing" survives a reload.
	held, err := PlanShareToken(ctx, userID, monday.AddDate(0, 0, -1), sunday)
	if err != nil || held != token {
		t.Fatalf("PlanShareToken: want %q, got %q (err %v)", token, held, err)
	}

	owner, gotStart, gotEnd, err := GetSharedPlan(ctx, token)
	if err != nil {
		t.Fatalf("GetSharedPlan: %v", err)
	}
	if owner != userID || !gotStart.Equal(monday) || !gotEnd.Equal(sunday) {
		t.Fatalf("shared plan mismatch: owner=%d %v–%v", owner, gotStart, gotEnd)
	}

	if err := UnsharePlan(ctx, userID, monday); err != nil {
		t.Fatalf("UnsharePlan: %v", err)
	}
	if _, _, _, err := GetSharedPlan(ctx, token); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("revoked token: want ErrNoRows, got %v", err)
	}
	if held, err := PlanShareToken(ctx, userID, monday.AddDate(0, 0, -1), sunday); err != nil || held != "" {
		t.Fatalf("PlanShareToken after revoke: want \"\", got %q (err %v)", held, err)
	}
}
