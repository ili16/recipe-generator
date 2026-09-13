package storage

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestAisleBackfillWrite is BACKLOG 6.9's storage half against a real DB (skipped unless
// DB_URL is set): an uncategorised name is offered for classification, the write reaches
// every recipe using that name case-insensitively, and a row that already has an aisle
// from generation time is never overwritten by the backfill's guess.
func TestAisleBackfillWrite(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-aisle') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	pantry := "pantry"
	mk := func(title string, ings ...model.RecipeIngredient) int {
		created, err := AddRecipeStructured(ctx, userID, model.RecipeDocument{
			Title: title, Language: "en", Ingredients: ings,
			Steps: []model.RecipeStep{{SortOrder: 1, StepText: "Cook it"}},
		}, "# "+title, nil, nil, "test-model", "v1", nil)
		if err != nil {
			t.Fatalf("AddRecipeStructured %s: %v", title, err)
		}
		t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, created.ID) })
		return created.ID
	}
	// Two recipes share an uncategorised name in different case; one row already carries
	// an aisle the generation call emitted.
	mk("Aisle Test One", model.RecipeIngredient{Item: "Zzgarlic"}, model.RecipeIngredient{Item: "Zzflour", Category: &pantry})
	mk("Aisle Test Two", model.RecipeIngredient{Item: "ZZGARLIC"})

	items, err := UncategorisedItems(ctx)
	if err != nil {
		t.Fatalf("UncategorisedItems: %v", err)
	}
	if !slices.Contains(items, "zzgarlic") {
		t.Errorf("uncategorised name missing from backfill input: %v", items)
	}
	if slices.Contains(items, "zzflour") {
		t.Errorf("already-categorised name must not be offered for reclassification")
	}

	// "zzflour" is deliberately included with a wrong aisle: the NULL-only WHERE must
	// leave it alone.
	n, err := SetItemCategories(ctx, []string{"zzgarlic", "zzflour"}, []string{"produce", "dairy"})
	if err != nil {
		t.Fatalf("SetItemCategories: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 rows updated (both garlic spellings), got %d", n)
	}

	rows, err := Pool.Query(ctx, `
		SELECT lower(ri.item), ri.category FROM recipe_ingredients ri
		JOIN recipes r ON r.id = ri.recipe_id WHERE r.user_id = $1`, userID)
	if err != nil {
		t.Fatalf("verify query: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var item string
		var cat *string
		if err := rows.Scan(&item, &cat); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if cat != nil {
			got[item] = *cat
		}
	}
	if got["zzgarlic"] != "produce" {
		t.Errorf("backfill did not reach every spelling: %v", got)
	}
	if got["zzflour"] != "pantry" {
		t.Errorf("backfill overwrote a generation-time aisle: %q", got["zzflour"])
	}
}
