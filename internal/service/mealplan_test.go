package service

import (
	"strings"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

func TestMealPlanSuggestSystemPrompt(t *testing.T) {
	if strings.Contains(mealPlanSuggestSystemPrompt(false), "fewer than 10") {
		t.Error("normal-variety prompt should not include the low-variety addendum")
	}
	if !strings.Contains(mealPlanSuggestSystemPrompt(true), "fewer than 10") {
		t.Error("low-variety prompt should include the low-variety addendum")
	}
}

func TestBuildMealPlanContextText(t *testing.T) {
	four := 4
	candidates := []model.Recipe{{ID: 7, Recipename: "Chicken Curry", Tags: []string{"dinner", "spicy"}, Servings: &four}}
	profile := model.CookingProfile{TopTags: []string{"dinner"}}
	difficulty := "easy"
	profile.PreferredDifficulty = &difficulty
	prefs := model.UserPreferences{DietaryPrefs: []string{"vegetarian"}, DislikedIngredients: []string{"cilantro"}}
	startsOn := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	endsOn := startsOn.AddDate(0, 0, 6)

	text := buildMealPlanContextText(candidates, profile, prefs, startsOn, endsOn, nil)

	for _, want := range []string{"2026-09-07", "2026-09-13", "7: Chicken Curry", "(serves 4)", "dinner", "easy", "vegetarian", "cilantro"} {
		if !strings.Contains(text, want) {
			t.Errorf("context text missing %q:\n%s", want, text)
		}
	}

	empty := buildMealPlanContextText(nil, model.CookingProfile{}, model.UserPreferences{}, startsOn, endsOn, nil)
	if !strings.Contains(empty, "Saved recipes: none.") {
		t.Errorf("expected 'none' placeholder for empty candidates, got:\n%s", empty)
	}
}

func TestBuildMealPlanContextTextSchedulingPrefs(t *testing.T) {
	startsOn := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	endsOn := startsOn.AddDate(0, 0, 6)

	// Default (batch_days=1, no no-food/no-cook days): none of the scheduling lines
	// should appear.
	defaultPrefs := model.UserPreferences{MealPlanBatchDays: 1}
	defaultText := buildMealPlanContextText(nil, model.CookingProfile{}, defaultPrefs, startsOn, endsOn, nil)
	for _, absent := range []string{"needs no food", "doesn't want to cook", "cooks in batches"} {
		if strings.Contains(defaultText, absent) {
			t.Errorf("default prefs should not mention %q:\n%s", absent, defaultText)
		}
	}

	scheduled := model.UserPreferences{
		MealPlanNoFoodDays: []string{"sunday"},
		MealPlanNoCookDays: []string{"wednesday"},
		MealPlanBatchDays:  2,
	}
	text := buildMealPlanContextText(nil, model.CookingProfile{}, scheduled, startsOn, endsOn, nil)
	for _, want := range []string{"sunday", "needs no food", "wednesday", "doesn't want to cook", "cooks in batches", "2 consecutive"} {
		if !strings.Contains(text, want) {
			t.Errorf("scheduling prefs text missing %q:\n%s", want, text)
		}
	}
}

// TestBuildMealPlanContextTextPriorMeal covers the week-boundary carryover context line
// ("cook Sunday for Monday").
func TestBuildMealPlanContextTextPriorMeal(t *testing.T) {
	startsOn := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	endsOn := startsOn.AddDate(0, 0, 6)
	prior := &model.MealPlanItem{RecipeID: 3, RecipeTitle: "Sunday Roast", PlannedOn: "2026-09-06"}

	text := buildMealPlanContextText(nil, model.CookingProfile{}, model.UserPreferences{}, startsOn, endsOn, prior)
	for _, want := range []string{"Most recently cooked meal before this range", "2026-09-06", "Sunday Roast", "id 3"} {
		if !strings.Contains(text, want) {
			t.Errorf("context text missing %q:\n%s", want, text)
		}
	}
}

// TestMealPlanSchemasCoverStructFields guards against the JSON schema drifting out of
// sync with the Go structs it validates: OpenAI's strict mode requires every property
// in "properties" to also be listed in "required" (nullable fields still required, just
// typed as anyOf-null), so a struct field added without a matching schema entry would
// silently break the live call rather than fail at build time.
func TestMealPlanSchemasCoverStructFields(t *testing.T) {
	assignmentSchema := mealPlanAssignmentSchema()
	wantAssignmentFields := []string{"recipe_id", "variant", "variant_of_recipe_id", "planned_on", "start_time", "servings"}
	assertSchemaFields(t, "mealPlanAssignmentSchema", assignmentSchema, wantAssignmentFields)

	suggestionSchema := mealPlanSuggestionJSONSchema()
	wantSuggestionFields := []string{"status", "message", "low_variety", "assignments"}
	assertSchemaFields(t, "mealPlanSuggestionJSONSchema", suggestionSchema, wantSuggestionFields)
}

func assertSchemaFields(t *testing.T, name string, schema map[string]any, want []string) {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s: missing properties", name)
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("%s: missing required", name)
	}
	requiredSet := map[string]bool{}
	for _, r := range required {
		requiredSet[r] = true
	}
	for _, f := range want {
		if _, ok := props[f]; !ok {
			t.Errorf("%s: properties missing field %q", name, f)
		}
		if !requiredSet[f] {
			t.Errorf("%s: required missing field %q (strict mode needs every property listed)", name, f)
		}
	}
	if len(props) != len(want) {
		t.Errorf("%s: properties has %d fields, want %d (schema/struct drifted)", name, len(props), len(want))
	}
}

