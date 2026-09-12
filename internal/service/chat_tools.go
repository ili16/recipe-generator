package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/ili16/recipe-generator/internal/utils"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// The tool catalog behind POST /api/v1/chat (specs/AGENT_CHAT.md §2.2). Two rules hold
// for every entry:
//
//   - Ownership never comes from tool args. The user is whoever auth.FromContext put on
//     the session; a recipe id in args is only ever passed to a storage call that is
//     already scoped by that user, so naming someone else's id yields not-found.
//   - Every tool returns JSON, never prose — a tool result the model has to re-parse out
//     of English is a tool result written by hand.
//
// A tool error is not fatal to the turn: it is handed back to the model as
// {"error": "..."} so it can apologise, retry differently, or ask the user. The text is
// always one we authored — raw err.Error() never reaches the model or the wire.

// errSignInRequired is returned by every tool that touches durable per-user state when
// the caller is anonymous. Anonymous callers can still generate and ask questions, which
// is the acquisition funnel the spec asks to keep working.
var errSignInRequired = errors.New("this needs a signed-in account; ask the user to sign in")

// artifact is a structured result the client renders as a card in the transcript rather
// than as prose — the primitive that makes "make it vegan" work in the same surface as
// "what's a good side for this?" (specs/AGENT_CHAT.md §2).
type artifact struct {
	Kind string `json:"kind"` // recipe | week_plan
	Data any    `json:"data"`
}

// toolFunc runs one tool. The returned value is JSON-marshalled into the tool message;
// a non-nil artifact is additionally streamed to the client as event: artifact.
type toolFunc func(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error)

type tool struct {
	Description string
	Params      map[string]any
	NeedsAuth   bool
	Run         toolFunc
}

// obj builds a JSON-schema object for a tool's parameters. Only required lists names the
// model must always supply; the rest are optional, so strict mode stays off here (unlike
// the response schemas, where every field is required by construction).
func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func strList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

