package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// BACKLOG 17.2's acceptance, and the half only SQL reaches: canonical_item() is what the
// pantry match joins on, so a kitchen holding "onion" must cover a recipe written in
// German, and one holding "parmigiano reggiano" must cover a recipe asking for parmesan.
//
// The control case matters as much as the match: "penne" shares a CLASS with spaghetti
// and must still miss, because a class is a suggestion (17.4) and never an identity. A
// pantry that quietly claimed to hold spaghetti would send someone to the kitchen with
// the wrong box.
func TestPantryMatchUsesCanonicalNotClass(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-alias-match') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	class := func(s string) *string { return &s }
	if err := UpsertAliases(ctx, []model.Alias{
		{Name: "zwiebel", Canonical: "onion"},
		{Name: "onion", Canonical: "onion"},
		{Name: "parmigiano reggiano", Canonical: "parmesan"},
		{Name: "parmesan", Canonical: "parmesan"},
		// Same class, different food: the substitution surfaces may offer it, the match
		// may not count it.
		{Name: "spaghetti", Canonical: "spaghetti", Class: class("pasta_dry")},
		{Name: "penne", Canonical: "penne", Class: class("pasta_dry")},
	}); err != nil {
		t.Fatalf("UpsertAliases: %v", err)
	}
	t.Cleanup(func() {
		Pool.Exec(ctx, `DELETE FROM ingredient_alias WHERE name = ANY($1::text[])`,
			[]string{"zwiebel", "onion", "parmigiano reggiano", "parmesan", "spaghetti", "penne"})
	})

	doc := model.RecipeDocument{
		Title: "Alias Pasta", Language: "de", Servings: i(2),
		Ingredients: []model.RecipeIngredient{
			{Item: "Zwiebel", Quantity: f(1)},   // pantry has the English name
			{Item: "Parmesan", Quantity: f(50)}, // pantry has the Italian name
			{Item: "Spaghetti", Quantity: f(200)},
		},
		Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Cook"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Alias Pasta", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	for _, item := range []model.PantryItem{
		{Name: "Onion", Quantity: f(3), Category: "produce"},
		{Name: "parmigiano reggiano", Quantity: f(200), Unit: u("g"), Category: "fridge"},
		{Name: "Penne", Quantity: f(500), Unit: u("g"), Category: "spices_dry"},
	} {
		if _, err := UpsertPantryItem(ctx, userID, item); err != nil {
			t.Fatalf("UpsertPantryItem %s: %v", item.Name, err)
		}
	}

	recipes, err := GetRecipes(userID)
	if err != nil {
		t.Fatalf("GetRecipes: %v", err)
	}
	var got *model.Recipe
	for i := range recipes {
		if recipes[i].ID == recipe.ID {
			got = &recipes[i]
		}
	}
	if got == nil {
		t.Fatal("recipe not returned")
	}
	if got.PantryTotal != 3 || got.PantryHave != 2 {
		t.Errorf("pantry match = %d of %d, want 2 of 3 (onion and parmesan match by canonical; penne must not match spaghetti by class)",
			got.PantryHave, got.PantryTotal)
	}
	// BACKLOG 17.4a: the class match is not silently absent either — it is the third
	// number. Penne is the one swap on offer, and it stays out of have.
	if got.PantryClose != 1 {
		t.Errorf("pantry close = %d, want 1 (penne swaps for spaghetti by class)", got.PantryClose)
	}
}

// BACKLOG 17.4b's half of the same fact, from the other direction: an ingredient search
// for what the kitchen holds finds the recipe that wants a sibling of it, and says so.
// A swap that came back indistinguishable from a use would have the agent announce a
// spaghetti dinner to someone who owns penne.
func TestRecipesUsingIngredientsSeparatesSwaps(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-swap-search') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	class := func(s string) *string { return &s }
	if err := UpsertAliases(ctx, []model.Alias{
		{Name: "spaghetti", Canonical: "spaghetti", Class: class("pasta_dry")},
		{Name: "penne", Canonical: "penne", Class: class("pasta_dry")},
		{Name: "zwiebel", Canonical: "onion"},
		{Name: "onion", Canonical: "onion"},
	}); err != nil {
		t.Fatalf("UpsertAliases: %v", err)
	}
	t.Cleanup(func() {
		Pool.Exec(ctx, `DELETE FROM ingredient_alias WHERE name = ANY($1::text[])`,
			[]string{"spaghetti", "penne", "zwiebel", "onion"})
	})

	doc := model.RecipeDocument{
		Title: "Swap Pasta", Language: "en", Servings: i(2),
		Ingredients: []model.RecipeIngredient{
			{Item: "Spaghetti", Quantity: f(200)},
			{Item: "Zwiebel", Quantity: f(1)},
		},
		Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Cook"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Swap Pasta", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	hits, err := RecipesUsingIngredients(ctx, userID, []string{"penne", "onion"})
	if err != nil {
		t.Fatalf("RecipesUsingIngredients: %v", err)
	}
	var uses, swaps []model.IngredientHit
	for _, h := range hits[recipe.ID] {
		if h.SwapFor == "" {
			uses = append(uses, h)
		} else {
			swaps = append(swaps, h)
		}
	}
	// "onion" is the same food as the recipe's "Zwiebel": a use, not a swap.
	if len(uses) != 1 || uses[0].Item != "zwiebel" {
		t.Errorf("uses = %v, want the onion only", uses)
	}
	// "penne" is a sibling of the recipe's spaghetti: a swap, and named as one.
	if len(swaps) != 1 || swaps[0].Item != "spaghetti" || swaps[0].SwapFor != "penne" {
		t.Errorf("swaps = %v, want spaghetti←penne", swaps)
	}
}
