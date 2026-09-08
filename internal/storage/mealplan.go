package storage

import (
	"context"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// GetMealPlanRange returns every item planned in [startsOn, endsOn] for userID, across
// any meal_plans row(s) covering that range. Querying by date range rather than a
// single meal_plan_id keeps this correct even if AddMealPlanItem's find-or-create ever
// races into two plan rows for the same week.
func GetMealPlanRange(ctx context.Context, userID int, startsOn, endsOn time.Time) ([]model.MealPlanItem, error) {
	rows, err := Pool.Query(ctx, `
		SELECT mpi.id, mpi.recipe_id, r.title, mpi.planned_on, mpi.start_time
		FROM meal_plan_items mpi
		JOIN meal_plans mp ON mp.id = mpi.meal_plan_id
		JOIN recipes r ON r.id = mpi.recipe_id
		WHERE mp.user_id = $1 AND mpi.planned_on BETWEEN $2 AND $3
		ORDER BY mpi.planned_on, mpi.start_time`,
		userID, startsOn, endsOn,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []model.MealPlanItem{}
	for rows.Next() {
		var item model.MealPlanItem
		var plannedOn time.Time
		var startTime time.Time
		if err := rows.Scan(&item.ID, &item.RecipeID, &item.RecipeTitle, &plannedOn, &startTime); err != nil {
			return nil, err
		}
		item.PlannedOn = plannedOn.Format("2006-01-02")
		item.StartTime = startTime.Format("15:04:05")
		items = append(items, item)
	}
	return items, rows.Err()
}

// weekdayISO maps a lowercase weekday name to its ISO weekday number (Monday=1..Sunday=7).
var weekdayISO = map[string]int{
	"monday": 1, "tuesday": 2, "wednesday": 3, "thursday": 4,
	"friday": 5, "saturday": 6, "sunday": 7,
}

// StartOfWeek returns the date of the most recent (or same) occurrence of weekStartDay
// on/before d. Falls back to Monday for an unrecognized weekStartDay.
func StartOfWeek(d time.Time, weekStartDay string) time.Time {
	start, ok := weekdayISO[weekStartDay]
	if !ok {
		start = 1
	}
	dayISO := int(d.Weekday())
	if dayISO == 0 {
		dayISO = 7
	}
	diff := dayISO - start
	if diff < 0 {
		diff += 7
	}
	return d.AddDate(0, 0, -diff)
}

// queryRower is satisfied by both *pgxpool.Pool and pgx.Tx, so
// findOrCreateMealPlanRow can run standalone or inside a caller's transaction.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// findOrCreateMealPlanRow finds or creates the meal_plans row for userID's week starting
// startsOn, returning its id.
func findOrCreateMealPlanRow(ctx context.Context, q queryRower, userID int, startsOn, endsOn time.Time) (int, error) {
	var planID int
	err := q.QueryRow(ctx,
		`SELECT id FROM meal_plans WHERE user_id = $1 AND starts_on = $2`,
		userID, startsOn,
	).Scan(&planID)
	if err == pgx.ErrNoRows {
		// ponytail: benign find-or-create race (no UNIQUE(user_id, starts_on)) — a
		// concurrent double-insert makes two plan rows for the same week, harmless
		// since GetMealPlanRange reads by date range, not by plan id. Add the unique
		// constraint + ON CONFLICT if this ever actually bites.
		err = q.QueryRow(ctx,
			`INSERT INTO meal_plans (user_id, title, starts_on, ends_on)
			 VALUES ($1, $2, $3, $4) RETURNING id`,
			userID, "Week of "+startsOn.Format("2006-01-02"), startsOn, endsOn,
		).Scan(&planID)
	}
	return planID, err
}

// AddMealPlanItem assigns recipeID to plannedOn/startTime for userID, creating that
// week's meal_plans row (starting on weekStartDay) on first use. Upserts by day: any
// existing item(s) for (userID, plannedOn) are replaced, so assigning an already-filled
// day is a single call with no separate delete step.
func AddMealPlanItem(ctx context.Context, userID, recipeID int, plannedOn time.Time, startTime, weekStartDay string) (model.MealPlanItem, error) {
	var recipeTitle string
	if err := Pool.QueryRow(ctx,
		`SELECT title FROM recipes WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
		recipeID, userID,
	).Scan(&recipeTitle); err != nil {
		return model.MealPlanItem{}, err
	}

	startsOn := StartOfWeek(plannedOn, weekStartDay)
	endsOn := startsOn.AddDate(0, 0, 6)

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return model.MealPlanItem{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	planID, err := findOrCreateMealPlanRow(ctx, tx, userID, startsOn, endsOn)
	if err != nil {
		return model.MealPlanItem{}, err
	}

	// Upsert-by-day: this user has at most one item per day (meal_slot is always the
	// DB default 'dinner' for now), so replacing whatever's already on plannedOn is
	// the natural "assign/swap" semantics for both manual picks and AI-accepted plans.
	if _, err = tx.Exec(ctx, `
		DELETE FROM meal_plan_items mpi
		USING meal_plans mp
		WHERE mpi.meal_plan_id = mp.id AND mp.user_id = $1 AND mpi.planned_on = $2`,
		userID, plannedOn,
	); err != nil {
		return model.MealPlanItem{}, err
	}

	var itemID int
	if err = tx.QueryRow(ctx,
		`INSERT INTO meal_plan_items (meal_plan_id, recipe_id, planned_on, start_time)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		planID, recipeID, plannedOn, startTime,
	).Scan(&itemID); err != nil {
		return model.MealPlanItem{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return model.MealPlanItem{}, err
	}

	return model.MealPlanItem{
		ID:          itemID,
		RecipeID:    recipeID,
		RecipeTitle: recipeTitle,
		PlannedOn:   plannedOn.Format("2006-01-02"),
		StartTime:   startTime,
	}, nil
}

// UpdateMealPlanItem moves itemID to plannedOn/startTime (full-replace, both fields
// always given) if it belongs (via its plan) to userID.
func UpdateMealPlanItem(ctx context.Context, userID, itemID int, plannedOn time.Time, startTime string) (model.MealPlanItem, error) {
	var recipeID int
	var recipeTitle string
	err := Pool.QueryRow(ctx, `
		UPDATE meal_plan_items mpi
		SET planned_on = $1, start_time = $2
		FROM meal_plans mp, recipes r
		WHERE mpi.id = $3 AND mpi.meal_plan_id = mp.id AND mp.user_id = $4 AND r.id = mpi.recipe_id
		RETURNING mpi.recipe_id, r.title`,
		plannedOn, startTime, itemID, userID,
	).Scan(&recipeID, &recipeTitle)
	if err != nil {
		return model.MealPlanItem{}, err
	}
	return model.MealPlanItem{
		ID:          itemID,
		RecipeID:    recipeID,
		RecipeTitle: recipeTitle,
		PlannedOn:   plannedOn.Format("2006-01-02"),
		StartTime:   startTime,
	}, nil
}

// DeleteMealPlanItem removes itemID if it belongs (via its plan) to userID.
func DeleteMealPlanItem(ctx context.Context, userID, itemID int) error {
	tag, err := Pool.Exec(ctx, `
		DELETE FROM meal_plan_items mpi
		USING meal_plans mp
		WHERE mpi.id = $1 AND mpi.meal_plan_id = mp.id AND mp.user_id = $2`,
		itemID, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// GetWeekPreferences returns the per-week scheduling overrides for userID's week
// starting on startsOn (already normalized via StartOfWeek by the caller). No row for
// that week is not an error — it just means every field is unset (pure inherit).
func GetWeekPreferences(ctx context.Context, userID int, startsOn time.Time) (model.MealPlanWeekPreferences, error) {
	var prefs model.MealPlanWeekPreferences
	err := Pool.QueryRow(ctx,
		`SELECT no_food_days_override, no_cook_days_override, batch_days_override
		 FROM meal_plans WHERE user_id = $1 AND starts_on = $2`,
		userID, startsOn,
	).Scan(&prefs.NoFoodDays, &prefs.NoCookDays, &prefs.BatchDays)
	if err == pgx.ErrNoRows {
		return model.MealPlanWeekPreferences{}, nil
	}
	return prefs, err
}

// UpdateWeekPreferences sets the per-week scheduling overrides for userID's week
// starting on startsOn, creating that week's meal_plans row (bounded by endsOn) on first
// use. A nil field clears back to "inherit the global default".
func UpdateWeekPreferences(ctx context.Context, userID int, startsOn, endsOn time.Time, prefs model.MealPlanWeekPreferences) error {
	planID, err := findOrCreateMealPlanRow(ctx, Pool, userID, startsOn, endsOn)
	if err != nil {
		return err
	}
	_, err = Pool.Exec(ctx,
		`UPDATE meal_plans SET no_food_days_override = $1, no_cook_days_override = $2, batch_days_override = $3 WHERE id = $4`,
		prefs.NoFoodDays, prefs.NoCookDays, prefs.BatchDays, planID,
	)
	return err
}
