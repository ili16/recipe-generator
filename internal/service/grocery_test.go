package service

import (
	"strings"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

func ptrF(f float64) *float64 { return &f }
func ptrS(s string) *string   { return &s }

// BACKLOG 6.1's acceptance: two recipes sharing an ingredient produce one summed line,
// and un-parseable amounts are grouped, not dropped.
func TestBuildGroceryList(t *testing.T) {
	lines := BuildGroceryList([]model.GroceryIngredient{
		{PlannedOn: "2026-09-14", RecipeTitle: "Roast", Item: "Flour", Quantity: ptrF(250), Unit: ptrS("g"), Category: ptrS("pantry")},
		{PlannedOn: "2026-09-15", RecipeTitle: "Bread", Item: "flour", Quantity: ptrF(0.25), Unit: ptrS("kg"), Category: ptrS("pantry")},
		{PlannedOn: "2026-09-14", RecipeTitle: "Roast", Item: "Salt", QuantityText: ptrS("to taste"), Category: ptrS("pantry")},
		{PlannedOn: "2026-09-15", RecipeTitle: "Bread", Item: "salt", Quantity: ptrF(5), Unit: ptrS("g"), Category: ptrS("pantry")},
		{PlannedOn: "2026-09-15", RecipeTitle: "Bread", Item: "Parsley", QuantityText: ptrS("for garnish"), Category: ptrS("produce")},
		{PlannedOn: "2026-09-14", RecipeTitle: "Roast", Item: "Eggs", Quantity: ptrF(2), Category: ptrS("protein")},
	}, nil, nil, nil)

	// Keyed lowercase: a line's display name is whichever spelling the first row that
	// created it used ("flour" from Bread, not "Flour" from Roast).
	byItem := map[string]model.GroceryLine{}
	for _, l := range lines {
		byItem[strings.ToLower(l.Item)] = l
	}
	if len(lines) != 4 {
		t.Fatalf("want 4 lines, got %d: %+v", len(lines), lines)
	}

	// Two meals, two unit spellings, one line.
	flour := byItem["flour"]
	if flour.Quantity == nil || *flour.Quantity != 500 || flour.Unit == nil || *flour.Unit != "g" {
		t.Errorf("flour not consolidated to 500 g: %+v", flour)
	}
	if len(flour.For) != 2 {
		t.Errorf("flour should name both meals, got %v", flour.For)
	}
	if flour.For[0] != "Mon Roast" {
		t.Errorf("meal label should carry the weekday, got %q", flour.For[0])
	}

	// "to taste" folds into the line that has a real amount, and neither is lost.
	salt := byItem["salt"]
	if salt.Quantity == nil || *salt.Quantity != 5 || salt.Note != "to taste" {
		t.Errorf("salt should keep both the amount and the note: %+v", salt)
	}

	// An item with no measured amount anywhere still gets a line.
	parsley := byItem["parsley"]
	if parsley.Quantity != nil || parsley.Note != "for garnish" {
		t.Errorf("parsley line wrong: %+v", parsley)
	}

	// Countable, unitless: a quantity with no unit, not a "2 " prefix on the name.
	eggs := byItem["eggs"]
	if eggs.Quantity == nil || *eggs.Quantity != 2 || eggs.Unit != nil {
		t.Errorf("eggs line wrong: %+v", eggs)
	}

	// Aisle order: produce, protein, pantry — the order of model.GroceryCategories.
	if lines[0].Category != "produce" || lines[1].Category != "protein" {
		t.Errorf("lines not grouped in aisle order: %+v", lines)
	}
}

func ptrI(i int) *int { return &i }

// BACKLOG 6.2's acceptance, the grocery half: a day planned for more portions than the
// recipe yields scales that day's quantities, and unknown servings on either side leave
// them alone.
func TestBuildGroceryListScalesByServings(t *testing.T) {
	lines := BuildGroceryList([]model.GroceryIngredient{
		// Cooking for 6 from a recipe that serves 2: ×3.
		{PlannedOn: "2026-09-14", RecipeTitle: "Roast", PlannedServings: ptrI(6), RecipeServings: ptrI(2),
			Item: "Flour", Quantity: ptrF(100), Unit: ptrS("g"), Category: ptrS("pantry")},
		// Same item, a day with no servings set: counted as written.
		{PlannedOn: "2026-09-15", RecipeTitle: "Bread", Item: "flour", Quantity: ptrF(50), Unit: ptrS("g"), Category: ptrS("pantry")},
		// The recipe doesn't say what it yields, so there is no ratio to scale by.
		{PlannedOn: "2026-09-16", RecipeTitle: "Cake", PlannedServings: ptrI(8),
			Item: "Sugar", Quantity: ptrF(80), Unit: ptrS("g"), Category: ptrS("pantry")},
	}, nil, nil, nil)

	byItem := map[string]model.GroceryLine{}
	for _, l := range lines {
		byItem[strings.ToLower(l.Item)] = l
	}
	if flour := byItem["flour"]; flour.Quantity == nil || *flour.Quantity != 350 {
		t.Errorf("flour should be 100×3 + 50 = 350, got %+v", flour)
	}
	if sugar := byItem["sugar"]; sugar.Quantity == nil || *sugar.Quantity != 80 {
		t.Errorf("sugar should be unscaled without a recipe yield, got %+v", sugar)
	}
}

// Staples are dropped outright: the point of marking salt as one is that it stops being
// a shopping decision, while staying a pantry row the recipe match still counts.
func TestBuildGroceryListDropsStaples(t *testing.T) {
	lines := BuildGroceryList([]model.GroceryIngredient{
		{PlannedOn: "2026-09-14", RecipeTitle: "Roast", Item: "Salt", Quantity: ptrF(5), Unit: ptrS("g"), Category: ptrS("pantry")},
		{PlannedOn: "2026-09-14", RecipeTitle: "Roast", Item: "Pepper", QuantityText: ptrS("to taste"), Category: ptrS("pantry")},
		{PlannedOn: "2026-09-14", RecipeTitle: "Roast", Item: "Flour", Quantity: ptrF(250), Unit: ptrS("g"), Category: ptrS("pantry")},
	}, map[string]bool{"salt": true, "pepper": true}, nil, nil)

	if len(lines) != 1 || strings.ToLower(lines[0].Item) != "flour" {
		t.Fatalf("only flour should survive, got %+v", lines)
	}
}

// The unit fix: "2 onions" on Monday and "150 g onion" on Thursday are one line, and it
// is expressed in the unit you shop in — whole onions, rounded up.
func TestBuildGroceryListConsolidatesAcrossUnits(t *testing.T) {
	grams := map[model.ItemUnit]float64{
		{Item: "onion", Unit: ""}:  150,
		{Item: "milk", Unit: "ml"}: 1,
	}
	lines := BuildGroceryList([]model.GroceryIngredient{
		{PlannedOn: "2026-09-14", RecipeTitle: "Soup", Item: "Onion", Quantity: ptrF(2), Category: ptrS("produce")},
		{PlannedOn: "2026-09-17", RecipeTitle: "Stew", Item: "onion", Quantity: ptrF(150), Unit: ptrS("g"), Category: ptrS("produce")},
		{PlannedOn: "2026-09-15", RecipeTitle: "Sauce", Item: "Milk", Quantity: ptrF(200), Unit: ptrS("ml"), Category: ptrS("dairy")},
		{PlannedOn: "2026-09-16", RecipeTitle: "Custard", Item: "milk", Quantity: ptrF(0.3), Unit: ptrS("l"), Category: ptrS("dairy")},
	}, nil, grams, nil)

	byItem := map[string]model.GroceryLine{}
	for _, l := range lines {
		byItem[strings.ToLower(l.Item)] = l
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 consolidated lines, got %d: %+v", len(lines), lines)
	}

	// 2 onions (300 g) + 150 g = 450 g = 3 onions, rendered as pieces.
	onion := byItem["onion"]
	if onion.Quantity == nil || *onion.Quantity != 3 || onion.Unit != nil {
		t.Errorf("onion should be 3 whole onions: %+v", onion)
	}
	if len(onion.For) != 2 {
		t.Errorf("onion should name both meals, got %v", onion.For)
	}

	// Milk is never counted in pieces, so it stays in the unit the week first used.
	milk := byItem["milk"]
	if milk.Quantity == nil || *milk.Quantity != 500 || milk.Unit == nil || *milk.Unit != "ml" {
		t.Errorf("milk should be 500 ml: %+v", milk)
	}
}

// A thin table must never produce a wrong number: one unweighable amount makes that item
// fall back to a line per unit family, and leaves every other item consolidated.
func TestBuildGroceryListFallsBackPerItem(t *testing.T) {
	grams := map[model.ItemUnit]float64{{Item: "onion", Unit: ""}: 150}
	lines := BuildGroceryList([]model.GroceryIngredient{
		{PlannedOn: "2026-09-14", RecipeTitle: "Soup", Item: "Onion", Quantity: ptrF(2), Category: ptrS("produce")},
		{PlannedOn: "2026-09-15", RecipeTitle: "Stew", Item: "Leek", Quantity: ptrF(1), Category: ptrS("produce")},
		{PlannedOn: "2026-09-16", RecipeTitle: "Pie", Item: "leek", Quantity: ptrF(80), Unit: ptrS("g"), Category: ptrS("produce")},
	}, nil, grams, nil)

	if len(lines) != 3 {
		t.Fatalf("leek should split into two lines and onion stay one, got %d: %+v", len(lines), lines)
	}
	for _, l := range lines {
		if strings.ToLower(l.Item) == "onion" && (l.Quantity == nil || *l.Quantity != 2) {
			t.Errorf("onion should still consolidate to 2: %+v", l)
		}
	}
}

// BACKLOG 17.1 reaching the shopping list. The size modifier lives on the ingredient's
// own wording, and the group key is the bare food by construction — so weighing from the
// key silently drops it and every large onion shops as an ordinary one. That is exactly
// the bug this asserts against: the same list built from the key alone gives 3, not 4.
//
// It also pins the rendering direction: the total is rendered back through the BARE name,
// because what you buy is ordinary onions however the recipes described them.
func TestGroceryListWeighsSizedIngredients(t *testing.T) {
	grams := map[model.ItemUnit]float64{{Item: "onion", Unit: ""}: 150}

	lines := BuildGroceryList([]model.GroceryIngredient{
		{PlannedOn: "2026-09-14", RecipeTitle: "Roast", Item: "large onion", Quantity: ptrF(2), Category: ptrS("produce")},
		{PlannedOn: "2026-09-15", RecipeTitle: "Soup", Item: "Onion", Quantity: ptrF(1), Category: ptrS("produce")},
	}, nil, grams, nil)

	if len(lines) != 1 {
		t.Fatalf("want one consolidated onion line, got %d: %+v", len(lines), lines)
	}
	// 2 large (2 x 1.35 x 150 = 405 g) plus one ordinary (150 g) is 555 g, which is 3.7
	// onions on the shelf and therefore 4 — half an onion is not for sale.
	if lines[0].Quantity == nil || *lines[0].Quantity != 4 {
		t.Errorf("want 4 onions, got %v (3 means the size modifier was dropped)", qtyOf(lines[0].Quantity))
	}
	if lines[0].Unit != nil {
		t.Errorf("a countable item renders in pieces, got unit %v", *lines[0].Unit)
	}
}

// qtyOf renders a nullable quantity for a failure message -- a pointer address tells the
// reader nothing about which number came out.
func qtyOf(q *float64) any {
	if q == nil {
		return "nil"
	}
	return *q
}
