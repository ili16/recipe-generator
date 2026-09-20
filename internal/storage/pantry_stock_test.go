package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestPantryStock is BACKLOG 16.6's acceptance for the half that lives in SQL: a staple
// marked running low goes back on the grocery list, answering either way stamps
// confirmed_at (which is what clears the stock check), and an amount arriving settles the
// question on its own.
func TestPantryStock(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-pantry-stock') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	salt, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "Salt", Category: "spices_dry", Staple: true})
	if err != nil {
		t.Fatalf("UpsertPantryItem: %v", err)
	}
	if salt.RunningLow {
		t.Error("a freshly added staple is not running low")
	}
	if time.Since(salt.ConfirmedAt) > time.Minute {
		t.Errorf("adding an item confirms it, got confirmed_at %v", salt.ConfirmedAt)
	}

	staples, err := StapleNames(ctx, userID)
	if err != nil {
		t.Fatalf("StapleNames: %v", err)
	}
	if !staples["salt"] {
		t.Fatal("salt is a staple and must be dropped from the grocery list")
	}

	// Age it past the stock check, then answer "running low". Both facts have to change:
	// it leaves the staple set (so it is shopped for) and it is confirmed (so the card
	// stops asking).
	if _, err := Pool.Exec(ctx,
		`UPDATE pantry_items SET confirmed_at = NOW() - INTERVAL '40 days' WHERE id = $1`, salt.ID); err != nil {
		t.Fatalf("age salt: %v", err)
	}
	low, err := SetPantryStock(ctx, userID, salt.ID, true)
	if err != nil {
		t.Fatalf("SetPantryStock: %v", err)
	}
	if !low.RunningLow {
		t.Error("the row must come back marked low")
	}
	if time.Since(low.ConfirmedAt) > time.Minute {
		t.Errorf("saying running low is confirming the line, got confirmed_at %v", low.ConfirmedAt)
	}
	if staples, _ := StapleNames(ctx, userID); staples["salt"] {
		t.Error("a staple running low must be shopped for")
	}

	// Tapping again takes it off the list.
	if _, err := SetPantryStock(ctx, userID, salt.ID, false); err != nil {
		t.Fatalf("SetPantryStock back: %v", err)
	}
	if staples, _ := StapleNames(ctx, userID); !staples["salt"] {
		t.Error("having it again drops it from the grocery list")
	}

	// Another kitchen's row is not-found, not that row.
	var otherID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-pantry-stock-other') RETURNING id`).Scan(&otherID); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherID) })
	if _, err := SetPantryStock(ctx, otherID, salt.ID, true); err == nil {
		t.Error("another user's pantry line must not be touchable")
	}

	// Re-adding with a real amount answers the question by itself; a bare "I have it"
	// does not withdraw a low mark the user set. Both round-trips keep the same unit,
	// because (name, unit) is the row's identity.
	flour, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "Flour", Category: "spices_dry"})
	if err != nil {
		t.Fatalf("UpsertPantryItem flour: %v", err)
	}
	if _, err := SetPantryStock(ctx, userID, flour.ID, true); err != nil {
		t.Fatalf("SetPantryStock flour: %v", err)
	}
	again, err := UpsertPantryItem(ctx, userID, model.PantryItem{Name: "Flour", Category: "spices_dry"})
	if err != nil {
		t.Fatalf("re-add flour: %v", err)
	}
	if again.ID != flour.ID {
		t.Fatalf("re-adding must hit the same row, got %d then %d", flour.ID, again.ID)
	}
	if !again.RunningLow {
		t.Error("re-stating a food with no amount must not clear a low mark")
	}

	rice, err := UpsertPantryItem(ctx, userID, model.PantryItem{
		Name: "Rice", Quantity: ptrF(100), Unit: ptrS("g"), Category: "spices_dry",
	})
	if err != nil {
		t.Fatalf("UpsertPantryItem rice: %v", err)
	}
	if _, err := SetPantryStock(ctx, userID, rice.ID, true); err != nil {
		t.Fatalf("SetPantryStock rice: %v", err)
	}
	stocked, err := UpsertPantryItem(ctx, userID, model.PantryItem{
		Name: "Rice", Quantity: ptrF(1), Unit: ptrS("kg"), Category: "spices_dry",
	})
	if err != nil {
		t.Fatalf("re-add rice with amount: %v", err)
	}
	if stocked.ID != rice.ID {
		t.Fatalf("kg folds to g, so it is the same row: got %d then %d", rice.ID, stocked.ID)
	}
	if stocked.RunningLow {
		t.Error("a kilo of rice arriving is the answer to 'running low'")
	}
}

// TestPantryStockExclusions is BACKLOG 16.5's acceptance for the half that lives in SQL.
// Each row here is a way the grocery list would lie if it subtracted: no amount, a
// staple, expired, or a fact nobody has confirmed in a month.
func TestPantryStockExclusions(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-pantry-stock-excl') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	gone := time.Now().AddDate(0, 0, -1).Format(dateLayout)
	soon := time.Now().AddDate(0, 0, 7).Format(dateLayout)
	add := func(name string, qty *float64, staple bool, expires *string) model.PantryItem {
		item, err := UpsertPantryItem(ctx, userID, model.PantryItem{
			Name: name, Quantity: qty, Unit: ptrS("g"), Category: "fridge", Staple: staple, ExpiresOn: expires,
		})
		if err != nil {
			t.Fatalf("UpsertPantryItem %s: %v", name, err)
		}
		return item
	}

	add("Butter", ptrF(200), false, &soon) // the only row that should count
	add("Flour", nil, false, nil)          // "I have flour" is not an amount
	add("Sugar", ptrF(500), true, nil)     // a staple leaves the list whole, never partly
	add("Cream", ptrF(200), false, &gone)  // expired: you still have to buy it
	stale := add("Yoghurt", ptrF(400), false, nil)
	if _, err := Pool.Exec(ctx, `UPDATE pantry_items SET confirmed_at = NOW() - make_interval(days => $2) WHERE id = $1`,
		stale.ID, PantryStockDays+1); err != nil {
		t.Fatalf("age yoghurt: %v", err)
	}

	stock, err := PantryStock(ctx, userID)
	if err != nil {
		t.Fatalf("PantryStock: %v", err)
	}
	if len(stock) != 1 {
		t.Fatalf("only the fresh, measured, non-staple, unexpired row counts, got %+v", stock)
	}
	if stock[0].Key != "butter" || stock[0].Quantity != 200 {
		t.Errorf("want 200 g of butter keyed canonically, got %+v", stock[0])
	}
}
