package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/storage"
)

// TestAddPantryItems: the scan's write half lands normalised rows, folds a re-add into
// one line, and hands back a bad line instead of losing the good ones. The scan half
// itself is an LLM call and is not exercised here; what is checked is everything that
// happens to its output afterwards.
func TestAddPantryItems(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	storage.InitDBPool()
	ctx := context.Background()

	var userID int
	if err := storage.Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-pantry-scan') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })
	s := newTestSession(userID, testDraft)

	args, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"name": "Onions", "quantity": 3, "unit": "piece", "category": "produce"},
		{"name": "milk", "quantity": 1, "unit": "l", "category": "fridge"},
		{"name": "   ", "category": "fridge"},
	}})
	out, _, err := dispatch(ctx, s, "add_pantry_items", string(args))
	if err != nil {
		t.Fatalf("add_pantry_items: %v", err)
	}
	result := out.(map[string]any)
	if n := len(result["skipped"].([]string)); n != 1 {
		t.Fatalf("skipped = %d, want the blank name only", n)
	}

	items, err := storage.ListPantryItems(ctx, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("pantry has %d items, want 2", len(items))
	}

	// A litre of milk is 1000 ml on the row: the same normalisation the pantry screen
	// does, which is what lets a recipe asking for 200 ml match it.
	byName := map[string]float64{}
	for _, it := range items {
		if it.Quantity != nil {
			byName[it.Name] = *it.Quantity
		}
	}
	if byName["milk"] != 1000 {
		t.Fatalf("milk = %v, want 1000 (ml)", byName["milk"])
	}
	if byName["onions"] != 3 {
		t.Fatalf("onions = %v, want 3 pieces under a lowercased name", byName)
	}

	// Re-adding milk replaces the amount rather than adding a second line.
	again, _ := json.Marshal(map[string]any{"items": []map[string]any{
		{"name": "milk", "quantity": 2, "unit": "l", "category": "fridge"},
	}})
	if _, _, err := dispatch(ctx, s, "add_pantry_items", string(again)); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	items, _ = storage.ListPantryItems(ctx, userID)
	if len(items) != 2 {
		t.Fatalf("re-adding milk made %d lines, want 2", len(items))
	}

	if _, _, err := dispatch(ctx, s, "add_pantry_items", `{"items":[]}`); err == nil {
		t.Fatal("an empty list was accepted")
	}
}

// TestScanPantryNeedsTheAttachedPhoto: a ref the turn never carried is refused rather
// than guessed at — the model inventing "image1" must not reach the vision call.
func TestScanPantryNeedsTheAttachedPhoto(t *testing.T) {
	s := newTestSession(1, testDraft)
	if _, _, err := dispatch(context.Background(), s, "scan_pantry", `{"image_ref":"image1"}`); err == nil {
		t.Fatal("an unattached image_ref was accepted")
	}
}