var chatTools = map[string]tool{
	"generate_recipe": {
		Description: "Create a new recipe from a description, a webpage URL, or a photo the user attached. Returns a draft_ref naming the unsaved draft; use it with transform_recipe or save_recipe.",
		Params: obj(map[string]any{
			"description": str("What the user asked for, e.g. 'a quick vegan pasta for two'"),
			"url":         str("A webpage to extract the recipe from, if the user gave one"),
			"image_ref":   str("The ref from an [attached photo] marker in the user's message, e.g. 'image1'"),
			"language":    str("ISO 639-1 code for the output language; omit to infer"),
		}),
		Run: toolGenerateRecipe,
	},
	"transform_recipe": {
		Description: "Apply an instruction ('make it vegan', 'halve it') to a recipe. target is a draft_ref from generate_recipe or a saved recipe id.",
		Params: obj(map[string]any{
			"target":      str("A draft_ref, or the id of a saved recipe"),
			"instruction": str("The change to make, in the user's own words"),
		}, "target", "instruction"),
		Run: toolTransformRecipe,
	},
	"search_my_recipes": {
		Description: "Search the user's saved recipes by title text and/or tags. Returns id, title and tags only.",
		Params: obj(map[string]any{
			"query": str("Text to match against recipe titles"),
			"tags":  strList("Tag slugs a recipe must all carry"),
		}),
		NeedsAuth: true,
		Run:       toolSearchMyRecipes,
	},
	"get_recipe": {
		Description: "Load one of the user's saved recipes in full, including ingredients and steps.",
		Params:      obj(map[string]any{"id": integer("The saved recipe's id")}, "id"),
		NeedsAuth:   true,
		Run:         toolGetRecipe,
	},
	"save_recipe": {
		Description: "Save a draft to the user's account. Only call this when the user has actually asked to save.",
		Params: obj(map[string]any{
			"draft_ref": str("The draft_ref to save"),
			"tags":      strList("Tag slugs; omit to keep the ones the draft already carries"),
		}, "draft_ref"),
		NeedsAuth: true,
		Run:       toolSaveRecipe,
	},
	"plan_week": {
		Description: "Propose (not save) a meal plan for the week starting on starts_on, drawn from the user's saved recipes and their scheduling preferences. Days before today are already cooked and are left out of the proposal. Returns the proposal plus variant_drafts, a date -> draft_ref map for days that are new recipes. To apply it, call apply_week_plan once with every day — never set_plan_day in a loop.",
		Params: obj(map[string]any{
			"starts_on":   str("First day of the range, YYYY-MM-DD"),
			"days":        integer("How many days to plan; defaults to 7"),
			"constraints": str("Anything extra the user asked for this week"),
		}, "starts_on"),
		NeedsAuth: true,
		Run:       toolPlanWeek,
	},
	"apply_week_plan": {
		Description: "Save a whole proposed plan to the user's meal plan in one atomic step: all days are written or none are. Call this once with every day of the plan. Days before today are already cooked and are skipped (reported as skipped_already_cooked). Days already filled with a different recipe are reported back instead of overwritten — show that list to the user and call again with confirm true once they agree.",
		Params: obj(map[string]any{
			"days": map[string]any{
				"type":        "array",
				"description": "One entry per day to write",
				"items": obj(map[string]any{
					"date":      str("The day to fill, YYYY-MM-DD"),
					"recipe_id": integer("A saved recipe's id"),
					"draft_ref": str("A draft_ref (e.g. from plan_week's variant_drafts) to save first, then plan"),
				}, "date"),
			},
			"confirm": map[string]any{"type": "boolean", "description": "Set true to go ahead and replace days that are already planned"},
		}, "days"),
		NeedsAuth: true,
		Run:       toolApplyWeekPlan,
	},
	"set_plan_day": {
		Description: "Put one recipe on one day of the user's saved meal plan. Only call this when the user has asked for that one specific day to change; to apply a whole plan use apply_week_plan.",
		Params: obj(map[string]any{
			"date":      str("The day to fill, YYYY-MM-DD"),
			"recipe_id": integer("A saved recipe's id"),
			"draft_ref": str("A draft_ref to save first, then plan"),
		}, "date"),
		NeedsAuth: true,
		Run:       toolSetPlanDay,
	},
	"clear_plan_day": {
		Description: "Remove everything planned on one day of the user's saved meal plan.",
		Params:      obj(map[string]any{"date": str("The day to clear, YYYY-MM-DD")}, "date"),
		NeedsAuth:   true,
		Run:         toolClearPlanDay,
	},
	"get_preferences": {
		Description: "Read the user's dietary preferences, disliked ingredients, skill level and meal-plan scheduling settings.",
		Params:      obj(map[string]any{}),
		NeedsAuth:   true,
		Run:         toolGetPreferences,
	},
	"set_preferences": {
		Description: "Update the user's preferences. patch carries only the fields to change, using the same field names get_preferences returns.",
		Params: obj(map[string]any{
			"patch": map[string]any{"type": "object", "description": "Fields to change, e.g. {\"dietary_prefs\": [\"vegetarian\"]}"},
		}, "patch"),
		NeedsAuth: true,
		Run:       toolSetPreferences,
	},
	"fetch_url": {
		Description: "Fetch the readable text of a webpage. Use this to look at a link the user shared when it isn't a recipe to extract.",
		Params:      obj(map[string]any{"url": str("The page to fetch")}, "url"),
		Run:         toolFetchURL,
	},
	"answer_cooking_question": {
		Description: "Answer a practical cooking question, optionally about a specific recipe and step.",
		Params: obj(map[string]any{
			"question":   str("The user's question"),
			"recipe_ref": str("A draft_ref or saved recipe id the question is about"),
			"step":       str("The step the user is on, if any"),
		}, "question"),
		Run: toolAnswerCookingQuestion,
	},
}

// toolDefinitions renders the catalog for the OpenAI request, in a stable order so the
// prompt prefix stays byte-identical across turns and stays cacheable.
func toolDefinitions() []openai.ChatCompletionToolParam {
	names := make([]string, 0, len(chatTools))
	for name := range chatTools {
		names = append(names, name)
	}
	sort.Strings(names)

	defs := make([]openai.ChatCompletionToolParam, 0, len(names))
	for _, name := range names {
		t := chatTools[name]
		defs = append(defs, openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        name,
				Description: openai.String(t.Description),
				Parameters:  shared.FunctionParameters(t.Params),
			},
		})
	}
	return defs
}

