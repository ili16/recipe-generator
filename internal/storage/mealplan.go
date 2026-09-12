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
		SELECT mpi.id, mpi.recipe_id, r.title, mpi.planned_on, mpi.start_time, mpi.servings, mpi.meal_slot
		FROM meal_plan_items mpi
		JOIN meal_plans mp ON mp.id = mpi.meal_plan_id
		JOIN recipes r ON r.id = mpi.recipe_id
		WHERE mp.user_id = $1 AND mpi.planned_on BETWEEN $2 AND $3
		ORDER BY mpi.planned_on, `+mealSlotOrder+`, mpi.start_time`,
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
		if err := rows.Scan(&item.ID, &item.RecipeID, &item.RecipeTitle, &plannedOn, &startTime, &item.Servings, &item.MealSlot); err != nil {
			return nil, err
		}
		item.PlannedOn = plannedOn.Format("2006-01-02")
		item.StartTime = startTime.Format("15:04:05")
		items = append(items, item)
	}
	return items, rows.Err()
}

// MealSlots are the meal-of-day values meal_plan_items.meal_slot accepts (the DB CHECK
// constraint enforces the same set), in the order a day is eaten.
var MealSlots = []string{"breakfast", "lunch", "dinner", "snack"}

// DefaultMealSlot is what an item with no slot given fills — everything before BACKLOG
// 6.4 wrote exactly this, so an unqualified assignment keeps its old meaning.
const DefaultMealSlot = "dinner"

// ValidMealSlot reports whether slot is one of MealSlots.
func ValidMealSlot(slot string) bool {
	for _, s := range MealSlots {
		if s == slot {
			return true
		}
	}
	return false
}

// mealSlotOrder sorts a day's items the way the day is eaten rather than alphabetically,
// so "the last meal of the day" is genuinely the last row.
const mealSlotOrder = `CASE mpi.meal_slot WHEN 'breakfast' THEN 0 WHEN 'lunch' THEN 1 WHEN 'dinner' THEN 2 ELSE 3 END`

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

// PlannedDay is one (day → recipe) assignment for ApplyMealPlanDays.
type PlannedDay struct {
	RecipeID  int
	PlannedOn time.Time
	StartTime string
	// Servings is how many portions are wanted that day; nil leaves it unset, meaning
	// "as the recipe is written" (BACKLOG 6.2).
	Servings *int
	// MealSlot is which meal of the day this fills; "" means dinner (BACKLOG 6.4).
	MealSlot string
}

// ApplyMealPlanDays writes every day in days for userID in **one transaction**, creating
// each week's meal_plans row (starting on weekStartDay) as needed. Upserts by day: any
// existing item(s) for (userID, day) are replaced, so assigning an already-filled day
// needs no separate delete step. All-or-nothing is the point — a whole-week apply that
// fails on day 5 must not leave days 1–4 written (BACKLOG.md 4.3).
func ApplyMealPlanDays(ctx context.Context, userID int, weekStartDay string, days []PlannedDay) ([]model.MealPlanItem, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	items := make([]model.MealPlanItem, 0, len(days))
	for _, d := range days {
		// Ownership is checked inside the transaction, per recipe: an unowned id must
		// abort the whole apply, not just skip its day.
		var recipeTitle string
		if err := tx.QueryRow(ctx,
			`SELECT title FROM recipes WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`,
			d.RecipeID, userID,
		).Scan(&recipeTitle); err != nil {
			return nil, err
		}

		startsOn := StartOfWeek(d.PlannedOn, weekStartDay)
		planID, err := findOrCreateMealPlanRow(ctx, tx, userID, startsOn, startsOn.AddDate(0, 0, 6))
		if err != nil {
			return nil, err
		}

		// Upsert by (day, slot): a day holds at most one item per meal slot, so
		// replacing whatever's in *that* slot is the natural "assign/swap" semantics —
		// and what makes re-applying the same plan a no-op rather than a duplicate.
		// Keying on the slot too is the whole of BACKLOG 6.4: planning a lunch no
		// longer silently deletes that day's dinner.
		slot := d.MealSlot
		if slot == "" {
			slot = DefaultMealSlot
		}
		if _, err = tx.Exec(ctx, `
			DELETE FROM meal_plan_items mpi
			USING meal_plans mp
			WHERE mpi.meal_plan_id = mp.id AND mp.user_id = $1 AND mpi.planned_on = $2
			  AND mpi.meal_slot = $3`,
			userID, d.PlannedOn, slot,
		); err != nil {
			return nil, err
		}

		var itemID int
		if err = tx.QueryRow(ctx,
			`INSERT INTO meal_plan_items (meal_plan_id, recipe_id, planned_on, start_time, servings, meal_slot)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			planID, d.RecipeID, d.PlannedOn, d.StartTime, d.Servings, slot,
		).Scan(&itemID); err != nil {
			return nil, err
		}

		items = append(items, model.MealPlanItem{
			ID:          itemID,
			RecipeID:    d.RecipeID,
			RecipeTitle: recipeTitle,
			PlannedOn:   d.PlannedOn.Format("2006-01-02"),
			StartTime:   d.StartTime,
			Servings:    d.Servings,
			MealSlot:    slot,
		})
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// AddMealPlanItem assigns one recipe to one slot of one day — ApplyMealPlanDays with a
// single entry.
func AddMealPlanItem(ctx context.Context, userID, recipeID int, plannedOn time.Time, startTime, weekStartDay, mealSlot string, servings *int) (model.MealPlanItem, error) {
	items, err := ApplyMealPlanDays(ctx, userID, weekStartDay,
		[]PlannedDay{{RecipeID: recipeID, PlannedOn: plannedOn, StartTime: startTime, Servings: servings, MealSlot: mealSlot}})
	if err != nil {
		return model.MealPlanItem{}, err
	}
	return items[0], nil
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
