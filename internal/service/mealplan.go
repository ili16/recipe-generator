package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// mealPlanAssignmentSchema describes one proposed day: exactly one of recipe_id
// (existing saved recipe) or variant (a brand-new proposed recipe) is non-null.
func mealPlanAssignmentSchema() map[string]any {
	nullable := func(typ string) map[string]any {
		return map[string]any{
			"anyOf": []any{map[string]any{"type": typ}, map[string]any{"type": "null"}},
		}
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"recipe_id":            nullable("integer"),
			"variant":              map[string]any{"anyOf": []any{recipeJSONSchema(), map[string]any{"type": "null"}}},
			"variant_of_recipe_id": nullable("integer"),
			"planned_on":           map[string]any{"type": "string"},
			"start_time":           map[string]any{"type": "string"},
		},
		"required":             []string{"recipe_id", "variant", "variant_of_recipe_id", "planned_on", "start_time"},
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

const mealPlanSuggestSystemPromptBase = `You are a meal-planning assistant. You are given a user's saved recipes and asked to assign one recipe per day to fill a date range.

Rules:
- Only use recipe_id values from the "Saved recipes" list provided — never invent a new recipe_id.
- Each assignment sets exactly one of "recipe_id" (use an existing saved recipe) or "variant" (propose a brand-new recipe, only when explicitly permitted below) — never both, never neither.
- When "variant" is set, "variant_of_recipe_id" must name the saved recipe it's a twist on, and "recipe_id" must be null. When "recipe_id" is set, "variant" and "variant_of_recipe_id" must be null.
- Prefer variety across the range — avoid repeating the same recipe on consecutive days when enough saved recipes exist.
- Use the user's most-cooked tags, preferred difficulty, dietary preferences, and disliked ingredients as soft signal, but every assignment must still come from (or be a variant of) the saved recipes list.
- Each meal takes about one hour; default start_time to "18:00:00" unless the user's message says otherwise.
- Respect any "needs no food planned" / "doesn't want to cook" / "cooks in batches" instructions in the context below: "needs no food" days get skipped entirely (no assignment for that date); "doesn't want to cook" days still get an assignment — repeat the most recent prior cooked day's recipe_id/variant (a leftover day) instead of skipping it or inventing a new dish, using the "Most recently cooked meal before this range" line when the no-cook day is at the very start of the range; when batching, repeat the same recipe_id across each run of that many consecutive days instead of a different recipe each day.
- Respond with status "applied" and exactly one assignment per day in the range (skipped days simply have no assignment). Only use status "needs_clarification" (assignments empty) if the saved recipes list is completely empty.`

const mealPlanLowVarietyAddendum = `

The user has fewer than 10 saved recipes, so variety will be limited. Set "low_variety" to true. You may propose "variant" days (a new recipe generated as a twist on one of the saved ones) for at most 2 days in the range to fill it out, instead of only repeating existing recipes.`

func mealPlanSuggestSystemPrompt(lowVariety bool) string {
	if lowVariety {
		return mealPlanSuggestSystemPromptBase + mealPlanLowVarietyAddendum
	}
	return mealPlanSuggestSystemPromptBase
}

// mealPlanChatSystemPrompt governs /meal-plan/chat. Unlike the fresh-week suggester, a
// chat message is a bounded, explicit edit request against an already-proposed plan —
// so it is never gated by low_variety: a user asking to swap an ingredient on one day
// must always have a legal way to express that as a targeted "variant", not just a pick
// from the saved-recipes list (which is how a generic ingredient swap like "shrimp to
// beef" used to end up substituting an unrelated saved recipe instead of editing the
// current dish).
const mealPlanChatSystemPrompt = mealPlanSuggestSystemPromptBase + `

You are now refining an existing plan via chat, not proposing one from scratch:
- The last assistant turn is the current plan. Leave every day the user's message doesn't ask you to change exactly as it is (same recipe_id/variant, same day/time).
- For a day the user does want changed — swapping an ingredient, protein, cuisine style, etc. — you MAY produce a "variant" for that day regardless of how many saved recipes the user has: base it on that day's current dish (its current recipe_id's saved recipe, or its current variant), keep the same dish identity, and change only what was asked. Use the "Current day recipes" ingredient/step details below to ground the edit in the real recipe, not just its title.
- Only pick a different existing recipe_id when the user is explicitly asking for a different already-saved recipe by name/description — never as a substitute for an edit you can't express by picking from the list.`

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
		b.WriteString("Saved recipes (id: title [tags]):\n")
		for _, r := range candidates {
			fmt.Fprintf(&b, "%d: %s %v\n", r.ID, r.Recipename, r.Tags)
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

// buildMealPlanChatMessages threads an in-progress plan-refinement conversation the
// same way buildRefineMessages does for a single recipe: original context, the
// assistant's initial proposal, every accepted instruction paired with the plan it
// produced, and finally the new message. assignedRecipes carries the structured content
// (ingredients) for every recipe_id currently in initial.Assignments, so a targeted edit
// ("swap the shrimp for beef") has real ingredients to work from instead of just a title.
func buildMealPlanChatMessages(candidates []model.Recipe, profile model.CookingProfile, prefs model.UserPreferences, startsOn, endsOn time.Time, priorMeal *model.MealPlanItem, assignedRecipes map[int]model.RecipeDocument, initial model.MealPlanSuggestion, history []model.MealPlanChatTurn, message string) ([]openai.ChatCompletionMessageParamUnion, error) {
	contextText := buildMealPlanContextText(candidates, profile, prefs, startsOn, endsOn, priorMeal)
	if len(assignedRecipes) > 0 {
		var b strings.Builder
		b.WriteString("\nCurrent day recipes (ingredients, for grounding an edit):\n")
		for id, doc := range assignedRecipes {
			fmt.Fprintf(&b, "%d: %s — %s\n", id, doc.Title, renderIngredientsSummary(doc))
		}
		contextText += b.String()
	}
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(mealPlanChatSystemPrompt),
		openai.UserMessage(contextText),
	}

	appendPlan := func(plan model.MealPlanSuggestion) error {
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		messages = append(messages, openai.AssistantMessage(string(raw)))
		return nil
	}

	if err := appendPlan(initial); err != nil {
		return nil, fmt.Errorf("marshal initial plan: %w", err)
	}
	for _, turn := range history {
		messages = append(messages, openai.UserMessage(turn.Message))
		if err := appendPlan(turn.Plan); err != nil {
			return nil, fmt.Errorf("marshal history plan: %w", err)
		}
	}
	messages = append(messages, openai.UserMessage(message))
	return messages, nil
}

// generateMealPlanSuggestion mirrors generateRefineFromMessages's call shape but
// decodes into model.MealPlanSuggestion against mealPlanSuggestionJSONSchema.
func generateMealPlanSuggestion(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (model.MealPlanSuggestion, error) {
	client := OpenAIclient()

	slog.DebugContext(ctx, "llm: meal plan suggestion start", "model", recipeModel, "messages", len(messages))
	t := time.Now()

	ctx, span := telemetry.StartSpan(ctx, "llm.chat_completion")
	span.SetAttributes(attribute.String("llm.model", string(recipeModel)), attribute.String("llm.call_type", "meal_plan_suggest"))
	defer span.End()

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    recipeModel,
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "meal_plan_suggestion",
					Schema: mealPlanSuggestionJSONSchema(),
					Strict: param.NewOpt(true),
				},
			},
		},
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.RecordLLMCall(ctx, "meal_plan_suggest", string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.MealPlanSuggestion{}, fmt.Errorf("meal plan suggestion completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		telemetry.RecordLLMCall(ctx, "meal_plan_suggest", string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.MealPlanSuggestion{}, errors.New("no choices returned")
	}
	telemetry.RecordLLMUsage(ctx, "meal_plan_suggest", string(recipeModel), completion.Usage.PromptTokens, completion.Usage.CompletionTokens)
	recordCost(ctx, "meal_plan_suggest", string(recipeModel), completion.Usage.PromptTokens, completion.Usage.CompletionTokens)

	raw := completion.Choices[0].Message.Content
	slog.DebugContext(ctx, "llm: meal plan suggestion raw output", "json", raw)

	var result model.MealPlanSuggestion
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		telemetry.RecordLLMCall(ctx, "meal_plan_suggest", string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.MealPlanSuggestion{}, fmt.Errorf("parse meal plan suggestion response: %w", err)
	}
	if result.Status == "applied" && len(result.Assignments) == 0 {
		telemetry.RecordLLMCall(ctx, "meal_plan_suggest", string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.MealPlanSuggestion{}, errors.New("applied status missing assignments")
	}
	slog.DebugContext(ctx, "llm: meal plan suggestion result", "status", result.Status, "dur_ms", time.Since(t).Milliseconds(), "tokens", completion.Usage.TotalTokens)
	telemetry.RecordLLMCall(ctx, "meal_plan_suggest", string(recipeModel), time.Since(t).Milliseconds(), true)
	return result, nil
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
	return generateMealPlanSuggestion(ctx, messages)
}

// MealPlanChat refines an in-progress plan proposal with a new user instruction,
// stateless like /refine-recipe — the caller resends the full history each call.
// assignedRecipes grounds a targeted edit in the real ingredients of whatever's
// currently assigned (see buildMealPlanChatMessages).
func MealPlanChat(ctx context.Context, candidates []model.Recipe, profile model.CookingProfile, prefs model.UserPreferences, startsOn, endsOn time.Time, priorMeal *model.MealPlanItem, assignedRecipes map[int]model.RecipeDocument, initial model.MealPlanSuggestion, history []model.MealPlanChatTurn, message string) (model.MealPlanSuggestion, error) {
	messages, err := buildMealPlanChatMessages(candidates, profile, prefs, startsOn, endsOn, priorMeal, assignedRecipes, initial, history, message)
	if err != nil {
		return model.MealPlanSuggestion{}, err
	}
	return generateMealPlanSuggestion(ctx, messages)
}

const mealPlanChatReplySystemPrompt = `You are a friendly cooking assistant helping someone adjust their AI-suggested meal plan via chat. Given their message and the plan context, reply in 1-2 short, natural sentences acknowledging what you're doing (or, if it's not possible given the saved recipes, saying so briefly). Do not list the full plan or use markdown — just a short conversational acknowledgment, plain text.`

// StreamMealPlanChatReply streams a short conversational acknowledgment of message
// (assistModel, plain text, no JSON schema) via onChunk as tokens arrive, then records
// cost/telemetry once the stream completes. Runs alongside MealPlanChat, which does
// the actual structured plan update — this call exists purely so the chat feels alive
// while that heavier call is in flight.
func StreamMealPlanChatReply(ctx context.Context, candidates []model.Recipe, profile model.CookingProfile, prefs model.UserPreferences, startsOn, endsOn time.Time, message string, onChunk func(string)) error {
	client := OpenAIclient()
	contextText := buildMealPlanContextText(candidates, profile, prefs, startsOn, endsOn, nil)

	slog.DebugContext(ctx, "llm: meal plan chat reply stream start", "model", assistModel)
	t := time.Now()

	ctx, span := telemetry.StartSpan(ctx, "llm.chat_completion")
	span.SetAttributes(attribute.String("llm.model", string(assistModel)), attribute.String("llm.call_type", "meal_plan_chat_reply"))
	defer span.End()

	stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(mealPlanChatReplySystemPrompt),
			openai.UserMessage(contextText),
			openai.UserMessage(message),
		},
		Model:         assistModel,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: param.NewOpt(true)},
	})
	defer stream.Close()

	var promptTokens, completionTokens int64
	for stream.Next() {
		chunk := stream.Current()
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			onChunk(chunk.Choices[0].Delta.Content)
		}
		if chunk.Usage.TotalTokens > 0 {
			promptTokens, completionTokens = chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens
		}
	}
	if err := stream.Err(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.RecordLLMCall(ctx, "meal_plan_chat_reply", string(assistModel), time.Since(t).Milliseconds(), false)
		return fmt.Errorf("meal plan chat reply stream: %w", err)
	}

	telemetry.RecordLLMUsage(ctx, "meal_plan_chat_reply", string(assistModel), promptTokens, completionTokens)
	recordCost(ctx, "meal_plan_chat_reply", string(assistModel), promptTokens, completionTokens)
	slog.DebugContext(ctx, "llm: meal plan chat reply stream done", "dur_ms", time.Since(t).Milliseconds())
	telemetry.RecordLLMCall(ctx, "meal_plan_chat_reply", string(assistModel), time.Since(t).Milliseconds(), true)
	return nil
}

// variantInstructions is appended to the base structured-recipe system prompt for
// POST /meal-plan/variants/{id} so the model produces a genuinely different but
// related recipe instead of an extraction of the one it's given.
const variantInstructions = "\n\nYou are producing a VARIANT of an existing recipe: a genuinely different, complete recipe that shares the same core idea (main ingredient, dish type, or technique) but differs meaningfully — a different cuisine style, a protein/ingredient swap, or a different cooking method. Do not just re-describe the base recipe."

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