// dispatch runs one tool call. An unknown name, malformed arguments, or a tool error all
// come back as errors for the caller to hand to the model — none of them end the turn.
func dispatch(ctx context.Context, s *chatSession, name string, rawArgs string) (any, *artifact, error) {
	t, ok := chatTools[name]
	if !ok {
		return nil, nil, fmt.Errorf("no such tool: %s", name)
	}
	if t.NeedsAuth && s.userID == 0 {
		return nil, nil, errSignInRequired
	}
	args := json.RawMessage(rawArgs)
	if len(strings.TrimSpace(rawArgs)) == 0 {
		args = json.RawMessage("{}")
	}
	return t.Run(ctx, s, args)
}

// ---- tools ----------------------------------------------------------------

func toolGenerateRecipe(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Description string `json:"description"`
		URL         string `json:"url"`
		ImageRef    string `json:"image_ref"`
		Language    string `json:"language"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	if a.Description == "" && a.URL == "" && a.ImageRef == "" {
		return nil, nil, errors.New("give a description, a url or an image_ref")
	}

	var image io.Reader
	if a.ImageRef != "" {
		data, ok := s.images[a.ImageRef]
		if !ok {
			return nil, nil, errors.New("that photo isn't attached to this message; ask the user to send it again")
		}
		image = bytes.NewReader(data)
	}

	doc, err := GenerateRecipe(ctx, model.GenerateRecipeInput{
		Prompt:            a.Description,
		URL:               a.URL,
		Image:             image,
		PreferredLanguage: a.Language,
	})
	if err != nil {
		return nil, nil, errors.New("could not produce a recipe from that")
	}
	// Which of the three inputs the user actually gave is the provenance, and this is
	// the only place that still knows it.
	origin := &model.GenerationOrigin{Prompt: a.Description, SourceType: "text", SourceURL: a.URL}
	switch {
	case a.URL != "":
		origin.SourceType = "url"
	case a.ImageRef != "":
		origin.SourceType = "image"
	}
	return s.addDraft(doc, origin)
}

func toolTransformRecipe(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Target      string `json:"target"`
		Instruction string `json:"instruction"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	doc, err := s.resolveRecipe(ctx, a.Target)
	if err != nil {
		return nil, nil, err
	}

	messages, err := buildRefineMessages(model.GenerationOrigin{Prompt: doc.Title, SourceType: "text"}, doc, nil, a.Instruction)
	if err != nil {
		return nil, nil, errors.New("could not prepare that change")
	}
	// This tool inherited /refine-recipe's traffic when BACKLOG 3.13 deleted that route,
	// so it inherits its metric too — otherwise refine latency and the applied/rejected
	// split go dark the moment the old handler is gone.
	start := time.Now()
	result, err := generateRefineFromMessages(ctx, messages)
	if err != nil {
		telemetry.RecordRefine(ctx, time.Since(start).Milliseconds(), "error")
		return nil, nil, errors.New("could not apply that change")
	}
	telemetry.RecordRefine(ctx, time.Since(start).Milliseconds(), result.Status)
	if result.Status != "applied" || result.Recipe == nil {
		// rejected / needs_choice: the envelope is the answer, not a failure.
		return map[string]any{"status": result.Status, "message": result.Message, "options": result.Options}, nil, nil
	}
	// An adjusted draft keeps the original's provenance — "from seriouseats.com" is
	// still true after "make it vegan". A saved-id target has none to inherit.
	return s.addDraft(*result.Recipe, s.draftInfo[strings.TrimSpace(a.Target)].origin)
}

func toolSearchMyRecipes(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Query string   `json:"query"`
		Tags  []string `json:"tags"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	recipes, err := storage.GetRecipes(s.userID)
	if err != nil {
		return nil, nil, errors.New("could not read your saved recipes")
	}

	type hit struct {
		ID    int      `json:"id"`
		Title string   `json:"title"`
		Tags  []string `json:"tags"`
	}
	hits := []hit{}
	query := strings.ToLower(strings.TrimSpace(a.Query))
	for _, r := range recipes {
		if query != "" && !strings.Contains(strings.ToLower(r.Recipename), query) {
			continue
		}
		if !hasAllTags(r.Tags, a.Tags) {
			continue
		}
		hits = append(hits, hit{ID: r.ID, Title: r.Recipename, Tags: r.Tags})
	}
	return map[string]any{"count": len(hits), "recipes": hits}, nil, nil
}

// hasAllTags reports whether have contains every tag in want (empty want matches all).
func hasAllTags(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if strings.EqualFold(h, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func toolGetRecipe(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	// GetRecipeByID is scoped by user_id, so another user's id is simply not found.
	recipe, err := storage.GetRecipeByID(ctx, s.userID, a.ID)
	if err != nil {
		return nil, nil, errors.New("no such recipe")
	}
	if recipe.Structured == nil {
		return map[string]any{"id": recipe.ID, "title": recipe.Recipename, "content": recipe.Recipe}, nil, nil
	}
	return map[string]any{"id": recipe.ID, "recipe": recipe.Structured},
		&artifact{Kind: "recipe", Data: map[string]any{"recipe_id": recipe.ID, "document": recipe.Structured}}, nil
}

func toolSaveRecipe(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		DraftRef string   `json:"draft_ref"`
		Tags     []string `json:"tags"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	id, doc, err := s.saveDraft(ctx, a.DraftRef, a.Tags)
	if err != nil {
		return nil, nil, err
	}
	return map[string]any{"recipe_id": id, "title": doc.Title}, nil, nil
}

