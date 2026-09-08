package storage

import (
	"context"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestPreferencesRoundTrip is a smoke test against a real DB (skipped unless DB_URL is
// set) for GetPreferences/UpdatePreferences, including the meal-plan scheduling columns
// added in migrations 00015/00016 — catches a column-order or array-scan mistake that a
// type check alone wouldn't.
func TestPreferencesRoundTrip(t *testing.T) {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-prefs-roundtrip') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	// Defaults for a freshly-provisioned user.
	defaults, err := GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("GetPreferences (defaults): %v", err)
	}
	if len(defaults.MealPlanNoFoodDays) != 0 || len(defaults.MealPlanNoCookDays) != 0 || defaults.MealPlanBatchDays != 1 || defaults.WeekStartDay != "monday" {
		t.Errorf("expected default no_food_days=[] no_cook_days=[] batch_days=1 week_start_day=monday, got %+v", defaults)
	}

	skill := "intermediate"
	prefs := model.UserPreferences{
		SkillLevel:          &skill,
		DietaryPrefs:        []string{"vegan"},
		DislikedIngredients: []string{"cilantro"},
		MealPlanNoFoodDays:  []string{"sunday"},
		MealPlanNoCookDays:  []string{"wednesday"},
		MealPlanBatchDays:   2,
		WeekStartDay:        "sunday",
	}
	if err := UpdatePreferences(ctx, userID, prefs); err != nil {
		t.Fatalf("UpdatePreferences: %v", err)
	}

	got, err := GetPreferences(ctx, userID)
	if err != nil {
		t.Fatalf("GetPreferences: %v", err)
	}
	if got.SkillLevel == nil || *got.SkillLevel != skill {
		t.Errorf("skill_level: got %+v, want %q", got.SkillLevel, skill)
	}
	if len(got.MealPlanNoFoodDays) != 1 || got.MealPlanNoFoodDays[0] != "sunday" {
		t.Errorf("meal_plan_no_food_days: got %v, want [sunday]", got.MealPlanNoFoodDays)
	}
	if len(got.MealPlanNoCookDays) != 1 || got.MealPlanNoCookDays[0] != "wednesday" {
		t.Errorf("meal_plan_no_cook_days: got %v, want [wednesday]", got.MealPlanNoCookDays)
	}
	if got.MealPlanBatchDays != 2 {
		t.Errorf("meal_plan_batch_days: got %d, want 2", got.MealPlanBatchDays)
	}
	if got.WeekStartDay != "sunday" {
		t.Errorf("week_start_day: got %q, want sunday", got.WeekStartDay)
	}
}
