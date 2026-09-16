package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

func f(v float64) *float64 { return &v }
func u(s string) *string   { return &s }
func i(v int) *int         { return &v }

// TestMarkMealCooked is the deduction against a real DB (skipped unless DB_URL is set).
// The cases that matter are the three refusals, not the happy path: a staple untouched,
// an unconvertible amount left alone rather than estimated, and a second mark that
// deducts nothing.
func TestMarkMealCooked(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-deduct-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	// "onion" has no conversion anywhere, so the recipe's 1 onion cannot be weighed
	// against the pantry's 3 — that line must survive untouched.
	doc := model.RecipeDocument{
		Title: "Test Bake", Language: "en", Servings: i(2),
		Ingredients: []model.RecipeIngredient{
			{Item: "Flour", Quantity: f(200), Unit: u("g")},
			{Item: "Eggs", Quantity: f(2)},
			{Item: "Salt", QuantityText: u("to taste")},
			{Item: "Onion", Quantity: f(1)},
		},
		Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Bake"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Test Bake", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	// An egg is countable, so it only converts because the table says what one weighs.
	if err := UpsertGrams(ctx, []model.GramsRow{
		{ItemUnit: model.ItemUnit{Item: "eggs", Unit: ""}, Grams: 50, Source: "test"},
	}); err != nil {
		t.Fatalf("UpsertGrams: %v", err)
	}

	for _, item := range []model.PantryItem{
		{Name: "Flour", Quantity: f(1), Unit: u("kg"), Category: "spices_dry"},
		{Name: "Eggs", Quantity: f(6), Category: "fridge"},
		{Name: "Onion", Quantity: f(3), Category: "produce"},
		{Name: "Salt", Quantity: f(500), Unit: u("g"), Category: "spices_dry", Staple: true},
	} {
		if _, err := UpsertPantryItem(ctx, userID, item); err != nil {
			t.Fatalf("UpsertPantryItem %s: %v", item.Name, err)
		}
	}

	day := time.Now().Truncate(24 * time.Hour)
	planned, err := AddMealPlanItem(ctx, userID, recipe.ID, day, "monday", "dinner", i(2))
	if err != nil {
		t.Fatalf("AddMealPlanItem: %v", err)
	}

	applied, skipped, err := MarkMealCooked(ctx, userID, planned.ID, nil)
	if err != nil {
		t.Fatalf("MarkMealCooked: %v", err)
	}

	pantry := func() map[string]*float64 {
		items, err := ListPantryItems(ctx, userID)
		if err != nil {
			t.Fatalf("ListPantryItems: %v", err)
		}
		out := map[string]*float64{}
		for _, it := range items {
			out[it.Name] = it.Quantity
		}
		return out
	}
	have := pantry()

	// 1 kg was normalised to 1000 g on the way into the pantry (UpsertPantryItem), so
	// the row is in grams and 200 g comes straight off it.
	if got := have["flour"]; got == nil || *got != 800 {
		t.Errorf("flour should be 800 g, got %v", deref(got))
	}
	// 6 eggs minus 2, converted through 50 g each and back.
	if got := have["eggs"]; got == nil || *got != 4 {
		t.Errorf("eggs should be 4, got %v", deref(got))
	}
	// A staple is never measured and never deducted.
	if got := have["salt"]; got == nil || *got != 500 {
		t.Errorf("salt is a staple and must not move, got %v", deref(got))
	}
	// Nothing knows what an onion weighs, so nothing is guessed.
	if got := have["onion"]; got == nil || *got != 3 {
		t.Errorf("onion should be untouched at 3, got %v", deref(got))
	}
	if len(applied) != 2 {
		t.Errorf("want flour and eggs applied, got %+v", applied)
	}
	if len(skipped) != 1 || skipped[0] != "onion" {
		t.Errorf("onion should be reported as skipped, got %v", skipped)
	}

	// Marking again must find nothing to mark — the deduction cannot run twice.
	if _, _, err := MarkMealCooked(ctx, userID, planned.ID, nil); err == nil {
		t.Error("a second mark should not succeed")
	}
	if got := pantry()["eggs"]; got == nil || *got != 4 {
		t.Errorf("a second mark must not deduct again, eggs = %v", deref(got))
	}

	// Undo puts the meal back on the week; the pantry deliberately stays as it is.
	if err := UnmarkMealCooked(ctx, userID, planned.ID); err != nil {
		t.Fatalf("UnmarkMealCooked: %v", err)
	}
	items, err := GetMealPlanRange(ctx, userID, day, day)
	if err != nil || len(items) != 1 || items[0].CookedAt != nil {
		t.Errorf("undo should clear cooked_at, got %+v (err %v)", items, err)
	}
	if got := pantry()["eggs"]; got == nil || *got != 4 {
		t.Errorf("undo must not restore the pantry, eggs = %v", deref(got))
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