func toolPlanWeek(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		StartsOn    string `json:"starts_on"`
		Days        int    `json:"days"`
		Constraints string `json:"constraints"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	startsOn, err := time.Parse(time.DateOnly, a.StartsOn)
	if err != nil {
		return nil, nil, errors.New("starts_on must be a date like 2026-09-14")
	}
	days := a.Days
	if days < 1 || days > 42 {
		days = 7
	}
	endsOn := startsOn.AddDate(0, 0, days-1)
	// "Plan this week" on a Thursday names Monday as starts_on, but Mon–Wed are already
	// cooked: propose from today onwards and leave the days behind us alone.
	var skippedPast string
	if startsOn.Before(today()) {
		if endsOn.Before(today()) {
			return nil, nil, errors.New("that whole range is in the past — it's already been cooked")
		}
		skippedPast = startsOn.Format(time.DateOnly)
		startsOn = today()
	}

	candidates, profile, prefs, err := LoadMealPlanSignal(ctx, s.userID, startsOn)
	if err != nil {
		return nil, nil, errors.New("could not read your recipes and preferences")
	}
	if a.Constraints != "" {
		prefs.DietaryPrefs = append(prefs.DietaryPrefs, a.Constraints)
	}
	priorMeal, err := LoadPriorMeal(ctx, s.userID, startsOn)
	if err != nil {
		return nil, nil, errors.New("could not read your existing plan")
	}

	// SuggestMealPlan runs the deterministic post-validation (no-cook carryover) itself
	// and records what it overrode on suggestion.Message, so the model explains the
	// difference instead of reporting an unqualified success (BACKLOG.md 2.5).
	suggestion, err := SuggestMealPlan(ctx, candidates, profile, prefs, startsOn, endsOn, priorMeal)
	if err != nil {
		return nil, nil, errors.New("could not put a plan together")
	}
	// A proposed day can be a brand-new variant recipe that exists nowhere yet. Park each
	// one as a draft so "apply it" has something set_plan_day can name — without this the
	// variant days of a plan are unappliable, which is the only path the planner now has
	// (BACKLOG.md 4.2).
	variantDrafts := map[string]string{}
	for _, a := range suggestion.Assignments {
		if a.Variant != nil {
			parent := 0
			if a.VariantOfRecipeID != nil {
				parent = *a.VariantOfRecipeID
			}
			variantDrafts[a.PlannedOn] = s.parkDraft(*a.Variant, parent, nil)
		}
	}

	out := map[string]any{"plan": suggestion, "variant_drafts": variantDrafts}
	if skippedPast != "" {
		out["note"] = "the range started " + skippedPast + ", but days before today were already cooked, so the plan starts today"
	}
	return out, &artifact{Kind: "week_plan", Data: map[string]any{
		"starts_on": startsOn.Format(time.DateOnly),
		"ends_on":   endsOn.Format(time.DateOnly),
		"plan":      suggestion,
	}}, nil
}

// planDayArg is one day as the model names it: a date plus either a saved recipe or a
// draft to save first. Shared by set_plan_day and apply_week_plan.
type planDayArg struct {
	Date     string `json:"date"`
	RecipeID int    `json:"recipe_id"`
	DraftRef string `json:"draft_ref"`
}

// resolvePlanDays turns the model's day arguments into storage assignments, saving any
// named drafts on the way (idempotently — see saveDraft). Drafts are saved before the
// apply transaction opens, so a failed apply can leave a recipe saved but unplanned;
// re-running reuses it rather than duplicating it.
// ponytail: one recipe per day, always 18:00 — meal_slot and per-day times are BACKLOG 6.4/4.4.
func (s *chatSession) resolvePlanDays(ctx context.Context, days []planDayArg) ([]storage.PlannedDay, []string, error) {
	if len(days) == 0 {
		return nil, nil, errors.New("give at least one day")
	}
	out := make([]storage.PlannedDay, 0, len(days))
	var past []string
	seen := map[string]bool{}
	for _, d := range days {
		plannedOn, err := time.Parse(time.DateOnly, d.Date)
		if err != nil {
			return nil, nil, errors.New("date must be a date like 2026-09-14")
		}
		if seen[d.Date] {
			return nil, nil, errors.New("the same day is listed twice: " + d.Date)
		}
		seen[d.Date] = true
		// A day before today has already been cooked — replanning the current week must
		// leave it alone rather than rewrite history.
		if plannedOn.Before(today()) {
			past = append(past, d.Date)
			continue
		}

		recipeID := d.RecipeID
		if recipeID == 0 {
			if d.DraftRef == "" {
				return nil, nil, errors.New("each day needs either recipe_id or draft_ref")
			}
			// A draft has to exist as a saved recipe before it can be planned.
			if recipeID, _, err = s.saveDraft(ctx, d.DraftRef, nil); err != nil {
				return nil, nil, err
			}
		}
		out = append(out, storage.PlannedDay{RecipeID: recipeID, PlannedOn: plannedOn, StartTime: "18:00:00"})
	}
	if len(out) == 0 {
		return nil, past, errors.New("those days are already in the past and were cooked — pick today or later")
	}
	return out, past, nil
}

// today is the current date at midnight, the boundary between "already cooked" (which no
// plan tool may touch) and "still plannable".
// ponytail: server local time, not the user's — send the device date on the chat turn if
// anyone ever plans from another timezone.
func today() time.Time {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local)
}

// applyPlanDays writes the resolved days, refusing first if any of them would replace a
// different recipe the user already has planned and confirm is not set.
func (s *chatSession) applyPlanDays(ctx context.Context, days []storage.PlannedDay, confirm bool) (any, error) {
	if !confirm {
		conflicts, err := s.wouldOverwrite(ctx, days)
		if err != nil {
			return nil, err
		}
		if len(conflicts) > 0 {
			return map[string]any{"applied": false, "confirm_required": true, "would_overwrite": conflicts}, nil
		}
	}

	prefs, err := storage.GetPreferences(ctx, s.userID)
	if err != nil {
		return nil, errors.New("could not read your preferences")
	}
	// One transaction, ownership checked per recipe inside it: all days land or none do.
	items, err := storage.ApplyMealPlanDays(ctx, s.userID, prefs.WeekStartDay, days)
	if err != nil {
		return nil, errors.New("could not save that plan")
	}
	return map[string]any{"applied": true, "items": items}, nil
}

// wouldOverwrite lists the days already holding a *different* recipe. Re-applying the
// same plan reports nothing, which is what makes a repeat call a no-op instead of a
// second round of confirmation.
func (s *chatSession) wouldOverwrite(ctx context.Context, days []storage.PlannedDay) ([]map[string]any, error) {
	first, last := days[0].PlannedOn, days[0].PlannedOn
	for _, d := range days {
		if d.PlannedOn.Before(first) {
			first = d.PlannedOn
		}
		if d.PlannedOn.After(last) {
			last = d.PlannedOn
		}
	}
	existing, err := storage.GetMealPlanRange(ctx, s.userID, first, last)
	if err != nil {
		return nil, errors.New("could not read your plan")
	}
	byDate := map[string]model.MealPlanItem{}
	for _, item := range existing {
		byDate[item.PlannedOn] = item
	}

	conflicts := []map[string]any{}
	for _, d := range days {
		item, ok := byDate[d.PlannedOn.Format(time.DateOnly)]
		if ok && item.RecipeID != d.RecipeID {
			conflicts = append(conflicts, map[string]any{"date": item.PlannedOn, "current_recipe": item.RecipeTitle})
		}
	}
	return conflicts, nil
}

func toolApplyWeekPlan(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Days    []planDayArg `json:"days"`
		Confirm bool         `json:"confirm"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	days, past, err := s.resolvePlanDays(ctx, a.Days)
	if err != nil {
		return nil, nil, err
	}
	result, err := s.applyPlanDays(ctx, days, a.Confirm)
	if err != nil {
		return nil, nil, err
	}
	if m, ok := result.(map[string]any); ok && len(past) > 0 {
		m["skipped_already_cooked"] = past
	}
	return result, nil, nil
}

