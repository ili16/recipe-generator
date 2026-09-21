package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// BACKLOG 18.1's acceptance: `uses` edges equal the ingredient lines, cuisine/technique are
// edges, deleting the recipe removes them, and another kitchen sees none of it.
func TestGraphEdges(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var owner, stranger int
	for id, oauth := range map[*int]string{&owner: "test-graph-a", &stranger: "test-graph-b"} {
		if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ($1) RETURNING id`, oauth).Scan(id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		uid := *id
		t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, uid) })
	}

	cuisine := "italian"
	doc := model.RecipeDocument{
		Title: "Graph Soup", Language: "en", Servings: i(2), Cuisine: &cuisine, Techniques: []string{"simmer"},
		Ingredients: []model.RecipeIngredient{{Item: "medium onion", Quantity: f(2)}, {Item: "Quince", Quantity: f(1)}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Simmer"}},
	}
	recipe, err := AddRecipeStructured(ctx, owner, doc, "# Graph Soup", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}

	edges, err := GraphEdges(ctx, owner, recipe.ID)
	if err != nil {
		t.Fatalf("GraphEdges: %v", err)
	}
	got := map[string]bool{}
	for _, e := range edges {
		got[e.Rel+":"+e.DstType+":"+e.Dst] = true
	}
	for _, want := range []string{"uses:ingredient:onion", "uses:ingredient:quince", "belongs_to:cuisine:italian", "cooked_with:technique:simmer"} {
		if !got[want] {
			t.Errorf("missing edge %s in %v", want, got)
		}
	}

	if other, _ := GraphEdges(ctx, stranger, 0); len(other) != 0 {
		t.Errorf("another kitchen sees %d edges", len(other))
	}

	if _, err := Pool.Exec(ctx, `UPDATE recipes SET deleted_at = NOW() WHERE id = $1`, recipe.ID); err != nil {
		t.Fatal(err)
	}
	if gone, _ := GraphEdges(ctx, owner, 0); len(gone) != 0 {
		t.Errorf("deleted recipe left %d edges", len(gone))
	}
}
