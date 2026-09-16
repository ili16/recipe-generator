package model

import "testing"

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
