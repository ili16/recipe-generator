package storage

import (
	"context"
	"errors"
	"fmt"
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
		SELECT mpi.id, mpi.recipe_id, r.title, mpi.planned_on, mpi.servings, mpi.meal_slot, mpi.source_item_id
		FROM meal_plan_items mpi
		JOIN meal_plans mp ON mp.id = mpi.meal_plan_id
		JOIN recipes r ON r.id = mpi.recipe_id
		WHERE `+inScope("mp", "$1")+` AND mpi.planned_on BETWEEN $2 AND $3
		ORDER BY mpi.planned_on, `+mealSlotOrder+`, mpi.id`,
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
		if err := rows.Scan(&item.ID, &item.RecipeID, &item.RecipeTitle, &plannedOn, &item.Servings, &item.MealSlot, &item.SourceItemID); err != nil {
			return nil, err
		}
		item.PlannedOn = plannedOn.Format("2006-01-02")
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
		`SELECT id FROM meal_plans WHERE `+inScope("meal_plans", "$1")+` AND starts_on = $2`,
		userID, startsOn,
	).Scan(&planID)
	if err == pgx.ErrNoRows {
		// ponytail: benign find-or-create race (no UNIQUE(user_id, starts_on)) — a
		// concurrent double-insert makes two plan rows for the same week, harmless
		// since GetMealPlanRange reads by date range, not by plan id. Add the unique
		// constraint + ON CONFLICT if this ever actually bites.
		err = q.QueryRow(ctx,
			`INSERT INTO meal_plans (user_id, household_id, title, starts_on, ends_on)
			 VALUES ($1, `+stampHousehold+`, $2, $3, $4) RETURNING id`,
			userID, "Week of "+startsOn.Format("2006-01-02"), startsOn, endsOn,
		).Scan(&planID)
	}
	return planID, err
}

// PlannedDay is one (day → recipe) assignment for ApplyMealPlanDays.
type PlannedDay struct {
	RecipeID  int
	PlannedOn time.Time
	// Servings is how many portions are wanted that day; nil leaves it unset, meaning
	// "as the recipe is written" (BACKLOG 6.2).
	Servings *int
	// MealSlot is which meal of the day this fills; "" means dinner (BACKLOG 6.4).
	MealSlot string
	// LeftoverOf, when set, is the day whose cooking this meal eats again (BACKLOG
	// 9.11). Resolved to that day's item id inside the apply transaction, so a caller
	// can write the cook and its leftovers in one call without knowing ids.
	LeftoverOf *time.Time
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

	// A newly planned day starts from how many people eat, not from whatever the recipe
	// happens to yield (BACKLOG 9.12). Read once, here, because every path that plans a
	// day — the picker, the agent, an applied week — comes through this function.
	// NULL household_size means the user never said, and then the old meaning stands:
	// no servings, "as the recipe is written".
	// Inside a household this is the household's number, not the caller's, or two
	// members planning the same table would size it differently (kitchenOwner, scope.go).
	var household *int
	if err = tx.QueryRow(ctx, `SELECT household_size FROM users WHERE id = $1`,
		kitchenOwner(ctx, tx, userID)).Scan(&household); err != nil {
		return nil, err
	}

	// Cooks first, leftovers second: a leftover's source_item_id is resolved by reading
	// the day it points at, and that day may be written by this very call (BACKLOG 9.13
	// writes a cook and its covered days together). Order in `items` still mirrors
	// `days`, so callers can keep indexing by position.
	items := make([]model.MealPlanItem, len(days))
	order := make([]int, 0, len(days))
	for i, d := range days {
		if d.LeftoverOf == nil {
			order = append(order, i)
		}
	}
	for i, d := range days {
		if d.LeftoverOf != nil {
			order = append(order, i)
		}
	}

	for _, idx := range order {
		d := days[idx]
		// Ownership is checked inside the transaction, per recipe: an unowned id must
		// abort the whole apply, not just skip its day.
		var recipeTitle string
		if err := tx.QueryRow(ctx,
			`SELECT title FROM recipes WHERE id = $2 AND `+inScope("recipes", "$1")+` AND deleted_at IS NULL`,
			userID, d.RecipeID,
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
			WHERE mpi.meal_plan_id = mp.id AND `+inScope("mp", "$1")+` AND mpi.planned_on = $2
			  AND mpi.meal_slot = $3`,
			userID, d.PlannedOn, slot,
		); err != nil {
			return nil, err
		}

		servings := d.Servings
		if servings == nil {
			servings = household
		}

		var sourceID *int
		if d.LeftoverOf != nil {
			if sourceID, err = findCookItem(ctx, tx, userID, *d.LeftoverOf, slot); err != nil {
				return nil, err
			}
		}

		var itemID int
		if err = tx.QueryRow(ctx,
			`INSERT INTO meal_plan_items (meal_plan_id, recipe_id, planned_on, servings, meal_slot, source_item_id)
			 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			planID, d.RecipeID, d.PlannedOn, servings, slot, sourceID,
		).Scan(&itemID); err != nil {
			return nil, err
		}

		items[idx] = model.MealPlanItem{
			ID:           itemID,
			RecipeID:     d.RecipeID,
			RecipeTitle:  recipeTitle,
			PlannedOn:    d.PlannedOn.Format("2006-01-02"),
			Servings:     servings,
			MealSlot:     slot,
			SourceItemID: sourceID,
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// findCookItem resolves "the meal cooked on day" to an item id, for a leftover that
// names its source by date (which is all the agent and the week view can cheaply say).
// Same slot wins; failing that, a day holding exactly one meal is unambiguous. A source
// that is itself a leftover resolves to *its* cook, so chains stay one level deep and
// deleting the pot deletes every portion of it.
func findCookItem(ctx context.Context, tx pgx.Tx, userID int, day time.Time, slot string) (*int, error) {
	var id int
	var source *int
	err := tx.QueryRow(ctx, `
		SELECT mpi.id, mpi.source_item_id
		FROM meal_plan_items mpi
		JOIN meal_plans mp ON mp.id = mpi.meal_plan_id
		WHERE `+inScope("mp", "$1")+` AND mpi.planned_on = $2 AND mpi.meal_slot = $3`,
		userID, day, slot,
	).Scan(&id, &source)
	if err == pgx.ErrNoRows {
		// No meal in that slot: a day holding exactly one meal is still unambiguous.
		// COUNT(*) OVER () is how we tell "the only one" from "the first of several".
		var n int
		err = tx.QueryRow(ctx, `
			SELECT mpi.id, mpi.source_item_id, COUNT(*) OVER ()
			FROM meal_plan_items mpi
			JOIN meal_plans mp ON mp.id = mpi.meal_plan_id
			WHERE `+inScope("mp", "$1")+` AND mpi.planned_on = $2`,
			userID, day,
		).Scan(&id, &source, &n)
		if err == nil && n > 1 {
			return nil, fmt.Errorf("%s holds more than one meal — say which slot the leftovers come from", day.Format("2006-01-02"))
		}
	}
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("nothing is cooked on %s to eat again", day.Format("2006-01-02"))
	}
	if err != nil {
		return nil, err
	}
	if source != nil {
		return source, nil
	}
	return &id, nil
}

// AddMealPlanItem assigns one recipe to one slot of one day — ApplyMealPlanDays with a
// single entry.
func AddMealPlanItem(ctx context.Context, userID, recipeID int, plannedOn time.Time, weekStartDay, mealSlot string, servings *int) (model.MealPlanItem, error) {
	items, err := ApplyMealPlanDays(ctx, userID, weekStartDay,
		[]PlannedDay{{RecipeID: recipeID, PlannedOn: plannedOn, Servings: servings, MealSlot: mealSlot}})
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
		WHERE mpi.id = $2 AND mpi.meal_plan_id = mp.id AND `+inScope("mp", "$1"),
		userID, itemID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SetBatchCook makes one cooking day cover a set of later days, in one transaction
// (BACKLOG 9.13). days is the **complete** set of days this pot covers, so the same call
// undoes the arrangement: a day that drops out of the list has its leftover row cleared,
// and an empty list takes it back to an ordinary single-day cook.
//
// It is deliberately not a caller of ApplyMealPlanDays: that upserts by deleting the
// existing row first, and deleting the cook cascades away the very leftovers this is
// writing. Here the cook is updated in place, so its id — which every leftover points at
// — survives.
//
// Returns the cook and its leftovers as they now stand.
func SetBatchCook(ctx context.Context, userID, itemID int, days []time.Time, servings *int) ([]model.MealPlanItem, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// The cook, scoped to the caller. A leftover may not itself become a cook: the pot it
	// came from is the one that gets scaled (BACKLOG 9.11).
	var cook model.MealPlanItem
	var cookDate time.Time
	var source *int
	if err = tx.QueryRow(ctx, `
		SELECT mpi.id, mpi.recipe_id, r.title, mpi.planned_on, mpi.servings, mpi.meal_slot, mpi.source_item_id
		FROM meal_plan_items mpi
		JOIN meal_plans mp ON mp.id = mpi.meal_plan_id
		JOIN recipes r ON r.id = mpi.recipe_id
		WHERE mpi.id = $2 AND `+inScope("mp", "$1"),
		userID, itemID,
	).Scan(&cook.ID, &cook.RecipeID, &cook.RecipeTitle, &cookDate, &cook.Servings, &cook.MealSlot, &source); err != nil {
		return nil, err
	}
	cook.PlannedOn = cookDate.Format("2006-01-02")
	if source != nil {
		return nil, errors.New("that meal is leftovers — scale the day it was cooked on instead")
	}
	for _, d := range days {
		if !d.After(cookDate) {
			return nil, errors.New("leftovers are eaten after the day they are cooked")
		}
	}

	// Days that dropped out of the list are no longer covered.
	if _, err = tx.Exec(ctx,
		`DELETE FROM meal_plan_items WHERE source_item_id = $1 AND planned_on <> ALL($2::date[])`,
		itemID, days,
	); err != nil {
		return nil, err
	}

	if servings != nil {
		if _, err = tx.Exec(ctx, `UPDATE meal_plan_items SET servings = $2 WHERE id = $1`, itemID, *servings); err != nil {
			return nil, err
		}
		cook.Servings = servings
	}

	items := []model.MealPlanItem{cook}
	for _, d := range days {
		// Whatever else was in that slot goes — the client confirms that first, exactly
		// as an ordinary reassignment does (BACKLOG 9.10).
		if _, err = tx.Exec(ctx, `
			DELETE FROM meal_plan_items mpi
			USING meal_plans mp
			WHERE mpi.meal_plan_id = mp.id AND `+inScope("mp", "$1")+` AND mpi.planned_on = $2
			  AND mpi.meal_slot = $3 AND mpi.source_item_id IS DISTINCT FROM $4`,
			userID, d, cook.MealSlot, itemID,
		); err != nil {
			return nil, err
		}

		startsOn := StartOfWeek(d, weekStartOf(ctx, tx, userID))
		planID, err := findOrCreateMealPlanRow(ctx, tx, userID, startsOn, startsOn.AddDate(0, 0, 6))
		if err != nil {
			return nil, err
		}
		var id int
		// A day already covered by this cook stays as it is, id included, so re-confirming
		// the same set of days changes nothing.
		if err = tx.QueryRow(ctx, `
			INSERT INTO meal_plan_items (meal_plan_id, recipe_id, planned_on, meal_slot, source_item_id)
			SELECT $1, $2, $3, $4, $5
			WHERE NOT EXISTS (
				SELECT 1 FROM meal_plan_items
				WHERE planned_on = $3 AND meal_slot = $4 AND source_item_id = $5)
			RETURNING id`,
			planID, cook.RecipeID, d, cook.MealSlot, itemID,
		).Scan(&id); err == pgx.ErrNoRows {
			err = tx.QueryRow(ctx,
				`SELECT id FROM meal_plan_items WHERE planned_on = $1 AND meal_slot = $2 AND source_item_id = $3`,
				d, cook.MealSlot, itemID,
			).Scan(&id)
		}
		if err != nil {
			return nil, err
		}
		items = append(items, model.MealPlanItem{
			ID:           id,
			RecipeID:     cook.RecipeID,
			RecipeTitle:  cook.RecipeTitle,
			PlannedOn:    d.Format("2006-01-02"),
			MealSlot:     cook.MealSlot,
			SourceItemID: &cook.ID,
		})
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return items, nil
}

// weekStartOf reads the user's week_start_day, falling back to Monday — StartOfWeek's own
// fallback — if it can't be read, since a week's start only decides which meal_plans row
// a day is filed under.
func weekStartOf(ctx context.Context, q queryRower, userID int) string {
	var day string
	if err := q.QueryRow(ctx, `SELECT week_start_day FROM users WHERE id = $1`,
		kitchenOwner(ctx, q, userID)).Scan(&day); err != nil {
		return "monday"
	}
	return day
}
