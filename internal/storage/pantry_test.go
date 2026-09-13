package storage

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

func ptrF(f float64) *float64 { return &f }
func ptrS(s string) *string   { return &s }

// TestPantryItems is BACKLOG 7.1's acceptance, against a real DB (skipped unless DB_URL
// is set): items round-trip, name and unit are normalised the way the grocery list
// normalises them, re-adding an item updates it instead of duplicating it, and another
// user's item id is not-found rather than that item.
func TestPantryItems(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID, otherUserID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-pantry-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-pantry-other') RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherUserID) })

	// "  Flour  " in kg is stored as "flour" in g — the same key the grocery list
	// consolidates on, which is what makes 7.3's match a plain comparison.
	flour, err := UpsertPantryItem(ctx, userID, model.PantryItem{
		Name: "  Flour  ", Quantity: ptrF(1.5), Unit: ptrS("kg"), Category: "spices_dry",
	})
	if err != nil {
		t.Fatalf("UpsertPantryItem: %v", err)
	}
	if flour.Name != "flour" || flour.Unit == nil || *flour.Unit != "g" || flour.Quantity == nil || *flour.Quantity != 1500 {
		t.Fatalf("not normalised: %+v", flour)
	}

	// Same item, new amount: one row, updated — not a second line.
	again, err := UpsertPantryItem(ctx, userID, model.PantryItem{
		Name: "FLOUR", Quantity: ptrF(800), Unit: ptrS("g"), Category: "spices_dry", ExpiresOn: ptrS("2026-10-01"),
	})
	if err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if again.ID != flour.ID || *again.Quantity != 800 || again.ExpiresOn == nil || *again.ExpiresOn != "2026-10-01" {
		t.Fatalf("re-adding should update the same row: %+v", again)
	}

	// A unitless item ("2 eggs") is its own row and must still collide with itself.
	eggs, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "eggs", Quantity: ptrF(6), Category: "fridge"})
	if err != nil {
		t.Fatalf("UpsertPantryItem (unitless): %v", err)
	}
	if eggs.ID == flour.ID || eggs.Unit != nil {
		t.Fatalf("unitless item: %+v", eggs)
	}
	if dup, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "eggs", Quantity: ptrF(12), Category: "fridge"}); err != nil || dup.ID != eggs.ID {
		t.Fatalf("unitless re-add should update the same row: %+v %v", dup, err)
	}

	// An unknown category falls back to the default rather than being written through.
	odd, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "saffron", Category: "cupboard-ish"})
	if err != nil {
		t.Fatalf("UpsertPantryItem (bad category): %v", err)
	}
	if odd.Category != "spices_dry" {
		t.Fatalf("unknown category should default: %q", odd.Category)
	}

	if _, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "   "}); !errors.Is(err, ErrEmptyPantryItemName) {
		t.Fatalf("blank name should be ErrEmptyPantryItemName, got %v", err)
	}
	if _, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "milk", ExpiresOn: ptrS("next tuesday")}); !errors.Is(err, ErrInvalidExpiry) {
		t.Fatalf("bad date should be ErrInvalidExpiry, got %v", err)
	}

	items, err := ListPantryItems(ctx, userID)
	if err != nil {
		t.Fatalf("ListPantryItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d: %+v", len(items), items)
	}
	// Expiring first, undated last (7.4's reading order).
	if items[0].ID != flour.ID {
		t.Fatalf("dated item should sort first: %+v", items)
	}

	// Another user's id is indistinguishable from a missing one.
	if err := DeletePantryItem(ctx, otherUserID, flour.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("other user's delete should be ErrNoRows, got %v", err)
	}
	if left, err := ListPantryItems(ctx, userID); err != nil || len(left) != 3 {
		t.Fatalf("item must survive another user's delete: %d %v", len(left), err)
	}
	if err := DeletePantryItem(ctx, userID, flour.ID); err != nil {
		t.Fatalf("DeletePantryItem: %v", err)
	}
	if err := DeletePantryItem(ctx, userID, flour.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second delete should be ErrNoRows, got %v", err)
	}
}

// TestPantryMatch is BACKLOG 7.3's acceptance: a recipe whose ingredients are all in the
// pantry reports 100%, and a different unit form of the same ingredient ("250 g flour" in
// the recipe vs "2 cups flour" in the pantry) still counts as a match.
func TestPantryMatch(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-pantry-match') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	doc := model.RecipeDocument{
		Title:    "Pancakes",
		Language: "en",
		Ingredients: []model.RecipeIngredient{
			{Item: "Flour", Quantity: ptrF(250), Unit: ptrS("g")},
			{Item: "Milk", Quantity: ptrF(300), Unit: ptrS("ml")},
		},
		Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Mix"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Pancakes", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	match := func() (have, total int) {
		recipes, err := GetRecipes(userID)
		if err != nil {
			t.Fatalf("GetRecipes: %v", err)
		}
		for _, r := range recipes {
			if r.ID == recipe.ID {
				return r.PantryHave, r.PantryTotal
			}
		}
		t.Fatalf("recipe %d missing from GetRecipes", recipe.ID)
		return 0, 0
	}

	// An empty pantry is 0 of 2 — the client's cue to show no badge, not a 0% one.
	if have, total := match(); have != 0 || total != 2 {
		t.Fatalf("empty pantry: have %d of %d, want 0 of 2", have, total)
	}

	// "2 cups flour" is a unit the recipe never uses, and a quantity that does not cover
	// the recipe's 250 g. It is still the same ingredient, so it still matches (7.3).
	if _, err := UpsertPantryItem(ctx, userID, model.PantryItem{
		Name: "flour", Quantity: ptrF(2), Unit: ptrS("cups"), Category: "spices_dry",
	}); err != nil {
		t.Fatalf("UpsertPantryItem: %v", err)
	}
	if have, total := match(); have != 1 || total != 2 {
		t.Fatalf("one match: have %d of %d, want 1 of 2", have, total)
	}

	if _, err := UpsertPantryItem(ctx, userID, model.PantryItem{
		Name: "  MILK ", Quantity: ptrF(1), Unit: ptrS("l"), Category: "fridge",
	}); err != nil {
		t.Fatalf("UpsertPantryItem: %v", err)
	}
	if have, total := match(); have != 2 || total != 2 {
		t.Fatalf("full match: have %d of %d, want 2 of 2", have, total)
	}

	// The other direction (BACKLOG 7.4): which recipes use an expiring item. Matched on the
	// same normalised name, so the recipe's "Milk" answers a pantry item spelled " MILK ".
	usedBy, err := RecipesUsingIngredients(ctx, userID, []string{" MILK ", "saffron"})
	if err != nil {
		t.Fatalf("RecipesUsingIngredients: %v", err)
	}
	if got := usedBy[recipe.ID]; len(got) != 1 || got[0] != "milk" {
		t.Fatalf("expected the recipe to use milk only, got %v", got)
	}
	if none, err := RecipesUsingIngredients(ctx, userID, []string{"saffron"}); err != nil || len(none) != 0 {
		t.Fatalf("an unused ingredient should match nothing: %v %v", none, err)
	}
}
