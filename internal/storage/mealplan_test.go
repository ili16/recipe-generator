package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// TestMealPlanWeekAssignAndRemove is a smoke test against a real DB (skipped unless
// DB_URL is set) for the meal-planner slice: AddMealPlanItem creates the week's
// meal_plans row implicitly and upserts by day, GetMealPlanRange's date-range join
// returns items for an arbitrary range (and excludes items outside it), ownership is
// enforced on add and delete.
func TestMealPlanWeekAssignAndRemove(t *testing.T) {
	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID, otherUserID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-mealplan-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-mealplan-other') RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherUserID) })

	doc := model.RecipeDocument{
		Title:       "Test Curry",
		Language:    "en",
		Ingredients: []model.RecipeIngredient{{Item: "Rice"}},
		Steps:       []model.RecipeStep{{SortOrder: 1, StepText: "Cook it"}},
	}
	recipe, err := AddRecipeStructured(ctx, userID, doc, "# Test Curry", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe.ID) })

	doc2 := doc
	doc2.Title = "Test Stir Fry"
	recipe2, err := AddRecipeStructured(ctx, userID, doc2, "# Test Stir Fry", nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("AddRecipeStructured (second recipe): %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM recipes WHERE id = $1`, recipe2.ID) })

	// A Tuesday, so the derived week start should land on the preceding Monday.
	tuesday := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	sunday := monday.AddDate(0, 0, 6)
	nextMonday := monday.AddDate(0, 0, 7)

	// Ownership enforced on add: another user can't assign someone else's recipe.
	if _, err := AddMealPlanItem(ctx, otherUserID, recipe.ID, tuesday, "18:00:00", "monday"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows adding another user's recipe, got %v", err)
	}

	item, err := AddMealPlanItem(ctx, userID, recipe.ID, tuesday, "18:00:00", "monday")
	if err != nil {
		t.Fatalf("AddMealPlanItem: %v", err)
	}
	if item.RecipeTitle != "Test Curry" || item.PlannedOn != "2026-09-08" || item.StartTime != "18:00:00" {
		t.Errorf("unexpected item: %+v", item)
	}

	// Upsert-by-day: assigning a second recipe to the same day replaces the first,
	// rather than stacking a second item.
	replaced, err := AddMealPlanItem(ctx, userID, recipe2.ID, tuesday, "19:30:00", "monday")
	if err != nil {
		t.Fatalf("AddMealPlanItem (replace): %v", err)
	}
	if replaced.RecipeTitle != "Test Stir Fry" || replaced.StartTime != "19:30:00" {
		t.Errorf("unexpected replaced item: %+v", replaced)
	}

	// An item planned for the following week must not leak into this week's range.
	if _, err := AddMealPlanItem(ctx, userID, recipe.ID, nextMonday, "18:00:00", "monday"); err != nil {
		t.Fatalf("AddMealPlanItem (next week): %v", err)
	}

	week, err := GetMealPlanRange(ctx, userID, monday, sunday)
	if err != nil {
		t.Fatalf("GetMealPlanRange: %v", err)
	}
	if len(week) != 1 || week[0].ID != replaced.ID {
		t.Fatalf("expected exactly this week's 1 (replaced) item, got %+v", week)
	}

	// A range spanning both weeks sees both items.
	twoWeeks, err := GetMealPlanRange(ctx, userID, monday, nextMonday)
	if err != nil {
		t.Fatalf("GetMealPlanRange (two weeks): %v", err)
	}
	if len(twoWeeks) != 2 {
		t.Fatalf("expected 2 items across the two-week range, got %+v", twoWeeks)
	}

	// Ownership enforced on delete: another user can't remove it.
	if err := DeleteMealPlanItem(ctx, otherUserID, replaced.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected pgx.ErrNoRows deleting another user's item, got %v", err)
	}

	if err := DeleteMealPlanItem(ctx, userID, replaced.ID); err != nil {
		t.Fatalf("DeleteMealPlanItem: %v", err)
	}
	week, err = GetMealPlanRange(ctx, userID, monday, sunday)
	if err != nil {
		t.Fatalf("GetMealPlanRange after delete: %v", err)
	}
	if len(week) != 0 {
		t.Fatalf("expected item removed, got %+v", week)
	}
}

func TestStartOfWeek(t *testing.T) {
	tuesday := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	sunday := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)

	if got := StartOfWeek(tuesday, "monday"); !got.Equal(monday) {
		t.Errorf("StartOfWeek(tuesday, monday) = %v, want %v", got, monday)
	}
	if got := StartOfWeek(tuesday, "sunday"); !got.Equal(sunday) {
		t.Errorf("StartOfWeek(tuesday, sunday) = %v, want %v (a Sunday-start week begun the day before)", got, sunday)
	}
	if got := StartOfWeek(tuesday, "tuesday"); !got.Equal(tuesday) {
		t.Errorf("StartOfWeek(tuesday, tuesday) = %v, want %v (already the start day)", got, tuesday)
	}
	// Unrecognized weekStartDay falls back to Monday.
	if got := StartOfWeek(tuesday, ""); !got.Equal(monday) {
		t.Errorf("StartOfWeek(tuesday, \"\") = %v, want %v (default to Monday)", got, monday)
	}
}
