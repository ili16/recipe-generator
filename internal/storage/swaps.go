package storage

import (
	"context"

	"github.com/ili16/recipe-generator/internal/model"
)

// A swap is per-user and never per-household (BACKLOG 17.4c): a recipe is one person's
// property lent to the kitchen (15.3), and what one member uses instead of spaghetti is
// no more the household's business than what they had for lunch. So nothing here goes
// through inScope — user_id is the whole scope.

// ListSwaps reads the caller's substitutions for one recipe, keyed by the ingredient's
// sort_order. A line with no swap is simply absent.
func ListSwaps(ctx context.Context, userID, recipeID int) (map[int]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT sort_order, replacement FROM recipe_ingredient_swaps
		WHERE user_id = $1 AND recipe_id = $2`, userID, recipeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int]string{}
	for rows.Next() {
		var sortOrder int
		var replacement string
		if err := rows.Scan(&sortOrder, &replacement); err != nil {
			return nil, err
		}
		out[sortOrder] = replacement
	}
	return out, rows.Err()
}

// SetSwap records what the caller uses instead on one line of one recipe. An empty
// replacement clears it — the same call undoes itself, so the client needs one endpoint
// and not two.
//
// The recipe must be one the caller can see; anything else is pgx.ErrNoRows, not a
// silent write. The line itself is not checked: a swap on a line that does not exist
// matches nothing on read and costs one dead row.
func SetSwap(ctx context.Context, userID, recipeID, sortOrder int, replacement string) error {
	var visible bool
	if err := Pool.QueryRow(ctx, `
		SELECT TRUE FROM recipes r
		WHERE r.id = $2 AND `+inScope("r", "$1")+` AND r.deleted_at IS NULL`,
		userID, recipeID).Scan(&visible); err != nil {
		return err
	}

	if name := model.NormalizeItem(replacement); name == "" {
		_, err := Pool.Exec(ctx, `
			DELETE FROM recipe_ingredient_swaps
			WHERE user_id = $1 AND recipe_id = $2 AND sort_order = $3`, userID, recipeID, sortOrder)
		return err
	}

	_, err := Pool.Exec(ctx, `
		INSERT INTO recipe_ingredient_swaps (user_id, recipe_id, sort_order, replacement)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, recipe_id, sort_order) DO UPDATE SET replacement = EXCLUDED.replacement`,
		userID, recipeID, sortOrder, replacement)
	return err
}

// SwapOptions is what the caller could swap each line of a recipe for: the foods their
// own kitchen holds that share the line's ingredient_alias.class (BACKLOG 17.4c). Keyed
// by sort_order, and a line with nothing to offer is absent.
//
// Deliberately the pantry and not the whole alias table — an option you do not own is a
// shopping suggestion, and this list is for cooking tonight with what is in the house.
func SwapOptions(ctx context.Context, userID, recipeID int) (map[int][]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT ri.sort_order, p.name
		FROM recipe_ingredients ri
		JOIN ingredient_alias ra ON ra.name = normalize_item(ri.item) AND ra.class IS NOT NULL
		JOIN pantry_items p ON `+inScope("p", "$1")+`
		JOIN ingredient_alias pa ON pa.name = normalize_item(p.name) AND pa.class = ra.class
		WHERE ri.recipe_id = $2
		  AND canonical_item(p.name) <> canonical_item(ri.item)
		ORDER BY ri.sort_order, p.name`, userID, recipeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int][]string{}
	for rows.Next() {
		var sortOrder int
		var name string
		if err := rows.Scan(&sortOrder, &name); err != nil {
			return nil, err
		}
		out[sortOrder] = append(out[sortOrder], name)
	}
	return out, rows.Err()
}
