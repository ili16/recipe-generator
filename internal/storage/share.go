package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// newShareToken is 128 bits of URL-safe randomness — not guessable, which is 8.4's third
// acceptance clause and 13.2's only security property.
func newShareToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ShareRecipe issues (or returns the existing) share token for a recipe the caller owns.
// Re-sharing is idempotent so a second tap doesn't invalidate a link already sent.
// pgx.ErrNoRows if the recipe isn't the caller's, or is in the trash.
func ShareRecipe(ctx context.Context, userID, recipeID int) (string, error) {
	fresh, err := newShareToken()
	if err != nil {
		return "", err
	}
	var token string
	err = Pool.QueryRow(ctx, `
		UPDATE recipes SET share_token = COALESCE(share_token, $3)
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
		RETURNING share_token`,
		recipeID, userID, fresh,
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

// SharePlan issues (or returns the existing) share token for the caller's week starting
// startsOn (BACKLOG 13.2). Idempotent for the same reason ShareRecipe is: a second tap
// must not invalidate a link already sent. The week row is created if the caller has not
// planned anything yet — a link to an empty week is a link to an empty week, not an error.
func SharePlan(ctx context.Context, userID int, startsOn, endsOn time.Time) (string, error) {
	planID, err := findOrCreateMealPlanRow(ctx, Pool, userID, startsOn, endsOn)
	if err != nil {
		return "", err
	}
	fresh, err := newShareToken()
	if err != nil {
		return "", err
	}
	var token string
	err = Pool.QueryRow(ctx, `
		UPDATE meal_plans SET share_token = COALESCE(share_token, $2)
		WHERE id = $1 RETURNING share_token`, planID, fresh,
	).Scan(&token)
	return token, err
}

// UnsharePlan revokes the week's link. Idempotent, and a no-op for a week never shared.
func UnsharePlan(ctx context.Context, userID int, startsOn time.Time) error {
	_, err := Pool.Exec(ctx, `
		UPDATE meal_plans SET share_token = NULL
		WHERE `+inScope("meal_plans", "$1")+` AND starts_on = $2`, userID, startsOn)
	return err
}

// GetSharedPlan resolves a week share token to the owner and dates behind it, for
// unauthenticated readers. The owner id is what makes it safe to hand to the ordinary
// owner-scoped reads (GetMealPlanRange, GetPlannedIngredients) — same shape as
// GetSharedRecipe. pgx.ErrNoRows for an unknown or revoked token.
func GetSharedPlan(ctx context.Context, token string) (ownerID int, startsOn, endsOn time.Time, err error) {
	if token == "" {
		return 0, time.Time{}, time.Time{}, pgx.ErrNoRows
	}
	err = Pool.QueryRow(ctx, `
		SELECT user_id, starts_on, ends_on FROM meal_plans WHERE share_token = $1`, token,
	).Scan(&ownerID, &startsOn, &endsOn)
	return ownerID, startsOn, endsOn, err
}

// PlanShareToken returns the share token of a week overlapping [startsOn, endsOn], or ""
// when none is shared. Reported alongside the week itself so the planner can offer "stop
// sharing" after a reload instead of only in the session that shared it.
//
// Overlap rather than starts_on equality because the planner deliberately asks for the
// day before the week too (leftovers carry across), so an equality match would never find
// the row it is looking at. NULLS LAST picks the shared row when the find-or-create race
// in mealplan.go has left two for the same week.
func PlanShareToken(ctx context.Context, userID int, startsOn, endsOn time.Time) (string, error) {
	var token *string
	err := Pool.QueryRow(ctx, `
		SELECT share_token FROM meal_plans
		WHERE `+inScope("meal_plans", "$1")+` AND starts_on <= $3 AND ends_on >= $2
		ORDER BY share_token NULLS LAST LIMIT 1`, userID, startsOn, endsOn,
	).Scan(&token)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil || token == nil {
		return "", err
	}
	return *token, nil
}