func toolSetPlanDay(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a planDayArg
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	days, _, err := s.resolvePlanDays(ctx, []planDayArg{a})
	if err != nil {
		return nil, nil, err
	}
	// A single day the user named explicitly is its own confirmation.
	result, err := s.applyPlanDays(ctx, days, true)
	if err != nil {
		return nil, nil, err
	}
	return result, nil, nil
}

func toolClearPlanDay(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	day, err := time.Parse(time.DateOnly, a.Date)
	if err != nil {
		return nil, nil, errors.New("date must be a date like 2026-09-14")
	}
	if day.Before(today()) {
		return nil, nil, errors.New("that day is already in the past and was cooked — it can't be cleared")
	}
	items, err := storage.GetMealPlanRange(ctx, s.userID, day, day)
	if err != nil {
		return nil, nil, errors.New("could not read your plan")
	}
	for _, item := range items {
		if err := storage.DeleteMealPlanItem(ctx, s.userID, item.ID); err != nil {
			return nil, nil, errors.New("could not clear that day")
		}
	}
	return map[string]any{"date": a.Date, "removed": len(items)}, nil, nil
}

func toolGetPreferences(ctx context.Context, s *chatSession, _ json.RawMessage) (any, *artifact, error) {
	prefs, err := storage.GetPreferences(ctx, s.userID)
	if err != nil {
		return nil, nil, errors.New("could not read your preferences")
	}
	return prefs, nil, nil
}

