package service

import (
	"encoding/json"
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

// TestMealPlanChatSystemPromptNotGatedByVariety guards the fix for the "shrimp to beef"
// bug: chat must always be allowed to propose a targeted variant, regardless of how many
// saved recipes the user has — unlike the fresh-week suggester.
func TestMealPlanChatSystemPromptNotGatedByVariety(t *testing.T) {
	if strings.Contains(mealPlanChatSystemPrompt, "fewer than 10") {
		t.Error("chat prompt must not be gated by the low-variety addendum")
	}
	if !strings.Contains(mealPlanChatSystemPrompt, "regardless of how many saved recipes") {
		t.Error("chat prompt should explicitly allow a targeted variant regardless of variety")
	}
}

func TestBuildMealPlanContextText(t *testing.T) {
	candidates := []model.Recipe{{ID: 7, Recipename: "Chicken Curry", Tags: []string{"dinner", "spicy"}}}
	profile := model.CookingProfile{TopTags: []string{"dinner"}}
	difficulty := "easy"
	profile.PreferredDifficulty = &difficulty
	prefs := model.UserPreferences{DietaryPrefs: []string{"vegetarian"}, DislikedIngredients: []string{"cilantro"}}
	startsOn := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	endsOn := startsOn.AddDate(0, 0, 6)

	text := buildMealPlanContextText(candidates, profile, prefs, startsOn, endsOn, nil)

	for _, want := range []string{"2026-09-07", "2026-09-13", "7: Chicken Curry", "dinner", "easy", "vegetarian", "cilantro"} {
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
	wantAssignmentFields := []string{"recipe_id", "variant", "variant_of_recipe_id", "planned_on", "start_time"}
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

func TestBuildMealPlanChatMessages(t *testing.T) {
	candidates := []model.Recipe{{ID: 1, Recipename: "Soup"}}
	startsOn := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	endsOn := startsOn.AddDate(0, 0, 6)
	recipeID := 1
	initial := model.MealPlanSuggestion{
		Status: "applied",
		Assignments: []model.MealPlanAssignment{
			{RecipeID: &recipeID, PlannedOn: "2026-09-07", StartTime: "18:00:00"},
		},
	}
	history := []model.MealPlanChatTurn{
		{Message: "swap Monday", Plan: initial},
	}

	messages, err := buildMealPlanChatMessages(candidates, model.CookingProfile{}, model.UserPreferences{}, startsOn, endsOn, nil, nil, initial, history, "make it vegetarian")
	if err != nil {
		t.Fatalf("buildMealPlanChatMessages: %v", err)
	}
	// system + context + initial(assistant) + history(user+assistant) + final user = 6
	if len(messages) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(messages))
	}
}

// TestBuildMealPlanChatMessagesGroundsAssignedRecipes covers the fix for the "shrimp to
// beef" bug's grounding half: the current day's ingredients must reach the model, not
// just its title, so a targeted swap has something real to edit.
func TestBuildMealPlanChatMessagesGroundsAssignedRecipes(t *testing.T) {
	startsOn := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	endsOn := startsOn.AddDate(0, 0, 6)
	recipeID := 1
	initial := model.MealPlanSuggestion{
		Status:      "applied",
		Assignments: []model.MealPlanAssignment{{RecipeID: &recipeID, PlannedOn: "2026-09-07", StartTime: "18:00:00"}},
	}
	assignedRecipes := map[int]model.RecipeDocument{
		1: {Title: "Shrimp Tacos", Ingredients: []model.RecipeIngredient{{Item: "shrimp"}, {Item: "tortillas"}}},
	}

	messages, err := buildMealPlanChatMessages(nil, model.CookingProfile{}, model.UserPreferences{}, startsOn, endsOn, nil, assignedRecipes, initial, nil, "swap the shrimp for beef")
	if err != nil {
		t.Fatalf("buildMealPlanChatMessages: %v", err)
	}
	found := false
	for _, m := range messages {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		if strings.Contains(string(raw), "shrimp") && strings.Contains(string(raw), "Shrimp Tacos") {
			found = true
		}
	}
	if !found {
		t.Error("expected the assigned recipe's ingredients to appear in a context message")
	}
}
