package storage

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// TestUpdateRecipeStructuredTogglesManuallyEdited is a smoke test against a real DB
// (skipped unless DB_URL is set) for the manual-edit flag + full-doc overwrite path:
// AddRecipeStructured -> UpdateRecipeStructured(manual) -> UpdateRecipeStructured(AI-sourced).
func TestUpdateRecipeStructuredTogglesManuallyEdited(t *testing.T) {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-manual-edit-flag') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	doc := model.RecipeDocument{
		Title:       "Test Soup",
		Language:    "en",
		Tags:        []string{"vegan"},
		Ingredients: []model.RecipeIngredient{{Item: "Water", Optional: false}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Boil it"}},
	}
	created, err := AddRecipeStructured(ctx, userID, doc, "# Test Soup", nil, nil, "test-model", "v1")
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, created.ID) })

	if created.ManuallyEdited {
		t.Fatalf("freshly created recipe should not be manually_edited")
	}

	manualDoc := doc
	manualDoc.Title = "Test Soup (edited)"
	manualDoc.Ingredients = []model.RecipeIngredient{{Item: "Water"}, {Item: "Salt"}}
	updated, err := UpdateRecipeStructured(ctx, userID, created.ID, manualDoc, "# Test Soup (edited)", true, "")
	if err != nil {
		t.Fatalf("UpdateRecipeStructured(manual): %v", err)
	}
	if !updated.ManuallyEdited {
		t.Errorf("expected manually_edited=true after manual edit")
	}

	fetched, err := GetRecipeByID(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID: %v", err)
	}
	if !fetched.ManuallyEdited || fetched.Recipename != "Test Soup (edited)" || len(fetched.Structured.Ingredients) != 2 {
		t.Errorf("GetRecipeByID mismatch: manually_edited=%v name=%q ingredients=%d",
			fetched.ManuallyEdited, fetched.Recipename, len(fetched.Structured.Ingredients))
	}

	list, err := GetRecipes(userID)
	if err != nil {
		t.Fatalf("GetRecipes: %v", err)
	}
	if len(list) != 1 || !list[0].ManuallyEdited {
		t.Errorf("GetRecipes should surface manually_edited=true in the list view, got %+v", list)
	}

	aiDoc := doc
	aiDoc.Title = "Test Soup (AI refined)"
	final, err := UpdateRecipeStructured(ctx, userID, created.ID, aiDoc, "# Test Soup (AI refined)", false, "make it fancier")
	if err != nil {
		t.Fatalf("UpdateRecipeStructured(ai): %v", err)
	}
	if final.ManuallyEdited {
		t.Errorf("expected manually_edited=false after AI-sourced update clears the flag")
	}

	history, err := GetRecipeVersions(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("GetRecipeVersions: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 versions (extraction, manual, ai_edit), got %d", len(history))
	}
	// Newest first.
	if history[0].ChangeKind != "ai_edit" || history[0].ChangeNote != "make it fancier" || history[0].Data.Title != "Test Soup (AI refined)" {
		t.Errorf("unexpected latest version: %+v", history[0])
	}
	if history[1].ChangeKind != "manual" || history[1].ChangeNote != "" || history[1].Data.Title != "Test Soup (edited)" {
		t.Errorf("unexpected middle version: %+v", history[1])
	}
	if history[2].ChangeKind != "extraction" || history[2].Data.Title != "Test Soup" {
		t.Errorf("unexpected initial version: %+v", history[2])
	}

	if _, err := GetRecipeVersions(ctx, userID+1, created.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected pgx.ErrNoRows for a recipe owned by a different user, got %v", err)
	}
}