func toolSetPreferences(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Patch map[string]any `json:"patch"`
	}
	if err := json.Unmarshal(args, &a); err != nil || len(a.Patch) == 0 {
		return nil, nil, errors.New("give a patch object naming the fields to change")
	}
	current, err := storage.GetPreferences(ctx, s.userID)
	if err != nil {
		return nil, nil, errors.New("could not read your preferences")
	}
	merged, err := mergePreferences(current, a.Patch)
	if err != nil {
		return nil, nil, errors.New("could not apply that change to your preferences")
	}
	if err := storage.UpdatePreferences(ctx, s.userID, merged); err != nil {
		return nil, nil, errors.New("could not save your preferences")
	}
	return merged, nil, nil
}

// mergePreferences overlays patch onto current by round-tripping both through the
// UserPreferences JSON shape — so the tool speaks exactly the field names
// get_preferences hands the model, and unknown keys fall away on the way back in.
func mergePreferences(current model.UserPreferences, patch map[string]any) (model.UserPreferences, error) {
	raw, err := json.Marshal(current)
	if err != nil {
		return current, err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return current, err
	}
	for k, v := range patch {
		fields[k] = v
	}
	merged, err := json.Marshal(fields)
	if err != nil {
		return current, err
	}
	var out model.UserPreferences
	if err := json.Unmarshal(merged, &out); err != nil {
		return current, err
	}
	return out, nil
}

// maxFetchedPageChars bounds what one fetch_url result can add to the thread — a long
// page would otherwise be re-sent on every remaining iteration of the turn.
const maxFetchedPageChars = 8000

func toolFetchURL(_ context.Context, _ *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.URL == "" {
		return nil, nil, errors.New("give a url")
	}
	content, err := utils.GetWebsiteContent(a.URL)
	if err != nil {
		return nil, nil, errors.New("could not read that page")
	}
	truncated := len(content) > maxFetchedPageChars
	if truncated {
		content = content[:maxFetchedPageChars]
	}
	return map[string]any{"url": a.URL, "content": content, "truncated": truncated}, nil, nil
}

func toolAnswerCookingQuestion(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Question  string `json:"question"`
		RecipeRef string `json:"recipe_ref"`
		Step      string `json:"step"`
	}
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Question) == "" {
		return nil, nil, errors.New("give a question")
	}
	sys := "You are a helpful cooking assistant. Answer concisely and practically (2-4 sentences)."
	if a.RecipeRef != "" {
		if doc, err := s.resolveRecipe(ctx, a.RecipeRef); err == nil {
			sys += "\n\nRecipe:\n" + RenderMarkdown(doc)
		}
	}
	if step := strings.TrimSpace(a.Step); step != "" {
		sys += "\n\nCurrent step: " + step
	}
	answer, err := chatCompletion(ctx, sys, a.Question, string(assistModel), "cooking_chat")
	if err != nil {
		return nil, nil, errors.New("could not answer that")
	}
	return map[string]any{"answer": strings.TrimSpace(answer)}, nil, nil
}

