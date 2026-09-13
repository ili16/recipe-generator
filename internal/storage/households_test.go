package storage

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// seedHouseholdUser makes a throwaway account that cleans itself up.
func seedHouseholdUser(t *testing.T, ctx context.Context, oauth string) int {
	t.Helper()
	var id int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ($1) RETURNING id`, oauth).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", oauth, err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })
	return id
}

func seedHouseholdRecipe(t *testing.T, ctx context.Context, userID int, title string) model.Recipe {
	t.Helper()
	doc := model.RecipeDocument{
		Title:       title,
		Ingredients: []model.RecipeIngredient{{Item: "flour", Quantity: ptrF(200), Unit: ptrS("g")}},
		Steps:       []model.RecipeStep{{StepText: "Mix."}},
	}
	r, err := AddRecipeStructured(ctx, userID, doc, "# "+title, nil, nil, "test-model", "v1", nil)
	if err != nil {
		t.Fatalf("seed recipe %s: %v", title, err)
	}
	return r
}

// TestHouseholdSharingAndExit is BACKLOG 15.1's acceptance against a real DB: two
// accounts share one kitchen, and the exit contract puts everything back.
//
// The three properties that matter, and the reason this test exists at all:
// sharing is a read widening (nobody's row ever changes owner), leaving takes only your
// own things, and disbanding removes the week that was still to come while leaving the
// week that was already eaten.
func TestHouseholdSharingAndExit(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	alice := seedHouseholdUser(t, ctx, "test-hh-alice")
	bob := seedHouseholdUser(t, ctx, "test-hh-bob")
	stranger := seedHouseholdUser(t, ctx, "test-hh-stranger")

	aliceRecipe := seedHouseholdRecipe(t, ctx, alice, "Alice Roast")
	bobRecipe := seedHouseholdRecipe(t, ctx, bob, "Bob Stew")
	strangerRecipe := seedHouseholdRecipe(t, ctx, stranger, "Stranger Soup")

	hh, err := CreateHousehold(ctx, alice, "The Flat")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM households WHERE id = $1::uuid`, hh.ID) })
	if hh.Name != "The Flat" || len(hh.Members) != 1 || !hh.Members[0].IsMe {
		t.Fatalf("fresh household should hold its creator alone: %+v", hh)
	}
	if len(hh.JoinCode) != 8 {
		t.Fatalf("join code should be 8 characters, got %q", hh.JoinCode)
	}

	// Creating a second one is refused rather than silently moving Alice.
	if _, err := CreateHousehold(ctx, alice, "Another"); !errors.Is(err, ErrAlreadyInHousehold) {
		t.Fatalf("second create should be ErrAlreadyInHousehold, got %v", err)
	}

	if _, err := JoinHousehold(ctx, bob, hh.JoinCode); err != nil {
		t.Fatalf("JoinHousehold: %v", err)
	}

	// --- Sharing: the library is the kitchen's, and says who owns each recipe.
	seen, err := GetRecipes(bob)
	if err != nil {
		t.Fatalf("GetRecipes: %v", err)
	}
	byID := map[int]model.Recipe{}
	for _, r := range seen {
		byID[r.ID] = r
	}
	if _, ok := byID[aliceRecipe.ID]; !ok {
		t.Error("Bob cannot see Alice's recipe")
	}
	if _, ok := byID[strangerRecipe.ID]; ok {
		t.Error("Bob can see a recipe belonging to nobody in his household")
	}
	if got := byID[aliceRecipe.ID]; got.OwnedByMe {
		t.Error("Alice's recipe is marked as owned by Bob")
	}
	if got := byID[bobRecipe.ID]; !got.OwnedByMe {
		t.Error("Bob's own recipe is not marked as his")
	}
	if _, err := GetRecipeByID(ctx, bob, aliceRecipe.ID); err != nil {
		t.Errorf("Bob should be able to open Alice's recipe: %v", err)
	}

	// Ownership did not move: a shared recipe is still only writable by its owner.
	// DeleteRecipe reports nothing either way (a miss is a silent no-op, as it has always
	// been), so the property to check is that the recipe is still there afterwards.
	if err := DeleteRecipe(ctx, bob, aliceRecipe.ID); err != nil {
		t.Fatalf("DeleteRecipe: %v", err)
	}
	if _, err := GetRecipeByID(ctx, alice, aliceRecipe.ID); err != nil {
		t.Errorf("Bob deleted Alice's recipe — sharing is read-only for non-owners: %v", err)
	}
	if _, err := UpdateRecipeStructured(ctx, bob, aliceRecipe.ID, model.RecipeDocument{
		Title: "Hijacked", Steps: []model.RecipeStep{{StepText: "No."}},
	}, "# Hijacked", true, ""); err == nil {
		t.Error("Bob rewrote Alice's recipe — only the owner may")
	}

	// --- Sharing: one fridge, one flour line, whoever put it there.
	if _, err := UpsertPantryItem(ctx, alice, model.PantryItem{Name: "Flour", Quantity: ptrF(1), Unit: ptrS("kg")}); err != nil {
		t.Fatalf("Alice pantry: %v", err)
	}
	if _, err := UpsertPantryItem(ctx, bob, model.PantryItem{Name: "flour", Quantity: ptrF(2000), Unit: ptrS("g")}); err != nil {
		t.Fatalf("Bob pantry: %v", err)
	}
	items, err := ListPantryItems(ctx, bob)
	if err != nil {
		t.Fatalf("ListPantryItems: %v", err)
	}
	if len(items) != 1 || *items[0].Quantity != 2000 {
		t.Fatalf("one household should hold one flour line at Bob's amount, got %+v", items)
	}

	// --- Sharing: one week. Bob plans a day, Alice sees it.
	past := time.Now().AddDate(0, 0, -3)
	future := time.Now().AddDate(0, 0, 3)
	if _, err := AddMealPlanItem(ctx, bob, aliceRecipe.ID, past, "monday", "dinner", nil); err != nil {
		t.Fatalf("plan a past day with Alice's recipe: %v", err)
	}
	if _, err := AddMealPlanItem(ctx, bob, bobRecipe.ID, future, "monday", "dinner", nil); err != nil {
		t.Fatalf("plan a future day: %v", err)
	}
	planned, err := GetMealPlanRange(ctx, alice, past.AddDate(0, 0, -1), future.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("GetMealPlanRange: %v", err)
	}
	if len(planned) != 2 {
		t.Fatalf("Alice should see both of Bob's planned days, got %d", len(planned))
	}

	// --- Leaving takes your own things and leaves the week behind.
	if err := LeaveHousehold(ctx, bob); err != nil {
		t.Fatalf("LeaveHousehold: %v", err)
	}
	if _, err := GetRecipeByID(ctx, bob, aliceRecipe.ID); err == nil {
		t.Error("Bob still sees Alice's recipe after leaving")
	}
	if _, err := GetRecipeByID(ctx, bob, bobRecipe.ID); err != nil {
		t.Errorf("Bob lost his own recipe by leaving: %v", err)
	}
	stillPlanned, err := GetMealPlanRange(ctx, alice, past.AddDate(0, 0, -1), future.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("GetMealPlanRange after leave: %v", err)
	}
	if len(stillPlanned) != 2 {
		t.Fatalf("the week should survive one member leaving, got %d days", len(stillPlanned))
	}

	// --- Disbanding: future days go, the eaten past stays, recipes go home.
	if err := DisbandHousehold(ctx, alice); err != nil {
		t.Fatalf("DisbandHousehold: %v", err)
	}
	left, err := GetMealPlanRange(ctx, alice, past.AddDate(0, 0, -1), future.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("GetMealPlanRange after disband: %v", err)
	}
	if len(left) != 1 {
		t.Fatalf("only the already-eaten day should remain, got %d: %+v", len(left), left)
	}
	if left[0].PlannedOn != past.Format("2006-01-02") {
		t.Errorf("the surviving day should be the past one, got %s", left[0].PlannedOn)
	}
	if _, err := GetRecipeByID(ctx, alice, aliceRecipe.ID); err != nil {
		t.Errorf("Alice lost her own recipe to the disband: %v", err)
	}
	if _, err := GetHousehold(ctx, alice); !errors.Is(err, ErrNotInHousehold) {
		t.Errorf("Alice is still in a household after disbanding: %v", err)
	}

	// The code is dead with the household it opened.
	if _, err := JoinHousehold(ctx, stranger, hh.JoinCode); err == nil {
		t.Error("a disbanded household's join code still works")
	}
}

