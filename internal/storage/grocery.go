package storage

import (
	"context"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// GetPlannedIngredients returns every ingredient of every recipe planned in
// [startsOn, endsOn] for userID, one row per (meal, ingredient) — consolidation into
// shopping lines is service.BuildGroceryList's job (BACKLOG 6.1).
//
// Optional ingredients are included: "you may want this" is a shopping decision, and a
// list that silently omits half a recipe is worse than one with a line to ignore.
func GetPlannedIngredients(ctx context.Context, userID int, startsOn, endsOn time.Time) ([]model.GroceryIngredient, error) {
	rows, err := Pool.Query(ctx, `
		SELECT mpi.planned_on, r.title, mpi.servings, r.servings, ri.item, ri.quantity, ri.unit, ri.quantity_text, ri.category
		FROM meal_plan_items mpi
		JOIN meal_plans mp ON mp.id = mpi.meal_plan_id
		JOIN recipes r ON r.id = mpi.recipe_id
		JOIN recipe_ingredients ri ON ri.recipe_id = r.id
		WHERE mp.user_id = $1 AND mpi.planned_on BETWEEN $2 AND $3 AND r.deleted_at IS NULL
		ORDER BY mpi.planned_on, mpi.start_time, ri.sort_order`,
		userID, startsOn, endsOn,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.GroceryIngredient{}
	for rows.Next() {
		var ing model.GroceryIngredient
		var plannedOn time.Time
		if err := rows.Scan(&plannedOn, &ing.RecipeTitle, &ing.PlannedServings, &ing.RecipeServings, &ing.Item, &ing.Quantity, &ing.Unit, &ing.QuantityText, &ing.Category); err != nil {
			return nil, err
		}
		ing.PlannedOn = plannedOn.Format("2006-01-02")
		out = append(out, ing)
	}
	return out, rows.Err()
}
