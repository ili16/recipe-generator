package storage

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// Users implements just-in-time user provisioning keyed by the OIDC subject claim.
type Users struct{}

func (Users) EnsureUser(ctx context.Context, subject, email, name, provider string) (int, error) {
	const query = `
		INSERT INTO users (oauth_id, name, email, oauth_provider, last_seen_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (oauth_id) DO UPDATE
		SET name           = COALESCE(NULLIF(EXCLUDED.name, ''), users.name),
		    email          = COALESCE(NULLIF(EXCLUDED.email, ''), users.email),
		    oauth_provider = COALESCE(NULLIF(EXCLUDED.oauth_provider, ''), users.oauth_provider),
		    last_seen_at   = NOW()
		RETURNING id`

	slog.Debug("db: ensure user", "subject", subject, "provider", provider)
	t := time.Now()
	var userID int
	if err := Pool.QueryRow(ctx, query, subject, name, email, provider).Scan(&userID); err != nil {
		return 0, fmt.Errorf("ensure user %s: %w", subject, err)
	}
	slog.Debug("db: ensure user done", "userID", userID, "dur_ms", time.Since(t).Milliseconds())
	return userID, nil
}

// GetPreferences returns a user's explicit profile settings.
//
// Two of them are not personal (BACKLOG 15.1). week_start_day and household_size describe
// the table, not the cook, and inside a household they are read from the kitchen's owner
// so every member sees — and plans against — the same week and the same number of mouths.
// Solo, kitchenOwner is the caller and this is exactly the query it always was.
func GetPreferences(ctx context.Context, userID int) (model.UserPreferences, error) {
	var p model.UserPreferences
	err := Pool.QueryRow(ctx, `
		SELECT me.skill_level, me.dietary_prefs, me.disliked_ingredients,
		       me.meal_plan_no_food_days, me.meal_plan_no_cook_days, me.meal_plan_batch_days,
		       me.language, kitchen.week_start_day, kitchen.household_size
		FROM users me, users kitchen WHERE me.id = $1 AND kitchen.id = $2`,
		userID, kitchenOwner(ctx, Pool, userID),
	).Scan(&p.SkillLevel, &p.DietaryPrefs, &p.DislikedIngredients,
		&p.MealPlanNoFoodDays, &p.MealPlanNoCookDays, &p.MealPlanBatchDays, &p.Language,
		&p.WeekStartDay, &p.HouseholdSize)
	return p, err
}

// UpdatePreferences overwrites a user's profile settings. The two household-wide fields
// are written where GetPreferences reads them, so any member can change the week start or
// the number of people eating and every member sees it — a member editing them into their
// own row instead would be a silent no-op, which is worse than not offering the control.
func UpdatePreferences(ctx context.Context, userID int, p model.UserPreferences) error {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx, `
		UPDATE users
		SET skill_level = $2, dietary_prefs = $3, disliked_ingredients = $4,
		    meal_plan_no_food_days = $5, meal_plan_no_cook_days = $6, meal_plan_batch_days = $7,
		    language = $8
		WHERE id = $1`,
		userID, p.SkillLevel, p.DietaryPrefs, p.DislikedIngredients,
		p.MealPlanNoFoodDays, p.MealPlanNoCookDays, p.MealPlanBatchDays, p.Language); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE users SET week_start_day = $2, household_size = $3 WHERE id = $1`,
		kitchenOwner(ctx, tx, userID), p.WeekStartDay, p.HouseholdSize); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// KitchenDietary returns the dietary preferences and disliked ingredients of everyone
// eating at the caller's table, de-duplicated (BACKLOG 15.1). Solo, that is just the
// caller's own two arrays and this is the same answer GetPreferences gives.
//
// Dislikes are the one preference that must be unioned rather than picked from: a plan
// nobody can eat is worse than no plan, so the household's constraint is every member's
// constraint at once. Skill level and the scheduling fields stay personal — they say how
// the cook works, not what may be served.
func KitchenDietary(ctx context.Context, userID int) (dietary, disliked []string, err error) {
	rows, err := Pool.Query(ctx, `
		SELECT dietary_prefs, disliked_ingredients FROM users u
		WHERE u.id = $1
		   OR (u.household_id IS NOT NULL
		       AND u.household_id = (SELECT household_id FROM users WHERE id = $1))`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	seenD, seenX := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var d, x []string
		if err := rows.Scan(&d, &x); err != nil {
			return nil, nil, err
		}
		for _, v := range d {
			if !seenD[v] {
				seenD[v] = true
				dietary = append(dietary, v)
			}
		}
		for _, v := range x {
			if !seenX[v] {
				seenX[v] = true
				disliked = append(disliked, v)
			}
		}
	}
	return dietary, disliked, rows.Err()
}