// TestRotateJoinCodeRevokes is the other half of the credential's reason to exist: a
// member who leaves must not keep a working key.
func TestRotateJoinCodeRevokes(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	owner := seedHouseholdUser(t, ctx, "test-hh-rot-owner")
	outsider := seedHouseholdUser(t, ctx, "test-hh-rot-outsider")

	hh, err := CreateHousehold(ctx, owner, "Rotate Me")
	if err != nil {
		t.Fatalf("CreateHousehold: %v", err)
	}
	t.Cleanup(func() {
		DisbandHousehold(ctx, owner)
		Pool.Exec(ctx, `DELETE FROM households WHERE id = $1::uuid`, hh.ID)
	})

	fresh, err := RotateJoinCode(ctx, owner)
	if err != nil {
		t.Fatalf("RotateJoinCode: %v", err)
	}
	if fresh == hh.JoinCode {
		t.Fatal("rotating returned the same code")
	}
	if _, err := JoinHousehold(ctx, outsider, hh.JoinCode); err == nil {
		t.Error("the old code still opens the household")
	}
	if _, err := JoinHousehold(ctx, outsider, fresh); err != nil {
		t.Errorf("the fresh code does not work: %v", err)
	}
}

// TestHouseholdScopingIsInertWhenSolo is the property the whole design rests on: a user
// with no household must see exactly what they saw before any of this existed.
func TestHouseholdScopingIsInertWhenSolo(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	solo := seedHouseholdUser(t, ctx, "test-hh-solo")
	other := seedHouseholdUser(t, ctx, "test-hh-solo-other")
	mine := seedHouseholdRecipe(t, ctx, solo, "Mine")
	theirs := seedHouseholdRecipe(t, ctx, other, "Theirs")

	seen, err := GetRecipes(solo)
	if err != nil {
		t.Fatalf("GetRecipes: %v", err)
	}
	if len(seen) != 1 || seen[0].ID != mine.ID || !seen[0].OwnedByMe {
		t.Fatalf("a user with no household sees only their own recipes, got %+v", seen)
	}
	if _, err := GetRecipeByID(ctx, solo, theirs.ID); err == nil {
		t.Error("a user with no household can read an unrelated user's recipe")
	}
	if err := LeaveHousehold(ctx, solo); !errors.Is(err, ErrNotInHousehold) {
		t.Errorf("leaving nothing should be ErrNotInHousehold, got %v", err)
	}
}
