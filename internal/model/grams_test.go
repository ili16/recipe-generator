package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ToGrams is the only unit arithmetic in the codebase and two of its callers subtract
// from somebody's fridge, so the contract that matters most is the last case: unknown
// must mean unknown, never 1.
func TestToGrams(t *testing.T) {
	table := map[ItemUnit]float64{
		{Item: "onion", Unit: ""}:           150,
		{Item: "flour", Unit: "cup"}:        120,
		{Item: GramsWildcard, Unit: "cup"}:  240,
		{Item: GramsWildcard, Unit: "tbsp"}: 15,
	}
	u := func(s string) *string { return &s }

	for _, tc := range []struct {
		name string
		item string
		unit *string
		qty  float64
		want float64
		ok   bool
	}{
		{"grams are the identity and need no row", "anything", u("g"), 250, 250, true},
		{"kg is folded by NormalizeUnit before lookup", "anything", u("kg"), 1.5, 1500, true},
		{"a countable item uses its per-piece row", "Onion", nil, 2, 300, true},
		{"an item's own row beats the wildcard", "flour", u("cup"), 2, 240, true},
		{"the wildcard answers an item with no row", "sugar", u("cup"), 1, 240, true},
		{"case and spacing are normalised", "  FLOUR ", u(" Cup "), 1, 120, true},
		{"an unknown countable item is unknown", "quince", nil, 3, 0, false},
		{"an unknown unit is unknown", "flour", u("handful"), 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ToGrams(table, tc.item, tc.unit, tc.qty)
			if ok != tc.ok || got != tc.want {
				t.Errorf("ToGrams = (%v, %v), want (%v, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// FromGrams is how a deduction gets written back in the unit its owner typed, so it has
// to be ToGrams' exact inverse — 800 g of flour is 0.8 kg, not 800 kg.
func TestFromGramsInvertsToGrams(t *testing.T) {
	table := map[ItemUnit]float64{{Item: "onion", Unit: ""}: 150}
	u := func(s string) *string { return &s }

	if got, ok := FromGrams(table, "flour", u("kg"), 800); !ok || got != 0.8 {
		t.Errorf("800 g in kg = (%v, %v), want (0.8, true)", got, ok)
	}
	if got, ok := FromGrams(table, "onion", nil, 450); !ok || got != 3 {
		t.Errorf("450 g of onion = (%v, %v), want (3, true)", got, ok)
	}
	if _, ok := FromGrams(table, "quince", nil, 450); ok {
		t.Error("an unweighable item must not convert back")
	}
}

// BACKLOG 16.4: the preparation is not part of the name. The SQL copy of this rule is
// normalize_item() in migration 00037 — if these cases change, that changes too.
func TestNormalizeItem(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Onion", "onion"},
		{"  FLOUR  ", "flour"},
		{"onion, finely chopped", "onion"},
		{"garlic (minced)", "garlic"},
		{"Zwiebel, gewürfelt", "zwiebel"},
		{"chicken breast, boneless, skinless", "chicken breast"},
		{"salt, to taste", "salt"},
		{", chopped", ""},
		{"", ""},
	} {
		if got := NormalizeItem(tc.in); got != tc.want {
			t.Errorf("NormalizeItem(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// BACKLOG 17.1: the size is not part of the name, it is part of the weight. The SQL copy
// of the strip is normalize_item() in migration 00040 — if these cases change, so does it.
func TestSplitSize(t *testing.T) {
	for _, tc := range []struct {
		in     string
		factor float64
		name   string
	}{
		{"onion", 1, "onion"},
		{"medium onion", 1, "onion"},
		{"large onion", 1.35, "onion"},
		{"small onion", 0.7, "onion"},
		{"Große Zwiebel", 1.35, "zwiebel"},
		{"kleine Zwiebel, gewürfelt", 0.7, "zwiebel"},
		{"grosse zwiebel", 1.35, "zwiebel"},
		{"large egg (beaten)", 1.35, "egg"},
		{"medium", 1, "medium"}, // a bare size word is a food name, not a modifier
		{"grande", 1, "grande"}, // ... and this one is a coffee
		{"large  ", 1, "large"}, // nothing left after the strip is not a strip
		{"chicken breast", 1, "chicken breast"},
		{"", 1, ""},
	} {
		factor, name := SplitSize(tc.in)
		if factor != tc.factor || name != tc.name {
			t.Errorf("SplitSize(%q) = (%v, %q), want (%v, %q)", tc.in, factor, name, tc.factor, tc.name)
		}
	}
}

// The factor weighs pieces and only pieces. A large onion is half again an ordinary one;
// a "large tbsp" is not a measure, and reading one as 1.35 tbsp would put a wrong number
// on a shopping list.
func TestToGramsAppliesSizeToPiecesOnly(t *testing.T) {
	table := map[ItemUnit]float64{
		{Item: "onion", Unit: ""}:           150,
		{Item: GramsWildcard, Unit: "tbsp"}: 15,
	}
	u := func(s string) *string { return &s }

	if got, ok := ToGrams(table, "large onion", nil, 1); !ok || got != 202.5 {
		t.Errorf("one large onion = (%v, %v), want (202.5, true)", got, ok)
	}
	if got, ok := ToGrams(table, "medium onion", nil, 2); !ok || got != 300 {
		t.Errorf("two medium onions = (%v, %v), want (300, true)", got, ok)
	}
	if got, ok := ToGrams(table, "large olive oil", u("tbsp"), 1); !ok || got != 15 {
		t.Errorf("a sized named unit must not scale: (%v, %v), want (15, true)", got, ok)
	}
	// FromGrams inverts through the same factor, so a deduction writes back what it took.
	if got, ok := FromGrams(table, "large onion", nil, 202.5); !ok || got != 1 {
		t.Errorf("202.5 g back into large onions = (%v, %v), want (1, true)", got, ok)
	}
}

// normalize_item() in migration 00040 is the SQL copy of SplitSize's strip, and the two
// run against the same rows: Go writes the key, SQL joins on it. A word in one list and
// not the other means a pantry line that silently stops matching its own recipe, which is
// invisible until someone's shopping list grows a second onion. Cheap to assert, so
// assert it.
func TestMigrationSizeWordsMatchGo(t *testing.T) {
	sql, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "00040_normalize_item_size.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	// Drop the whitespace and stitch the concatenated string literals back together, so
	// the alternation reads as one run of |-separated words however the SQL wraps it.
	flat := strings.ReplaceAll(strings.Join(strings.Fields(string(sql)), ""), "'||'", "")
	for word := range sizeFactors {
		if !strings.Contains(flat, "|"+word+"|") &&
			!strings.Contains(flat, "("+word+"|") &&
			!strings.Contains(flat, "|"+word+")") {
			t.Errorf("size word %q is in model.sizeFactors but not in normalize_item()", word)
		}
	}
}
