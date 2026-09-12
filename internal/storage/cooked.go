package storage

import "context"

// MarkCooked stamps a recipe the caller owns as cooked now. found=false if recipeID
// doesn't exist, isn't owned by userID, or is in the trash (no row is written then).
func MarkCooked(ctx context.Context, userID, recipeID int) (found bool, err error) {
	tag, err := Pool.Exec(ctx, `
		UPDATE recipes SET last_cooked_at = NOW()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, recipeID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UnmarkCooked clears the cooked mark (a mis-tap), leaving no trace of when it was set.
func UnmarkCooked(ctx context.Context, userID, recipeID int) error {
	_, err := Pool.Exec(ctx, `
		UPDATE recipes SET last_cooked_at = NULL WHERE id = $1 AND user_id = $2`, recipeID, userID)
	return err
}
