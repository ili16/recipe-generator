package storage

import (
	"context"
	"errors"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// Ticked-off grocery lines (BACKLOG 15.9). A tick used to live in the device's
// AsyncStorage, which is right for one cook and wrong for a household: two people
// splitting the shop never see each other's, so one of them buys the milk twice.
//
// Scoped like every other shared thing (inScope, scope.go): the household's ticks when
// the caller is in one, their own otherwise. For a solo cook this is exactly what the
// device used to hold, now on the server and shared across their own devices.
//
// The uniqueness index from 00045 -- one row per (scope, week, line) -- is what makes the
// pantry write happen exactly once no matter how many members tick the same line.

// ListGroceryTicks returns the kitchen's ticks for one shopping week.
func ListGroceryTicks(ctx context.Context, userID int, weekStart time.Time) ([]model.GroceryTick, error) {
	rows, err := Pool.Query(ctx, `
		SELECT line_key, pantry_item_id, user_id = $1
		FROM grocery_ticks
		WHERE week_start = $2 AND `+inScope("grocery_ticks", "$1"), userID, weekStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ticks := []model.GroceryTick{}
	for rows.Next() {
		var t model.GroceryTick
		if err := rows.Scan(&t.LineKey, &t.PantryItemID, &t.Mine); err != nil {
			return nil, err
		}
		ticks = append(ticks, t)
	}
	return ticks, rows.Err()
}

// TickGroceryLine marks one line bought and puts it in the kitchen's pantry (BACKLOG 7.2).
//
// Idempotent by construction: the insert takes the scope's single slot for this line and
// week, and a caller who loses that race gets the winner's tick back -- the same
// pantry_item_id, no second pantry write, nothing to reconcile. That is the "exactly once"
// half of 15.9's acceptance, and it is the index doing it rather than any code here.
//
// The pantry write deliberately runs outside a transaction with the tick. If it fails the
// tick stands with a nil pantry_item_id, which is precisely the state every tick had
// before 7.2 -- a shopper in a supermarket cares that the line went grey, not that a
// fridge inventory stayed consistent with it.
func TickGroceryLine(ctx context.Context, userID int, weekStart time.Time, lineKey string, item model.PantryItem) (model.GroceryTick, error) {
	var id int
	err := Pool.QueryRow(ctx, `
		INSERT INTO grocery_ticks (user_id, household_id, week_start, line_key)
		VALUES ($1, `+stampHousehold+`, $2, $3)
		ON CONFLICT DO NOTHING
		RETURNING id`, userID, weekStart, lineKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Somebody in this kitchen already ticked it. Theirs wins, pantry row included.
		return getGroceryTick(ctx, userID, weekStart, lineKey)
	}
	if err != nil {
		return model.GroceryTick{}, err
	}

	tick := model.GroceryTick{LineKey: lineKey, Mine: true}
	pantryItem, err := UpsertPantryItem(ctx, userID, item)
	if err != nil {
		return tick, nil
	}
	if _, err := Pool.Exec(ctx, `UPDATE grocery_ticks SET pantry_item_id = $2 WHERE id = $1`, id, pantryItem.ID); err != nil {
		return tick, nil
	}
	tick.PantryItemID = &pantryItem.ID
	return tick, nil
}

// UntickGroceryLine clears one line for the whole kitchen and takes its pantry row back
// out. Unticking a line somebody else ticked is allowed on purpose: one list, one shop,
// and "I put that back" is a thing either of them can say.
func UntickGroceryLine(ctx context.Context, userID int, weekStart time.Time, lineKey string) error {
	var pantryItemID *int
	err := Pool.QueryRow(ctx, `
		DELETE FROM grocery_ticks
		WHERE week_start = $2 AND line_key = $3 AND `+inScope("grocery_ticks", "$1")+`
		RETURNING pantry_item_id`, userID, weekStart, lineKey).Scan(&pantryItemID)
	if err != nil {
		return err
	}
	if pantryItemID == nil {
		return nil
	}
	// Already gone is the outcome we wanted, so a missing row is not an error here.
	if err := DeletePantryItem(ctx, userID, *pantryItemID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

func getGroceryTick(ctx context.Context, userID int, weekStart time.Time, lineKey string) (model.GroceryTick, error) {
	var t model.GroceryTick
	err := Pool.QueryRow(ctx, `
		SELECT line_key, pantry_item_id, user_id = $1
		FROM grocery_ticks
		WHERE week_start = $2 AND line_key = $3 AND `+inScope("grocery_ticks", "$1"),
		userID, weekStart, lineKey).Scan(&t.LineKey, &t.PantryItemID, &t.Mine)
	return t, err
}
