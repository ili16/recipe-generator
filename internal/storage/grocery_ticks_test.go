package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestGroceryTicksAreShared is BACKLOG 15.9's acceptance, both halves: two members of one
// household see each other's ticks, and the line lands in the pantry exactly once however
// many of them tick it.
//
// The second half is the one worth a test. Before this, every device wrote its own pantry
// row for a ticked line, so two shoppers ticking the milk produced two writes and the
// first untick deleted a row the other still believed in. The unique index in 00045 is
// what stops that, and this is the check that it is actually doing it.
func TestGroceryTicksAreShared(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	seed := func(oauth string) int {
		var id int
		if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ($1) RETURNING id`, oauth).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", oauth, err)
		}
		t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	alice, bob, stranger := seed("test-ticks-alice"), seed("test-ticks-bob"), seed("test-ticks-stranger")

	hh, err := CreateHousehold(ctx, alice, "The Shop")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM households WHERE id = $1::uuid`, hh.ID) })
	if _, err := JoinHousehold(ctx, bob, hh.JoinCode); err != nil {
		t.Fatalf("JoinHousehold: %v", err)
	}

	week := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	milk := model.PantryItem{Name: "Milk", Quantity: f(2), Unit: u("l"), Category: "fridge"}
	t.Cleanup(func() {
		Pool.Exec(ctx, `DELETE FROM pantry_items WHERE user_id = ANY($1)`, []int{alice, bob, stranger})
	})

	// Alice ticks the milk in the shop.
	first, err := TickGroceryLine(ctx, alice, week, "milk|l", milk)
	if err != nil {
		t.Fatalf("alice ticks: %v", err)
	}
	if first.PantryItemID == nil {
		t.Fatal("alice's tick wrote no pantry row")
	}

	// Bob, in the same shop, sees it — the whole point of the entry.
	bobSees, err := ListGroceryTicks(ctx, bob, week)
	if err != nil {
		t.Fatalf("bob lists: %v", err)
	}
	if len(bobSees) != 1 || bobSees[0].LineKey != "milk|l" {
		t.Fatalf("bob does not see alice's tick: %+v", bobSees)
	}
	if bobSees[0].Mine {
		t.Errorf("alice's tick is reported as bob's own: %+v", bobSees[0])
	}
	if aliceSees, err := ListGroceryTicks(ctx, alice, week); err != nil || len(aliceSees) != 1 || !aliceSees[0].Mine {
		t.Errorf("alice's own tick is not hers: %+v (%v)", aliceSees, err)
	}

	// A stranger's kitchen is untouched by any of it.
	if ticks, err := ListGroceryTicks(ctx, stranger, week); err != nil || len(ticks) != 0 {
		t.Fatalf("a stranger sees the household's ticks: %+v (%v)", ticks, err)
	}

	// Bob taps it anyway, before his screen refreshed. Exactly once: same tick, same
	// pantry row, no second write.
	second, err := TickGroceryLine(ctx, bob, week, "milk|l", milk)
	if err != nil {
		t.Fatalf("bob ticks: %v", err)
	}
	if second.PantryItemID == nil || *second.PantryItemID != *first.PantryItemID {
		t.Fatalf("bob's tick made a second pantry row: %v vs %v", second.PantryItemID, first.PantryItemID)
	}
	var ticks, pantryRows int
	Pool.QueryRow(ctx, `SELECT count(*) FROM grocery_ticks WHERE week_start = $1`, week).Scan(&ticks)
	Pool.QueryRow(ctx, `SELECT count(*) FROM pantry_items WHERE household_id = $1::uuid`, hh.ID).Scan(&pantryRows)
	if ticks != 1 || pantryRows != 1 {
		t.Errorf("two taps made %d ticks and %d pantry rows, want 1 and 1", ticks, pantryRows)
	}

	// Either of them can put it back, and it leaves the pantry with it.
	if err := UntickGroceryLine(ctx, bob, week, "milk|l"); err != nil {
		t.Fatalf("bob unticks: %v", err)
	}
	if left, err := ListGroceryTicks(ctx, alice, week); err != nil || len(left) != 0 {
		t.Fatalf("alice still sees the tick after bob untick: %+v (%v)", left, err)
	}
	Pool.QueryRow(ctx, `SELECT count(*) FROM pantry_items WHERE household_id = $1::uuid`, hh.ID).Scan(&pantryRows)
	if pantryRows != 0 {
		t.Errorf("unticking left %d pantry rows behind", pantryRows)
	}
}

// TestGroceryTicksSoloCookIsUnchanged: the scope predicate has to collapse to "mine" for
// a cook with no household, the same way every other shared read does (scope.go).
func TestGroceryTicksSoloCookIsUnchanged(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var me, other int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-ticks-solo') RETURNING id`).Scan(&me); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, me) })
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-ticks-solo-other') RETURNING id`).Scan(&other); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, other) })
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM pantry_items WHERE user_id = ANY($1)`, []int{me, other}) })

	week := time.Date(2027, 4, 5, 0, 0, 0, 0, time.UTC)
	if _, err := TickGroceryLine(ctx, me, week, "eggs|", model.PantryItem{Name: "Eggs", Quantity: f(6), Category: "fridge"}); err != nil {
		t.Fatalf("tick: %v", err)
	}

	mine, err := ListGroceryTicks(ctx, me, week)
	if err != nil || len(mine) != 1 {
		t.Fatalf("my own tick did not come back: %+v (%v)", mine, err)
	}
	// Two solo cooks share a scope key only if the COALESCE is wrong.
	if theirs, err := ListGroceryTicks(ctx, other, week); err != nil || len(theirs) != 0 {
		t.Fatalf("another solo cook sees my ticks: %+v (%v)", theirs, err)
	}
	// Both can hold the same line in the same week without colliding on the index.
	if _, err := TickGroceryLine(ctx, other, week, "eggs|", model.PantryItem{Name: "Eggs", Quantity: f(6), Category: "fridge"}); err != nil {
		t.Fatalf("the other cook cannot tick the same line: %v", err)
	}
}
