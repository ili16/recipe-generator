package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/openai/openai-go"
)

// mealPlanAssignmentSchema describes one proposed day: exactly one of recipe_id
// (existing saved recipe) or variant (a brand-new proposed recipe) is non-null.
func mealPlanAssignmentSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"recipe_id":            llm.Nullable("integer"),
			"variant":              map[string]any{"anyOf": []any{recipeJSONSchema(), map[string]any{"type": "null"}}},
			"variant_of_recipe_id": llm.Nullable("integer"),
			"planned_on":           map[string]any{"type": "string"},
			"servings":             llm.Nullable("integer"),
		},
		"required":             []string{"recipe_id", "variant", "variant_of_recipe_id", "planned_on", "servings"},
		"additionalProperties": false,
	}
}

func mealPlanSuggestionJSONSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type": "string",
				"enum": []string{"applied", "needs_clarification"},
			},
			"message": map[string]any{
				"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}},
			},
			"low_variety": map[string]any{"type": "boolean"},
			"assignments": map[string]any{"type": "array", "items": mealPlanAssignmentSchema()},
		},
		"required":             []string{"status", "message", "low_variety", "assignments"},
		"additionalProperties": false,
	}
}

func mealPlanSuggestSystemPrompt(lowVariety bool) string {
	if lowVariety {
		return llm.MealPlanSuggestBase + llm.MealPlanLowVarietyAddendum
	}
	return llm.MealPlanSuggestBase
}

// buildMealPlanContextText renders the candidate recipes plus soft personalization
// signal as the user-role message grounding a suggest/chat call. priorMeal, when
// non-nil, is the most recently cooked meal strictly before startsOn — lets a no-cook
// day at the very start of the range carry forward a dish from the prior week instead of
// having nothing to reference.
func buildMealPlanContextText(candidates []model.Recipe, profile model.CookingProfile, prefs model.UserPreferences, startsOn, endsOn time.Time, priorMeal *model.MealPlanItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Date range to plan: %s to %s (inclusive).\n\n", startsOn.Format("2006-01-02"), endsOn.Format("2006-01-02"))
	if priorMeal != nil {
		fmt.Fprintf(&b, "Most recently cooked meal before this range: %s — %s (id %d).\n\n", priorMeal.PlannedOn, priorMeal.RecipeTitle, priorMeal.RecipeID)
	}
	if len(candidates) == 0 {
		b.WriteString("Saved recipes: none.\n")
	} else {
		b.WriteString("Saved recipes (id: title [tags], and what the recipe yields):\n")
		for _, r := range candidates {
			fmt.Fprintf(&b, "%d: %s %v", r.ID, r.Recipename, r.Tags)
			if r.Servings != nil {
				fmt.Fprintf(&b, " (serves %d)", *r.Servings)
			}
			// How the last cook actually went (BACKLOG 6.7) — a soft signal, not a filter:
			// prefer the 5s, don't refuse to ever plan a 2 again.
			if r.CookedRating != nil {
				fmt.Fprintf(&b, " (user rated their last cook %d/5)", *r.CookedRating)
			}
			b.WriteString("\n")
		}
	}
	if len(profile.TopTags) > 0 {
		fmt.Fprintf(&b, "\nUser's most-cooked tags: %v\n", profile.TopTags)
	}
	if profile.PreferredDifficulty != nil {
		fmt.Fprintf(&b, "Preferred difficulty: %s\n", *profile.PreferredDifficulty)
	}
	if len(prefs.DietaryPrefs) > 0 {
		fmt.Fprintf(&b, "Dietary preferences: %v\n", prefs.DietaryPrefs)
	}
	if len(prefs.DislikedIngredients) > 0 {
		fmt.Fprintf(&b, "Disliked ingredients (avoid): %v\n", prefs.DislikedIngredients)
	}
	if prefs.HouseholdSize != nil {
		fmt.Fprintf(&b, "The user cooks for %d people (BACKLOG 9.12) — a day's servings should cover that unless the user says otherwise, and a recipe yielding less needs scaling up.\n", *prefs.HouseholdSize)
	}
	if len(prefs.MealPlanNoFoodDays) > 0 {
		fmt.Fprintf(&b, "The user needs no food planned on: %v (skip these days entirely — no assignment)\n", prefs.MealPlanNoFoodDays)
	}
	if len(prefs.MealPlanNoCookDays) > 0 {
		fmt.Fprintf(&b, "The user doesn't want to cook on: %v, but still needs food that day (a leftover day — see the rule above)\n", prefs.MealPlanNoCookDays)
	}
	if prefs.MealPlanBatchDays > 1 {
		fmt.Fprintf(&b, "The user cooks in batches: repeat the same recipe_id across every run of %d consecutive days (skipping only \"needs no food\" days), instead of a different recipe each day.\n", prefs.MealPlanBatchDays)
	}
	return b.String()
}

// renderIngredientsSummary compacts a RecipeDocument's ingredients into one line, for
// grounding a targeted chat edit without spending tokens on the full structured doc.
func renderIngredientsSummary(doc model.RecipeDocument) string {
	items := make([]string, 0, len(doc.Ingredients))
	for _, ing := range doc.Ingredients {
		items = append(items, ing.Item)
	}
	return strings.Join(items, ", ")
}