// ---- draft handling -------------------------------------------------------

// parkDraft files a document under a fresh draft_ref so a later tool call (save_recipe,
// apply_week_plan, transform_recipe) can name it. variantOf is the saved recipe this
// document was proposed as a twist on, or 0 — it is carried onto the row when the draft
// is saved, so a plan's variant days land in My Recipes grouped under their parent
// rather than as unmarked siblings (BACKLOG.md 4.3).
func (s *chatSession) parkDraft(doc model.RecipeDocument, variantOf int, origin *model.GenerationOrigin) string {
	s.nextDraft++
	ref := "draft_" + strconv.Itoa(s.nextDraft)
	s.drafts[ref] = doc
	if s.draftInfo == nil {
		s.draftInfo = map[string]draftInfo{}
	}
	s.draftInfo[ref] = draftInfo{variantOf: variantOf, origin: origin}
	return ref
}

// addDraft parks a freshly generated document under a new draft_ref and returns the
// compact summary the model reads plus the full-document artifact the client renders.
func (s *chatSession) addDraft(doc model.RecipeDocument, origin *model.GenerationOrigin) (any, *artifact, error) {
	ref := s.parkDraft(doc, 0, origin)

	return map[string]any{
			"draft_ref":     ref,
			"title":         doc.Title,
			"summary":       doc.Summary,
			"language":      doc.Language,
			"servings":      doc.Servings,
			"total_minutes": doc.TotalMinutes,
			"tags":          doc.Tags,
			"ingredients":   len(doc.Ingredients),
			"steps":         len(doc.Steps),
		}, &artifact{Kind: "recipe", Data: map[string]any{
			"draft_ref": ref,
			"document":  doc,
		}}, nil
}

// resolveRecipe turns a draft_ref or a saved recipe id into a document. A saved id is
// looked up scoped to this session's user, so another user's id resolves to not-found.
func (s *chatSession) resolveRecipe(ctx context.Context, ref string) (model.RecipeDocument, error) {
	ref = strings.TrimSpace(ref)
	if doc, ok := s.drafts[ref]; ok {
		return doc, nil
	}
	id, err := strconv.Atoi(strings.TrimPrefix(ref, "recipe:"))
	if err != nil {
		return model.RecipeDocument{}, errors.New("no such draft: " + ref)
	}
	if s.userID == 0 {
		return model.RecipeDocument{}, errSignInRequired
	}
	recipe, err := storage.GetRecipeByID(ctx, s.userID, id)
	if err != nil || recipe.Structured == nil {
		return model.RecipeDocument{}, errors.New("no such recipe")
	}
	return *recipe.Structured, nil
}

// saveDraft persists a draft as a real recipe and returns its id. Shared by save_recipe
// and the plan tools, which have to save a draft before they can plan it. Saving the same
// draft twice returns the first recipe rather than making a second one — the model
// re-running "apply the plan" must not duplicate every variant in My Recipes.
func (s *chatSession) saveDraft(ctx context.Context, ref string, tags []string) (int, model.RecipeDocument, error) {
	ref = strings.TrimSpace(ref)
	doc, ok := s.drafts[ref]
	if !ok {
		return 0, model.RecipeDocument{}, errors.New("no such draft: " + ref)
	}
	info := s.draftInfo[ref]
	if info.savedID != 0 {
		return info.savedID, doc, nil
	}
	if len(tags) > 0 {
		doc.Tags = tags
	}
	var variantOf *int
	if info.variantOf != 0 {
		variantOf = &info.variantOf
	}
	created, err := storage.AddRecipeStructured(ctx, s.userID, doc, RenderMarkdown(doc), info.origin, nil, string(recipeModel), promptVersion, variantOf)
	if err != nil {
		return 0, model.RecipeDocument{}, errors.New("could not save that recipe")
	}
	if s.draftInfo == nil {
		s.draftInfo = map[string]draftInfo{}
	}
	info.savedID = created.ID
	s.draftInfo[ref] = info
	s.drafts[ref] = doc
	return created.ID, doc, nil
}
