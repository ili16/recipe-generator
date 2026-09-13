package service

import "testing"

// TestPickPairsIsKeyedByName is the regression for the bug BACKLOG 9.3's dry run found:
// classifyAisles used to return a bare []string joined to the batch by index, so an
// answer with an extra or missing entry shifted every item after it onto another item's
// aisle — silently, and then wrote it. A real run returned 111 aisles for 100 items.
// Pairing by name means a bad answer can only cost the items it actually names.
func TestPickPairsIsKeyedByName(t *testing.T) {
	chunk := []string{"flour", "chicken thighs", "parsley"}

	// The model answered for every item, but out of order and with an invented extra.
	// Index-joining this would have labelled flour "produce".
	names, cats := pickPairs(chunk, map[string]string{
		"parsley":        "produce",
		"ketchup":        "pantry", // never asked for
		"chicken thighs": "protein",
		"flour":          "pantry",
	})
	assertPairs(t, "reordered answer", names, cats,
		[]string{"flour", "chicken thighs", "parsley"},
		[]string{"pantry", "protein", "produce"})

	// A dropped item stays NULL for a later run rather than borrowing its neighbour's.
	names, cats = pickPairs(chunk, map[string]string{
		"flour":   "pantry",
		"parsley": "produce",
	})
	assertPairs(t, "dropped item", names, cats,
		[]string{"flour", "parsley"}, []string{"pantry", "produce"})

	// "other" and anything off the vocabulary are not written at all.
	names, cats = pickPairs(chunk, map[string]string{
		"flour":          "other",
		"chicken thighs": "butcher", // not a GroceryCategory
		"parsley":        "produce",
	})
	assertPairs(t, "unwritable answers", names, cats,
		[]string{"parsley"}, []string{"produce"})

	// An empty answer writes nothing and must not panic.
	if names, cats = pickPairs(chunk, map[string]string{}); names != nil || cats != nil {
		t.Errorf("empty answer: got %v/%v, want nothing", names, cats)
	}
}

func assertPairs(t *testing.T, what string, gotNames, gotCats, wantNames, wantCats []string) {
	t.Helper()
	if len(gotNames) != len(gotCats) {
		t.Fatalf("%s: names/cats length mismatch: %d vs %d", what, len(gotNames), len(gotCats))
	}
	if len(gotNames) != len(wantNames) {
		t.Fatalf("%s: got %v/%v, want %v/%v", what, gotNames, gotCats, wantNames, wantCats)
	}
	for i := range wantNames {
		if gotNames[i] != wantNames[i] || gotCats[i] != wantCats[i] {
			t.Errorf("%s: pair %d = %s→%s, want %s→%s",
				what, i, gotNames[i], gotCats[i], wantNames[i], wantCats[i])
		}
	}
}
