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
	})

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
	})

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