// generateMealPlanSuggestion mirrors generateRefineFromMessages's call shape but
// decodes into model.MealPlanSuggestion against mealPlanSuggestionJSONSchema.
func generateMealPlanSuggestion(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (model.MealPlanSuggestion, error) {
	result, _, err := llm.Call[model.MealPlanSuggestion](ctx, llm.Spec{
		Model:    recipeModel,
		CallType: "meal_plan_suggest",
		Messages: messages,
		Schema:   &llm.Schema{Name: "meal_plan_suggestion", Schema: mealPlanSuggestionJSONSchema()},
	})
	if err != nil {
		return model.MealPlanSuggestion{}, err
	}
	if result.Status == "applied" && len(result.Assignments) == 0 {
		return model.MealPlanSuggestion{}, errors.New("applied status missing assignments")
	}
	return result, nil
}

// enforceNoCookCarryover overwrites the assignment on each no-cook day so it exactly
// repeats the most recently cooked day's dish. The system prompt asks the model to do
// this itself, but nothing about the JSON schema enforces it — this makes a no-cook
// day's content correct by construction instead of by LLM cooperation. No-op when
// there are no no-cook days or the suggestion wasn't applied. lastCooked starts from
// priorMeal (if any) and is only advanced by genuinely-cooked days, so a run of
// consecutive no-cook days all correctly carry the same original dish forward.
func enforceNoCookCarryover(result *model.MealPlanSuggestion, prefs model.UserPreferences, startsOn, endsOn time.Time, priorMeal *model.MealPlanItem) {
	if result.Status != "applied" || len(prefs.MealPlanNoCookDays) == 0 {
		return
	}
	// Normalise to UTC calendar dates so Weekday() below names the same day the client
	// derives from the YYYY-MM-DD string, whatever timezone this process runs in.
	startsOn, endsOn = utcDate(startsOn), utcDate(endsOn)
	var notes []string
	noCook := make(map[string]bool, len(prefs.MealPlanNoCookDays))
	for _, d := range prefs.MealPlanNoCookDays {
		noCook[d] = true
	}

	byDate := make(map[string]int, len(result.Assignments))
	for i, a := range result.Assignments {
		byDate[a.PlannedOn] = i
	}

	var lastCooked *model.MealPlanAssignment
	if priorMeal != nil {
		lastCooked = &model.MealPlanAssignment{RecipeID: &priorMeal.RecipeID, PlannedOn: priorMeal.PlannedOn}
	}

	appended := false
	for d := startsOn; !d.After(endsOn); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		idx, exists := byDate[dateStr]

		if noCook[strings.ToLower(d.Weekday().String())] {
			if lastCooked == nil {
				continue // nothing to carry forward yet — leave the model's guess alone
			}
			carried := *lastCooked
			carried.PlannedOn = dateStr
			// Say it in the data, not just by repeating the recipe id: the client used
			// to re-derive "this is a leftover" from the repeat, and got it wrong
			// whenever a dish was deliberately cooked twice (BACKLOG 9.11).
			source := lastCooked.PlannedOn
			carried.LeftoverOf = &source
			if exists {
				if !sameAssignment(result.Assignments[idx], carried) {
					// Say what was overridden. A silent revert lets the model reply
					// "Done!" about a change this just undid (BACKLOG.md 2.5).
					notes = append(notes, dateStr+" is a no-cook day, so it carries "+lastCooked.PlannedOn+"'s dish forward instead of the proposed one")
				}
				result.Assignments[idx] = carried
			} else {
				result.Assignments = append(result.Assignments, carried)
				appended = true
			}
			continue
		}
		if exists {
			cooked := result.Assignments[idx]
			lastCooked = &cooked
		}
	}

	if appended {
		sort.Slice(result.Assignments, func(i, j int) bool { return result.Assignments[i].PlannedOn < result.Assignments[j].PlannedOn })
	}
	// Surface the overrides on the suggestion's own message, so whoever renders the
	// reply — the meal-plan screen today, the agent loop tomorrow — tells the user what
	// this changed instead of reporting an unqualified success.
	if len(notes) > 0 {
		msg := strings.Join(notes, " ")
		if result.Message != nil && *result.Message != "" {
			msg = *result.Message + " " + msg
		}
		result.Message = &msg
	}
}

