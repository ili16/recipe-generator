package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/storage"
)

// TestRenameRecipe: the rename lands on the row and is rejected for a blank title.
// Runs against a real DB — skipped unless DB_URL is set.
func TestRenameRecipe(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	storage.InitDBPool()
	ctx := context.Background()

	var userID int
	if err := storage.Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-rename') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	doc := testDraft
	doc.Title = "Half-Batch Crème Brûlée"
	saved, err := storage.AddRecipeStructured(ctx, userID, doc, "# x", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("seed recipe: %v", err)
	}
	s := newTestSession(userID, doc)

	args, _ := json.Marshal(map[string]any{"id": saved.ID, "title": "Crème Brûlée"})
	if _, _, err := dispatch(ctx, s, "rename_recipe", string(args)); err != nil {
		t.Fatalf("rename_recipe: %v", err)
	}
	got, err := storage.GetRecipeByID(ctx, userID, saved.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Structured.Title != "Crème Brûlée" {
		t.Fatalf("title = %q, want %q", got.Structured.Title, "Crème Brûlée")
	}
	if len(got.Structured.Ingredients) != len(doc.Ingredients) || len(got.Structured.Steps) != len(doc.Steps) {
		t.Fatalf("rename lost content: %d ingredients, %d steps", len(got.Structured.Ingredients), len(got.Structured.Steps))
	}

	blank, _ := json.Marshal(map[string]any{"id": saved.ID, "title": "  "})
	if _, _, err := dispatch(ctx, s, "rename_recipe", string(blank)); err == nil {
		t.Fatal("blank title was accepted")
	}
}
