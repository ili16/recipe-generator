package service

import "testing"

// TestUsableClassDropsCategories is the regression for the crème brûlée card: the model
// answered "spice" for a vanilla bean and for black pepper, so a pantry holding pepper
// was offered as a swap for vanilla. A class only ever means "you could use this
// instead", so a shelf name has to be no class at all.
func TestUsableClassDropsCategories(t *testing.T) {
	ptr := func(s string) *string { return &s }

	dropped := []*string{nil, ptr(""), ptr("  "), ptr("spice"), ptr("Spices"), ptr("ground_spice"),
		ptr("herbs"), ptr("vegetable"), ptr("dairy"), ptr("other"),
		// The model writing "no class" as a word instead of a JSON null: every food
		// that answered this way used to match every other one.
		ptr("null"), ptr("NULL"), ptr("none"), ptr("n/a")}
	for _, c := range dropped {
		if got := usableClass(c); got != nil {
			t.Errorf("usableClass(%v) = %q, want nil", c, *got)
		}
	}

	kept := map[string]string{"pasta_dry": "pasta_dry", "hard_cheese": "hard_cheese",
		"  Liquid_Fat ": "liquid_fat", "vanilla": "vanilla"}
	for in, want := range kept {
		got := usableClass(&in)
		if got == nil || *got != want {
			t.Errorf("usableClass(%q) = %v, want %q", in, got, want)
		}
	}
}

// TestApplicableFixesKeepsOnlyAuditedFoods pins the ways the audit's answer is not usable
// as written: a name nobody asked about (the model inventing or misspelling one, which
// would be an UPDATE against an unaudited row), a "correction" that is just another shelf,
// and two answers about the same food.
func TestApplicableFixesKeepsOnlyAuditedFoods(t *testing.T) {
	ptr := func(s string) *string { return &s }
	audited := map[string]string{
		"vanilla bean": "spice", "black pepper": "spice", "penne": "pasta_dry", "nutmeg": "spice",
	}

	got := applicableFixes(audited, map[string]*string{
		"vanilla bean": ptr("vanilla"),   // moved to a real family
		"black pepper": nil,              // nothing substitutes for it
		"penne":        ptr("pasta_dry"), // unchanged, so not a fix
		"nutmeg":       ptr("herb"),      // a shelf for a shelf: no correction at all
		"saffron":      ptr("spice"),     // never audited
	})

	want := []ClassFix{
		{Name: "black pepper", Was: "spice", Class: nil},
		{Name: "nutmeg", Was: "spice", Class: nil},
		{Name: "vanilla bean", Was: "spice", Class: ptr("vanilla")},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d fixes, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Name != w.Name || got[i].Was != w.Was || !sameClass(got[i].Class, w.Class) {
			t.Errorf("fix %d = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestApplicableFixesDropsAFoodAnsweredTwice: "Vanilla Bean, ground" normalises to the
// same food as "vanilla bean", so these are two answers about one row. A correction the
// reviewer contradicts itself on is no correction — the row is left for a later run.
func TestApplicableFixesDropsAFoodAnsweredTwice(t *testing.T) {
	ptr := func(s string) *string { return &s }
	audited := map[string]string{"vanilla bean": "spice"}

	got := applicableFixes(audited, map[string]*string{
		"vanilla bean":         ptr("vanilla"),
		"Vanilla Bean, ground": ptr("baking"),
	})
	if len(got) != 0 {
		t.Errorf("got %+v, want nothing written for a food answered two ways", got)
	}
}
