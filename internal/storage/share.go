package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// ShareRecipe issues (or returns the existing) share token for a recipe the caller owns.
// Re-sharing is idempotent so a second tap doesn't invalidate a link already sent.
// pgx.ErrNoRows if the recipe isn't the caller's, or is in the trash.
func ShareRecipe(ctx context.Context, userID, recipeID int) (string, error) {
	buf := make([]byte, 16) // 128 bits — not guessable, which is 8.4's third acceptance clause.
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var token string
	err := Pool.QueryRow(ctx, `
		UPDATE recipes SET share_token = COALESCE(share_token, $3)
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		RETURNING share_token`,
		recipeID, userID, base64.RawURLEncoding.EncodeToString(buf),
	).Scan(&token)
	return token, err
}

// UnshareRecipe revokes the link. Idempotent: an unshared recipe stays unshared.
func UnshareRecipe(ctx context.Context, userID, recipeID int) error {
	_, err := Pool.Exec(ctx, `
		UPDATE recipes SET share_token = NULL WHERE id = $1 AND user_id = $2`, recipeID, userID)
	return err
}

// GetSharedRecipe resolves a share token to its recipe, for unauthenticated readers.
// pgx.ErrNoRows for an unknown, revoked or trashed token. The owner lookup is what makes
// this safe to hand to GetRecipeByID, which is otherwise owner-scoped.
func GetSharedRecipe(ctx context.Context, token string) (model.Recipe, error) {
	if token == "" {
		return model.Recipe{}, pgx.ErrNoRows
	}
	var ownerID, recipeID int
	if err := Pool.QueryRow(ctx, `
		SELECT user_id, id FROM recipes
		WHERE share_token = $1 AND deleted_at IS NULL`, token,
	).Scan(&ownerID, &recipeID); err != nil {
		return model.Recipe{}, err
	}
	return GetRecipeByID(ctx, ownerID, recipeID)
}
