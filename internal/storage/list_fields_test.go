package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestGetRecipesDenormalisesServingsAndTime covers BACKLOG 6.5's data half: the library
// list carries servings and a cooking time so the client can filter on them without
// loading every document. The time falls back to prep+cook, and stays NULL — not 0 —
// when nothing is recorded, so "unknown" never filters in as "instant".
func TestGetRecipesDenormalisesServingsAndTime(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-list-fields') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	i := func(v int) *int { return &v }
	base := model.RecipeDocument{
		Language:    "en",
		Ingredients: []model.RecipeIngredient{{Item: "Water"}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Boil it"}},
	}

	total := base
	total.Title, total.Servings, total.TotalMinutes = "Has total", i(6), i(20)

	split := base
	split.Title, split.PrepMinutes, split.CookMinutes = "Prep plus cook", i(10), i(5)

	unknown := base
	unknown.Title = "Knows nothing"

	for _, doc := range []model.RecipeDocument{total, split, unknown} {
		created, err := AddRecipeStructured(ctx, userID, doc, "# "+doc.Title, nil, nil, "test-model", "v1", nil)
		if err != nil {
			t.Fatalf("AddRecipeStructured(%s): %v", doc.Title, err)
		}
		t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, created.ID) })
	}

	list, err := GetRecipes(userID)
	if err != nil {
		t.Fatalf("GetRecipes: %v", err)
	}
	byName := make(map[string]model.Recipe, len(list))
	for _, r := range list {
		byName[r.Recipename] = r
	}

	if got := byName["Has total"]; got.Servings == nil || *got.Servings != 6 || got.TotalMinutes == nil || *got.TotalMinutes != 20 {
		t.Errorf("explicit total: servings=%v total_minutes=%v", got.Servings, got.TotalMinutes)
	}
	if got := byName["Prep plus cook"]; got.TotalMinutes == nil || *got.TotalMinutes != 15 {
		t.Errorf("prep+cook fallback: total_minutes=%v, want 15", got.TotalMinutes)
	}
	if got := byName["Knows nothing"]; got.Servings != nil || got.TotalMinutes != nil {
		t.Errorf("unknown should stay nil, got servings=%v total_minutes=%v", got.Servings, got.TotalMinutes)
	}
}
