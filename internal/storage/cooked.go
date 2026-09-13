package storage

import "context"

// MarkCooked stamps a recipe the caller owns as cooked now, and — when rating is
// non-nil (BACKLOG 6.7) — records how that cook went, 1–5. A nil rating leaves any
// previous one alone, so the automatic mark at the end of cooking mode and the rating
// the user taps a moment later are the same idempotent call. found=false if recipeID
// doesn't exist, isn't owned by userID, or is in the trash (no row is written then).
func MarkCooked(ctx context.Context, userID, recipeID int, rating *int16) (found bool, err error) {
	tag, err := Pool.Exec(ctx, `
		UPDATE recipes SET last_cooked_at = NOW(), cooked_rating = COALESCE($3, cooked_rating)
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, recipeID, userID, rating)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UnmarkCooked clears the cooked mark (a mis-tap), leaving no trace of when it was set
// or how it was rated.
func UnmarkCooked(ctx context.Context, userID, recipeID int) error {
	_, err := Pool.Exec(ctx, `
		UPDATE recipes SET last_cooked_at = NULL, cooked_rating = NULL
		WHERE id = $1 AND user_id = $2`, recipeID, userID)
	return err
}
