package storage

import "testing"

func TestGraphTraversal(t *testing.T) {
	uses := func(r, ing string) GraphEdge { return GraphEdge{"recipe", r, "uses", "ingredient", ing, 1} }
	edges := []GraphEdge{
		uses("1", "butter"), uses("1", "flour"),
		uses("2", "margarine"), uses("2", "flour"),
		uses("3", "quince"),
		{"ingredient", "butter", "is_a", "class", "fat", 1},
		{"ingredient", "margarine", "is_a", "class", "fat", 1},
		{"ingredient", "zwiebel", "translates", "ingredient", "onion", 1},
	}
	got := rankSimilar(edges, "1")
	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("rankSimilar = %v, want only recipe 2", got)
	}
	if r := relatedIngredients(edges, "butter"); len(r) != 1 || r[0] != "margarine" {
		t.Errorf("class siblings = %v", r)
	}
	if r := relatedIngredients(edges, "onion"); len(r) != 1 || r[0] != "zwiebel" {
		t.Errorf("translations = %v", r)
	}
	if n := len(walk(edges, "recipe", "1", 1)); n != 2 {
		t.Errorf("depth 1 = %d edges, want 2", n)
	}
	if n := len(walk(edges, "recipe", "1", 2)); n != 4 { // recipe 1's two uses, butter's class, recipe 2's use of flour
		t.Errorf("depth 2 = %d edges, want 4", n)
	}

	// The map's edges: one pair only (1↔2, sharing flour); recipe 3 shares nothing.
	links := overlapLinks(edges, 4)
	if len(links) != 1 || links[0].A != 1 || links[0].B != 2 {
		t.Fatalf("overlapLinks = %+v, want one 1-2 link", links)
	}
	if len(links[0].Shared) != 1 || links[0].Shared[0] != "flour" {
		t.Errorf("shared = %v, want [flour]", links[0].Shared)
	}
	if n := len(overlapLinks(edges, 0)); n != 1 { // top=0 means "no cap", still deduped
		t.Errorf("uncapped links = %d, want 1", n)
	}
}

// A kitchen where everything contains salt: the map used to join crème brûlée to pizza because
// plain Jaccard counted a staple as evidence. Only the two bakes may link, and not on salt.
func TestOverlapIgnoresStaples(t *testing.T) {
	uses := func(r, ing string) GraphEdge { return GraphEdge{"recipe", r, "uses", "ingredient", ing, 1} }
	edges := []GraphEdge{
		uses("1", "salt"), uses("1", "flour"), uses("2", "salt"), uses("2", "flour"),
		uses("3", "salt"), uses("3", "cream"), uses("4", "salt"), uses("4", "olive"),
		uses("5", "salt"), uses("5", "quince"), uses("6", "salt"), uses("6", "anchovy"),
	}
	links := overlapLinks(edges, 4)
	if len(links) != 1 || links[0].A != 1 || links[0].B != 2 {
		t.Fatalf("overlapLinks = %+v, want only the 1-2 flour link", links)
	}
	if len(links[0].Shared) != 1 || links[0].Shared[0] != "flour" {
		t.Errorf("shared = %v, want [flour] — salt must never be named", links[0].Shared)
	}
	// Ranking still answers "what is closest", staples or not; only the map's floor is strict.
	if got := rankSimilar(edges, "3"); len(got) != 0 {
		t.Errorf("rankSimilar(cream-only recipe) = %v, want nothing (salt is not evidence)", got)
	}
}
