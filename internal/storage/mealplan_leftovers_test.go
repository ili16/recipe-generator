package storage

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestBatchCookAndLeftovers is a smoke test against a real DB (skipped unless DB_URL is
// set) for BACKLOG 9.11–9.13: the source_item_id link, its ON DELETE CASCADE, the
// household-size default on a newly planned day, and SetBatchCook in both directions.
// None of it is reachable from a type check — it is all SQL.
func TestBatchCookAndLeftovers(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx,
		`INSERT INTO users (oauth_id, household_size) VALUES ('test-batch-cook', 4) RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) }) //nolint:errcheck

	var recipeID int
	if err := Pool.QueryRow(ctx,
		`INSERT INTO recipes (user_id, title, content) VALUES ($1, 'Chili', '') RETURNING id`,
		userID,
	).Scan(&recipeID); err != nil {
		t.Fatalf("seed recipe: %v", err)
	}

	day := func(n int) time.Time { return time.Date(2026, 9, 20+n, 0, 0, 0, 0, time.UTC) }

	// A newly planned day starts from the household size, not from the recipe's yield
	// (BACKLOG 9.12).
	items, err := ApplyMealPlanDays(ctx, userID, "monday", []PlannedDay{{RecipeID: recipeID, PlannedOn: day(0)}})
	if err != nil {
		t.Fatalf("ApplyMealPlanDays: %v", err)
	}
	cook := items[0]
	if cook.Servings == nil || *cook.Servings != 4 {
		t.Fatalf("servings: got %v, want 4 (the household size)", cook.Servings)
	}

	// Cook once, eat three days: the cook scales to 4 × 3 and two leftover rows appear.
	twelve := 12
	covered, err := SetBatchCook(ctx, userID, cook.ID, []time.Time{day(1), day(2)}, &twelve)
	if err != nil {
		t.Fatalf("SetBatchCook: %v", err)
	}
	if len(covered) != 3 {
		t.Fatalf("covered: got %d items, want 3 (the cook plus two leftovers)", len(covered))
	}
	for _, it := range covered[1:] {
		if it.SourceItemID == nil || *it.SourceItemID != cook.ID {
			t.Errorf("%s: source_item_id %v, want %d", it.PlannedOn, it.SourceItemID, cook.ID)
		}
	}

	// Re-confirming the same days changes nothing, ids included.
	again, err := SetBatchCook(ctx, userID, cook.ID, []time.Time{day(1), day(2)}, &twelve)
	if err != nil {
		t.Fatalf("SetBatchCook (idempotent): %v", err)
	}
	if again[1].ID != covered[1].ID || again[2].ID != covered[2].ID {
		t.Errorf("re-confirming rewrote rows: %v then %v", []int{covered[1].ID, covered[2].ID}, []int{again[1].ID, again[2].ID})
	}

	// Unticking a day is the undo: that leftover goes, the cook scales back down.
	eight := 8
	if _, err = SetBatchCook(ctx, userID, cook.ID, []time.Time{day(1)}, &eight); err != nil {
		t.Fatalf("SetBatchCook (untick): %v", err)
	}
	got, err := GetMealPlanRange(ctx, userID, day(0), day(6))
	if err != nil {
		t.Fatalf("GetMealPlanRange: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after unticking: got %d items, want 2", len(got))
	}
	if got[0].Servings == nil || *got[0].Servings != 8 {
		t.Errorf("cook servings after unticking: got %v, want 8", got[0].Servings)
	}

	// A leftover may not itself become a cook — the pot it came from is what scales.
	if _, err = SetBatchCook(ctx, userID, got[1].ID, []time.Time{day(3)}, nil); err == nil {
		t.Error("SetBatchCook on a leftover: want an error, got none")
	}

	// Clearing the cook clears its leftovers: you did not cook it (BACKLOG 9.11).
	if err = DeleteMealPlanItem(ctx, userID, cook.ID); err != nil {
		t.Fatalf("DeleteMealPlanItem: %v", err)
	}
	if got, err = GetMealPlanRange(ctx, userID, day(0), day(6)); err != nil {
		t.Fatalf("GetMealPlanRange (after delete): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("after clearing the cook: got %d items, want 0", len(got))
	}
}
