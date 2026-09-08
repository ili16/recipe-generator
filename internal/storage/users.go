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
func GetPreferences(ctx context.Context, userID int) (model.UserPreferences, error) {
	var p model.UserPreferences
	err := Pool.QueryRow(ctx, `
		SELECT skill_level, dietary_prefs, disliked_ingredients, cooking_cadence,
		       meal_plan_no_food_days, meal_plan_no_cook_days, meal_plan_batch_days, week_start_day
		FROM users WHERE id = $1`, userID,
	).Scan(&p.SkillLevel, &p.DietaryPrefs, &p.DislikedIngredients, &p.CookingCadence,
		&p.MealPlanNoFoodDays, &p.MealPlanNoCookDays, &p.MealPlanBatchDays, &p.WeekStartDay)
	return p, err
}

// UpdatePreferences overwrites a user's profile settings.
func UpdatePreferences(ctx context.Context, userID int, p model.UserPreferences) error {
	_, err := Pool.Exec(ctx, `
		UPDATE users
		SET skill_level = $2, dietary_prefs = $3, disliked_ingredients = $4, cooking_cadence = $5,
		    meal_plan_no_food_days = $6, meal_plan_no_cook_days = $7, meal_plan_batch_days = $8, week_start_day = $9
		WHERE id = $1`,
		userID, p.SkillLevel, p.DietaryPrefs, p.DislikedIngredients, p.CookingCadence,
		p.MealPlanNoFoodDays, p.MealPlanNoCookDays, p.MealPlanBatchDays, p.WeekStartDay)
	return err
}
