package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestCookFlowRoundTrips is a smoke test against a real DB (skipped unless DB_URL is set)
// for the two columns migration 00032 added: a step's phase and its parallel group survive
// a write and a read.
//
// Worth its own test because the failure is silent. An insert and a select that have
// drifted apart lose the flow without erroring anywhere: cooking mode just quietly goes
// back to walking a flat list, which is exactly what it looked like before the feature.
// The nil case is here for the same reason — every recipe saved before this existed has
// NULL in both columns and must keep reading back as "no flow", not as a zero-valued one.
func TestCookFlowRoundTrips(t *testing.T) {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-cook-flow') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	phase := func(s string) *string { return &s }
	group := func(i int) *int { return &i }

	doc := model.RecipeDocument{
		Title:    "Test Pasta",
		Language: "en",
		Ingredients: []model.RecipeIngredient{
			{Item: "Pasta"}, {Item: "Onion"},
		},
		Steps: []model.RecipeStep{
			// A parallel block: the water goes on while the onion is diced.
			{SortOrder: 1, StepText: "Boil salted water", Phase: phase("cook"), ParallelGroup: group(1), IngredientIndices: []int{0}},
			{SortOrder: 2, StepText: "Dice the onion", Phase: phase("prep"), ParallelGroup: group(1), IngredientIndices: []int{1}},
			// A step on its own, and one with no flow at all (the legacy shape).
			{SortOrder: 3, StepText: "Rest off the heat", Phase: phase("wait")},
			{SortOrder: 4, StepText: "Serve"},
		},
	}
	created, err := AddRecipeStructured(ctx, userID, doc, "# Test Pasta", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, created.ID) })

	got, err := GetRecipeByID(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID: %v", err)
	}
	if got.Structured == nil {
		t.Fatal("no structured document came back")
	}
	steps := got.Structured.Steps
	if len(steps) != 4 {
		t.Fatalf("got %d steps, want 4", len(steps))
	}

	deref := func(s *string) string {
		if s == nil {
			return "<nil>"
		}
		return *s
	}
	derefInt := func(i *int) string {
		if i == nil {
			return "<nil>"
		}
		return string(rune('0' + *i))
	}

	for i, want := range doc.Steps {
		if deref(steps[i].Phase) != deref(want.Phase) {
			t.Errorf("step %d phase = %s, want %s", i+1, deref(steps[i].Phase), deref(want.Phase))
		}
		if derefInt(steps[i].ParallelGroup) != derefInt(want.ParallelGroup) {
			t.Errorf("step %d parallel_group = %s, want %s", i+1, derefInt(steps[i].ParallelGroup), derefInt(want.ParallelGroup))
		}
	}

	// The flow must survive an edit too — UpdateRecipeStructured deletes and re-inserts
	// the steps, so it is a second, independent copy of the write path.
	edited := doc
	edited.Steps[3].Phase = phase("cook")
	updated, err := UpdateRecipeStructured(ctx, userID, created.ID, edited, "# Test Pasta", false, "")
	if err != nil {
		t.Fatalf("UpdateRecipeStructured: %v", err)
	}
	if updated.Structured == nil || len(updated.Structured.Steps) != 4 {
		t.Fatal("edit lost the steps")
	}
	if deref(updated.Structured.Steps[3].Phase) != "cook" {
		t.Errorf("after edit, step 4 phase = %s, want cook", deref(updated.Structured.Steps[3].Phase))
	}
	if deref(updated.Structured.Steps[0].Phase) != "cook" || derefInt(updated.Structured.Steps[0].ParallelGroup) != "1" {
		t.Errorf("after edit, step 1 lost its flow: phase=%s group=%s",
			deref(updated.Structured.Steps[0].Phase), derefInt(updated.Structured.Steps[0].ParallelGroup))
	}
}