// TestEnforceNoCookCarryover covers the code-level fix for the "leftover day" mechanic:
// the system prompt alone can't guarantee a no-cook day repeats the right dish, so this
// makes the day's content correct by construction regardless of what the model returned.
func TestEnforceNoCookCarryover(t *testing.T) {
	startsOn := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC) // a Monday
	endsOn := startsOn.AddDate(0, 0, 6)
	day := func(offset int) string { return startsOn.AddDate(0, 0, offset).Format("2006-01-02") }
	weekday := func(offset int) string { return strings.ToLower(startsOn.AddDate(0, 0, offset).Weekday().String()) }
	id := func(n int) *int { return &n }

	findAssignment := func(t *testing.T, assignments []model.MealPlanAssignment, plannedOn string) model.MealPlanAssignment {
		t.Helper()
		for _, a := range assignments {
			if a.PlannedOn == plannedOn {
				return a
			}
		}
		t.Fatalf("no assignment found for %s", plannedOn)
		return model.MealPlanAssignment{}
	}

	t.Run("mid-range no-cook day is overwritten with the prior cooked day's dish", func(t *testing.T) {
		result := model.MealPlanSuggestion{Status: "applied", Assignments: []model.MealPlanAssignment{
			{RecipeID: id(1), PlannedOn: day(0), StartTime: "18:00:00"},  // Mon, cooked
			{RecipeID: id(2), PlannedOn: day(1), StartTime: "18:00:00"},  // Tue, cooked
			{RecipeID: id(99), PlannedOn: day(2), StartTime: "18:00:00"}, // Wed, no-cook — model got it wrong
			{RecipeID: id(3), PlannedOn: day(3), StartTime: "18:00:00"},  // Thu, cooked
		}}
		enforceNoCookCarryover(&result, model.UserPreferences{MealPlanNoCookDays: []string{weekday(2)}}, startsOn, endsOn, nil)

		if wed := findAssignment(t, result.Assignments, day(2)); wed.RecipeID == nil || *wed.RecipeID != 2 {
			t.Errorf("expected Wednesday to carry Tuesday's recipe_id 2, got %+v", wed)
		}
	})

	t.Run("an override is explained on the message, not applied silently", func(t *testing.T) {
		// BACKLOG.md 2.5: "make Thursday salmon" on a no-cook Thursday used to be
		// reverted here while the reply still said "Done!". The revert is still the
		// right outcome — the rule is true by construction — but it has to be visible,
		// so whoever renders the reply tells the user what actually happened.
		msg := "Done!"
		result := model.MealPlanSuggestion{Status: "applied", Message: &msg, Assignments: []model.MealPlanAssignment{
			{RecipeID: id(1), PlannedOn: day(0), StartTime: "18:00:00"},  // Mon, cooked
			{RecipeID: id(99), PlannedOn: day(3), StartTime: "18:00:00"}, // Thu, no-cook — user asked for this
		}}
		enforceNoCookCarryover(&result, model.UserPreferences{MealPlanNoCookDays: []string{weekday(3)}}, startsOn, endsOn, nil)

		if thu := findAssignment(t, result.Assignments, day(3)); thu.RecipeID == nil || *thu.RecipeID != 1 {
			t.Errorf("expected Thursday to carry Monday's recipe_id 1, got %+v", thu)
		}
		if result.Message == nil || !strings.Contains(*result.Message, day(3)) {
			t.Errorf("the override was applied silently; message = %v", result.Message)
		}
		if !strings.HasPrefix(*result.Message, "Done!") {
			t.Errorf("the model's own message was discarded: %q", *result.Message)
		}
	})

	t.Run("a day the model already got right is not reported as an override", func(t *testing.T) {
		result := model.MealPlanSuggestion{Status: "applied", Assignments: []model.MealPlanAssignment{
			{RecipeID: id(1), PlannedOn: day(0), StartTime: "18:00:00"}, // Mon, cooked
			{RecipeID: id(1), PlannedOn: day(3), StartTime: "18:00:00"}, // Thu, no-cook, already carried
		}}
		enforceNoCookCarryover(&result, model.UserPreferences{MealPlanNoCookDays: []string{weekday(3)}}, startsOn, endsOn, nil)

		if result.Message != nil {
			t.Errorf("nothing changed, but a note was added: %q", *result.Message)
		}
	})

	t.Run("chained no-cook days all carry the same original cooked dish", func(t *testing.T) {
		result := model.MealPlanSuggestion{Status: "applied", Assignments: []model.MealPlanAssignment{
			{RecipeID: id(1), PlannedOn: day(0), StartTime: "18:00:00"},   // Mon, cooked
			{RecipeID: id(2), PlannedOn: day(1), StartTime: "18:00:00"},   // Tue, cooked
			{RecipeID: id(99), PlannedOn: day(2), StartTime: "18:00:00"},  // Wed, no-cook
			{RecipeID: id(100), PlannedOn: day(3), StartTime: "18:00:00"}, // Thu, no-cook
		}}
		enforceNoCookCarryover(&result, model.UserPreferences{MealPlanNoCookDays: []string{weekday(2), weekday(3)}}, startsOn, endsOn, nil)

		for _, offset := range []int{2, 3} {
			if a := findAssignment(t, result.Assignments, day(offset)); a.RecipeID == nil || *a.RecipeID != 2 {
				t.Errorf("day offset %d: expected carried recipe_id 2, got %+v", offset, a)
			}
		}
	})

	t.Run("no-cook day at the start of the range carries forward from priorMeal", func(t *testing.T) {
		result := model.MealPlanSuggestion{Status: "applied", Assignments: []model.MealPlanAssignment{
			{RecipeID: id(99), PlannedOn: day(0), StartTime: "18:00:00"}, // Mon, no-cook, model got it wrong
		}}
		priorMeal := &model.MealPlanItem{RecipeID: 42, PlannedOn: "2026-09-06", StartTime: "18:00:00"}
		enforceNoCookCarryover(&result, model.UserPreferences{MealPlanNoCookDays: []string{weekday(0)}}, startsOn, endsOn, priorMeal)

		if mon := findAssignment(t, result.Assignments, day(0)); mon.RecipeID == nil || *mon.RecipeID != 42 {
			t.Errorf("expected Monday to carry priorMeal's recipe_id 42, got %+v", mon)
		}
	})

	t.Run("missing assignment on a no-cook day is inserted, not just fixed", func(t *testing.T) {
		result := model.MealPlanSuggestion{Status: "applied", Assignments: []model.MealPlanAssignment{
			{RecipeID: id(1), PlannedOn: day(0), StartTime: "18:00:00"}, // Mon, cooked
			// Tue (no-cook) has no assignment at all — model treated it like a no-food day.
			{RecipeID: id(3), PlannedOn: day(2), StartTime: "18:00:00"}, // Wed, cooked
		}}
		enforceNoCookCarryover(&result, model.UserPreferences{MealPlanNoCookDays: []string{weekday(1)}}, startsOn, endsOn, nil)

		if len(result.Assignments) != 3 {
			t.Fatalf("expected the missing no-cook day to be inserted, got %d assignments", len(result.Assignments))
		}
		if tue := findAssignment(t, result.Assignments, day(1)); tue.RecipeID == nil || *tue.RecipeID != 1 {
			t.Errorf("expected inserted Tuesday to carry Monday's recipe_id 1, got %+v", tue)
		}
	})

	t.Run("no reference to carry forward leaves the model's assignment untouched", func(t *testing.T) {
		result := model.MealPlanSuggestion{Status: "applied", Assignments: []model.MealPlanAssignment{
			{RecipeID: id(7), PlannedOn: day(0), StartTime: "18:00:00"}, // Mon, no-cook, nothing to reference
		}}
		enforceNoCookCarryover(&result, model.UserPreferences{MealPlanNoCookDays: []string{weekday(0)}}, startsOn, endsOn, nil)

		if mon := findAssignment(t, result.Assignments, day(0)); mon.RecipeID == nil || *mon.RecipeID != 7 {
			t.Errorf("expected assignment to remain untouched, got %+v", mon)
		}
	})

	t.Run("no-op when there are no no-cook days", func(t *testing.T) {
		result := model.MealPlanSuggestion{Status: "applied", Assignments: []model.MealPlanAssignment{
			{RecipeID: id(1), PlannedOn: day(0), StartTime: "18:00:00"},
		}}
		before := result.Assignments[0]
		enforceNoCookCarryover(&result, model.UserPreferences{}, startsOn, endsOn, nil)
		if result.Assignments[0] != before {
			t.Errorf("expected no changes with no no-cook days, got %+v", result.Assignments[0])
		}
	})
}

// TestSuppressRecentlyCooked covers the cooldown (BACKLOG 6.3): a recipe cooked inside
// the window is held back, one cooked before it (or never) is not, and a library where
// everything is on cooldown is returned whole rather than empty.
func TestSuppressRecentlyCooked(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { ts := now.Add(-d); return &ts }

	ids := func(rs []model.Recipe) []int {
		out := []int{}
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}

	got := ids(suppressRecentlyCooked([]model.Recipe{
		{ID: 1}, // never cooked
		{ID: 2, LastCookedAt: at(3 * 24 * time.Hour)},  // inside the window
		{ID: 3, LastCookedAt: at(30 * 24 * time.Hour)}, // long past it
		{ID: 4, LastCookedAt: at(CookedCooldown)},      // exactly at the edge: allowed
	}, now))
	if len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("expected [1 3 4], got %v", got)
	}

	all := []model.Recipe{{ID: 1, LastCookedAt: at(time.Hour)}, {ID: 2, LastCookedAt: at(time.Hour)}}
	if got := ids(suppressRecentlyCooked(all, now)); len(got) != 2 {
		t.Fatalf("everything on cooldown should fall back to the full list, got %v", got)
	}
}
