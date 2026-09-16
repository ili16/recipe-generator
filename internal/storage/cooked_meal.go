package storage

import (
	"context"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// MarkMealCooked records that a planned meal was actually cooked and eaten, and takes
// what it used out of the pantry — in one transaction, because a fridge emptied for a
// meal that did not get marked (or the reverse) is worse than neither happening.
//
// It stamps two places, which are different facts:
//
//   - meal_plan_items.cooked_at — this pot is gone. The grocery list stops shopping for
//     it, and it cannot be deducted twice (a second call finds nothing to mark).
//   - recipes.last_cooked_at, via MarkCooked — this recipe has been cooked, which is what
//     the suggestion cooldown reads. That one is owner-scoped by design (a cook is a
//     person's, not a household's), so a member cooking someone else's recipe simply
//     leaves it unstamped. Not an error: the meal still happened.
//
// scale comes from the day's servings against the recipe's yield, the same ratio the
// grocery list shopped by — so the pantry loses exactly what the list bought.
//
// Returns what the deduction changed and the ingredient names it would not guess at; see
// DeductPantry for when it refuses.
func MarkMealCooked(ctx context.Context, userID, itemID int, rating *int16) ([]model.PantryChange, []string, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Claiming the mark is also the idempotency guard: cooked_at IS NULL means a second
	// tap deducts nothing rather than emptying the fridge twice.
	var recipeID int
	var planned, yield *int
	if err := tx.QueryRow(ctx, `
		UPDATE meal_plan_items mpi SET cooked_at = NOW()
		FROM meal_plans mp, recipes r
		WHERE mpi.meal_plan_id = mp.id AND r.id = mpi.recipe_id
		  AND mpi.id = $2 AND mpi.cooked_at IS NULL AND `+inScope("mp", "$1")+`
		RETURNING mpi.recipe_id, mpi.servings, r.servings`,
		userID, itemID,
	).Scan(&recipeID, &planned, &yield); err != nil {
		return nil, nil, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE recipes SET last_cooked_at = NOW(), cooked_rating = COALESCE($3, cooked_rating)
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, recipeID, userID, rating); err != nil {
		return nil, nil, err
	}

	applied, skipped, err := DeductPantry(ctx, tx, userID, recipeID, model.PortionScale(planned, yield))
	if err != nil {
		return nil, nil, err
	}
	return applied, skipped, tx.Commit(ctx)
}

// UnmarkMealCooked undoes a mis-tap: the meal goes back on the week and back onto the
// grocery list.
//
// It deliberately does **not** put the ingredients back in the pantry. There is no record
// of what was deducted — the rows were updated in place, not logged — and inventing one
// to support an undo nobody has asked for would be a worse trade than the user retyping a
// pantry line. The caller says so in the UI rather than pretending otherwise.
func UnmarkMealCooked(ctx context.Context, userID, itemID int) error {
	tag, err := Pool.Exec(ctx, `
		UPDATE meal_plan_items mpi SET cooked_at = NULL
		FROM meal_plans mp
		WHERE mpi.meal_plan_id = mp.id AND mpi.id = $2 AND `+inScope("mp", "$1"), userID, itemID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// PreviewDeductPantry answers "what would cooking this take out of the fridge" without
// taking it, for the approval card the agent shows before it acts (BACKLOG 10.2).
//
// It is the real deduction in a transaction that is always rolled back, rather than a
// second implementation of the same arithmetic. A preview computed by different code
// than the action is a lie waiting to happen — and this one's whole job is to be the
// thing the user agreed to.
func PreviewDeductPantry(ctx context.Context, userID, recipeID int, scale float64) ([]model.PantryChange, []string, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	return DeductPantry(ctx, tx, userID, recipeID, scale)
}