// utcDate strips a time to its calendar date in UTC.
func utcDate(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// sameAssignment reports whether two assignments put the same dish on the plate —
// enough to tell a real override from a no-op rewrite.
func sameAssignment(a, b model.MealPlanAssignment) bool {
	if a.RecipeID != nil && b.RecipeID != nil {
		return *a.RecipeID == *b.RecipeID
	}
	return a.RecipeID == nil && b.RecipeID == nil && a.Variant == nil && b.Variant == nil
}

// SuggestMealPlan proposes a fresh plan for [startsOn, endsOn] from candidates. priorMeal,
// when non-nil, is the most recently cooked meal strictly before startsOn (crosses the
// week boundary so a no-cook day at the start of the range has something to carry
// forward — e.g. "cook Sunday for Monday").
func SuggestMealPlan(ctx context.Context, candidates []model.Recipe, profile model.CookingProfile, prefs model.UserPreferences, startsOn, endsOn time.Time, priorMeal *model.MealPlanItem) (model.MealPlanSuggestion, error) {
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(mealPlanSuggestSystemPrompt(len(candidates) < 10)),
		openai.UserMessage(buildMealPlanContextText(candidates, profile, prefs, startsOn, endsOn, priorMeal)),
	}
	result, err := generateMealPlanSuggestion(ctx, messages)
	if err != nil {
		return result, err
	}
	enforceNoCookCarryover(&result, prefs, startsOn, endsOn, priorMeal)
	return result, nil
}

// POST /meal-plan/variants/{id} so the model produces a genuinely different but
// related recipe instead of an extraction of the one it's given.
var variantInstructions = llm.NoCarryOverTimeNote + llm.VariantAddendum

// GenerateRecipeVariant proposes a new, unsaved RecipeDocument as a twist on base.
// hint, if non-empty, steers the twist (e.g. "make it Thai-style"); otherwise the
// model picks one. Reuses recipeJSONSchema()/generateStructuredFromMessages verbatim
// — a variant is shaped exactly like any other generated recipe.
func GenerateRecipeVariant(ctx context.Context, base model.RecipeDocument, hint string) (model.RecipeDocument, error) {
	instruction := "Create a variant of this recipe."
	if hint != "" {
		instruction = "Create a variant of this recipe: " + hint
	}
	raw, err := json.Marshal(base)
	if err != nil {
		return model.RecipeDocument{}, fmt.Errorf("marshal base recipe: %w", err)
	}
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(buildSystemPrompt(base.Language) + variantInstructions),
		openai.UserMessage("Base recipe: " + base.Title),
		openai.AssistantMessage(string(raw)),
		openai.UserMessage(instruction),
	}
	return generateStructuredFromMessages(ctx, "meal_plan_variant", messages)
}

// CookedCooldown is how long a recipe stays out of the meal-plan candidate list after
// the user marks it cooked (BACKLOG 6.3) — long enough that a week's plan doesn't repeat
// what was eaten days ago, short enough that a small library still cycles.
const CookedCooldown = 14 * 24 * time.Hour

// suppressRecentlyCooked drops candidates cooked within CookedCooldown of now. A library
// where everything is on cooldown falls back to the full list: no candidates means the
// planner has nothing to plan, which is worse than a repeat.
func suppressRecentlyCooked(candidates []model.Recipe, now time.Time) []model.Recipe {
	kept := make([]model.Recipe, 0, len(candidates))
	for _, r := range candidates {
		if r.LastCookedAt == nil || now.Sub(*r.LastCookedAt) >= CookedCooldown {
			kept = append(kept, r)
		}
	}
	if len(kept) == 0 {
		return candidates
	}
	return kept
}

// LoadMealPlanSignal gathers the candidate recipes + soft personalization signal shared
// by /meal-plan/suggest, /meal-plan/chat and the plan_week chat tool, with the user's per-week scheduling
// overrides (if any) for the week containing startsOn merged over their global
// defaults. A missing cooking profile (not yet computed by the background worker) or
// unset preferences are not errors — both zero values are fine soft-signal inputs.
func LoadMealPlanSignal(ctx context.Context, userID int, startsOn time.Time) ([]model.Recipe, model.CookingProfile, model.UserPreferences, error) {
	candidates, err := storage.GetRecipes(userID)
	if err != nil {
		return nil, model.CookingProfile{}, model.UserPreferences{}, err
	}
	candidates = suppressRecentlyCooked(candidates, time.Now())
	profile, _, err := storage.GetCookingProfile(ctx, userID)
	if err != nil {
		return nil, model.CookingProfile{}, model.UserPreferences{}, err
	}
	prefs, err := storage.GetPreferences(ctx, userID)
	if err != nil {
		return nil, model.CookingProfile{}, model.UserPreferences{}, err
	}
	// What may be served is the whole table's business, not the caller's (BACKLOG 15.1).
	// Solo this returns the caller's own two arrays unchanged.
	if dietary, disliked, err := storage.KitchenDietary(ctx, userID); err == nil {
		prefs.DietaryPrefs, prefs.DislikedIngredients = dietary, disliked
	}
	return candidates, profile, prefs, nil
}

// LoadPriorMeal returns the most recently planned meal-plan item in the 3 days strictly
// before startsOn, so a no-cook day at the very start of a range can carry forward a
// dish cooked just before the week boundary (e.g. "cook Sunday for Monday"). No prior
// item is not an error.
func LoadPriorMeal(ctx context.Context, userID int, startsOn time.Time) (*model.MealPlanItem, error) {
	items, err := storage.GetMealPlanRange(ctx, userID, startsOn.AddDate(0, 0, -3), startsOn.AddDate(0, 0, -1))
	if err != nil || len(items) == 0 {
		return nil, err
	}
	last := items[len(items)-1] // GetMealPlanRange orders by planned_on, then meal slot
	return &last, nil
}
