package storage

import "context"

// SetVote upserts the caller's vote on a recipe they own. found=false if recipeID
// doesn't exist or isn't owned by userID (no row is written in that case).
func SetVote(ctx context.Context, userID, recipeID int, vote int16) (found bool, err error) {
	tag, err := Pool.Exec(ctx, `
		INSERT INTO recipe_votes (recipe_id, user_id, vote)
		SELECT $1, $2, $3
		WHERE EXISTS (SELECT 1 FROM recipes WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)
		ON CONFLICT (recipe_id, user_id) DO UPDATE SET vote = EXCLUDED.vote, created_at = NOW()`,
		recipeID, userID, vote)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// RemoveVote deletes the caller's vote on a recipe, if any.
func RemoveVote(ctx context.Context, userID, recipeID int) error {
	_, err := Pool.Exec(ctx, `DELETE FROM recipe_votes WHERE recipe_id = $1 AND user_id = $2`, recipeID, userID)
	return err
}
