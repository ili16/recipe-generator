package service

import (
	"bytes"
	"cmp"
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
	"github.com/jackc/pgx/v5"
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
	// Mutating gates the call behind the user's explicit approval instead of the
	// model's prose (BACKLOG.md 10.2): the loop emits event: approval_request and
	// stops, and the call only runs once the user says Apply.
	//
	// The gate is on *scope*, not on "does it write". A single row the user named in
	// their own message is its own confirmation — save_recipe ("save it") and
	// set_plan_day ("put the curry on Thursday") write silently, because asking again
	// is noise. What asks is the multi-day write, the destructive one, and the one
	// that changes every future turn: apply_week_plan, clear_plan_day, set_preferences.
	Mutating bool
	Run      toolFunc
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
		Description: "Create a new recipe from a description, a webpage URL, or a photo the user attached. Returns a draft_ref naming the unsaved draft; the recipe card is already on the user's screen, so do not call display_recipe for it. Use the ref with transform_recipe or save_recipe.",
		Params: obj(map[string]any{
			"description":  str("What the user asked for, e.g. 'a quick vegan pasta for two'. With from_message, only an extra instruction, or omit"),
			"from_message": map[string]any{"type": "boolean", "description": "true when the user's own message contains the recipe itself (ingredients and steps). The server passes their message on unchanged — do not copy it into description"},
			"url":          str("A webpage to extract the recipe from, if the user gave one"),
			"image_ref":    str("The ref from an [attached photo] marker in the user's message, e.g. 'image1'"),
			"language":     str("ISO 639-1 code for the output language; omit to infer"),
		}),
		Run: toolGenerateRecipe,
	},
	"transform_recipe": {
		Description: "Apply an instruction ('make it vegan', 'halve it') to a recipe. Returns a new draft_ref; the new card is shown automatically. target is a draft_ref from generate_recipe or a saved recipe id.",
		Params: obj(map[string]any{
			"target":      str("A draft_ref, or the id of a saved recipe"),
			"instruction": str("The change to make, in the user's own words"),
		}, "target", "instruction"),
		Run: toolTransformRecipe,
	},
	"translate_recipe": {
		Description: "Write a recipe out again in another language. The original is never touched: this returns a new draft, and saving it files the translation as a sibling of the saved recipe it came from. Use it when the user wants a recipe in a different language than it was written in; use transform_recipe when the dish itself should change. target is a draft_ref or a saved recipe id.",
		Params: obj(map[string]any{
			"target":   str("A draft_ref, or the id of a saved recipe"),
			"language": str("ISO 639-1 code to translate into, e.g. 'de'"),
		}, "target", "language"),
		Run: toolTranslateRecipe,
	},
	"review_cook_flow": {
		Description: "Re-plan how a recipe is cooked, without changing the recipe. Merges steps too small to be worth their own screen, labels each step as mise en place / cook / wait, and marks the steps that should run at the same time (chopping while the water boils). Ingredients, quantities and the dish itself are never touched. Use it when a recipe's steps are fiddly, out of order, or written as a list of one-line actions, and when the user asks how to actually cook something. target is a draft_ref or a saved recipe id.",
		Params:      obj(map[string]any{"target": str("A draft_ref, or the id of a saved recipe")}, "target"),
		Run:         toolReviewCookFlow,
	},
	"search_my_recipes": {
		Description: "Search the user's saved recipes by title text, tags, and/or ingredients they use. Returns id, title and tags, plus uses (the asked-for ingredients the recipe actually needs) and swaps (ingredients it asks for that one of yours could stand in for — penne for spaghetti). A recipe found only through swaps is not cookable as written: name the swap when you offer it.",
		Params: obj(map[string]any{
			"query":       str("Text to match against recipe titles"),
			"tags":        strList("Tag slugs a recipe must all carry"),
			"ingredients": strList("Ingredient names; a recipe matches if it uses at least one of them. Use this to cook something up before it goes off."),
		}),
		NeedsAuth: true,
		Run:       toolSearchMyRecipes,
	},
	"meal_history": {
		Description: "What the user has actually cooked lately, most recently first, with tags, their rating and their vote. Read it before proposing a week so you do not serve the same thing twice, and when the user asks what they have been eating. Defaults to the last 14 days.",
		Params:      obj(map[string]any{"days": integer("How far back to look, in days; defaults to 14")}),
		NeedsAuth:   true,
		Run:         toolMealHistory,
	},
	"get_recipe": {
		Description: "Load one of the user's saved recipes in full, including ingredients and steps.",
		Params:      obj(map[string]any{"id": integer("The saved recipe's id")}, "id"),
		NeedsAuth:   true,
		Run:         toolGetRecipe,
	},
	"similar_recipes": {
		Description: "Recipes in the user's library that are like one of their saved recipes, best match first. Matches on shared ingredients (a Zwiebel and an onion count as the same food), ingredient classes, cuisine and technique — not on wording. Use it for 'something like this' or 'what else do I have like the curry'.",
		Params:      obj(map[string]any{"id": integer("The saved recipe's id")}, "id"),
		NeedsAuth:   true,
		Run:         toolSimilarRecipes,
	},
	"similar_ingredients": {
		Description: "Foods related to an ingredient, in any language: its other spellings and the foods of the same class in the user's kitchen. Use it for 'what can I use instead of X' style questions before suggesting a swap.",
		Params:      obj(map[string]any{"name": str("The ingredient, e.g. 'Zwiebel' or 'butter'")}, "name"),
		NeedsAuth:   true,
		Run:         toolSimilarIngredients,
	},
	"neighbours": {
		Description: "Explore the user's recipe graph around one node: everything within depth hops (1-3). type is recipe, ingredient, cuisine, technique, tag or meal; node is a recipe id, an ingredient name or a slug. Use it to answer 'what do I cook with X' or 'what is my Italian food made of'.",
		Params: obj(map[string]any{
			"type":  str("recipe | ingredient | cuisine | technique | tag | meal"),
			"node":  str("A recipe id, ingredient name, or cuisine/technique/tag slug"),
			"depth": integer("Hops to follow, 1-3; defaults to 1"),
		}, "type", "node"),
		NeedsAuth: true,
		Run:       toolNeighbours,
	},
	"rename_recipe": {
		Description: "Rename one of the user's saved recipes. Use it when the user asks for a different name, or when a saved recipe carries a name that only made sense in the conversation that produced it (\"Half-Batch Cr\u00e8me Br\u00fbl\u00e9e\"). The new title names the dish, not the request: no serving counts, no source, no wording of the change that produced it.",
		Params: obj(map[string]any{
			"id":    integer("The saved recipe's id"),
			"title": str("The new title"),
		}, "id", "title"),
		NeedsAuth: true,
		Run:       toolRenameRecipe,
	},
	"offer_options": {
		Description: "Put tappable choices on the user's screen and end the turn: either several dish ideas, or one clarifying question with 2-4 short answers. Use it instead of listing ideas or asking questions in prose. For dish ideas give every option a detail line (main components, sauce, sides); for a question's answers omit detail.",
		Params: obj(map[string]any{
			"prompt": str("One short line shown above the choices: the question, or a lead-in like 'Five takes on gourmet duck'"),
			"options": map[string]any{
				"type":        "array",
				"description": "2-6 choices",
				"items": obj(map[string]any{
					"label":  str("Short dish name or answer"),
					"detail": str("One line describing the dish: key components, sauce, sides. Omit for plain answers"),
				}, "label"),
			},
		}, "prompt", "options"),
		Run: toolOfferOptions,
	},
	"display_recipe": {
		Description: "Show an existing recipe as a card — a saved recipe by id, or an earlier draft the user asked to see again. generate_recipe, transform_recipe, translate_recipe and review_cook_flow already show their own result; never call this for those. target is a draft_ref or a saved recipe id.",
		Params:      obj(map[string]any{"target": str("A draft_ref, or the id of a saved recipe")}, "target"),
		Run:         toolDisplayRecipe,
	},
	"save_recipe": {
		Description: "Save a draft to the user's account. Only call this when the user has actually asked to save. If the user already has a recipe that looks like this one, nothing is saved and the match is reported back as duplicate_of — tell the user what they already have and offer to save it as a variant of that recipe or as a separate recipe, then call again with confirm true (plus variant_of set to the existing id, if they chose variant).",
		Params: obj(map[string]any{
			"draft_ref":  str("The draft_ref to save"),
			"tags":       strList("Tag slugs; omit to keep the ones the draft already carries"),
			"confirm":    map[string]any{"type": "boolean", "description": "Set true to save even though a duplicate was reported"},
			"variant_of": integer("The id of the existing recipe this is a variant of, when the user chose that over a separate recipe"),
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
		Description: "Save a whole proposed plan to the user's meal plan in one atomic step: all days are written or none are. Call this once with every day of the plan. Days before today are already cooked and are skipped (reported as skipped_already_cooked). Days already filled with a different recipe are reported back instead of overwritten — show that list to the user and call again with confirm true once they agree. Carry each day's servings over from the proposal.",
		Params: obj(map[string]any{
			"days": map[string]any{
				"type":        "array",
				"description": "One entry per day to write",
				"items": obj(map[string]any{
					"date":        str("The day to fill, YYYY-MM-DD"),
					"recipe_id":   integer("A saved recipe's id"),
					"draft_ref":   str("A draft_ref (e.g. from plan_week's variant_drafts) to save first, then plan"),
					"servings":    integer("Portions wanted that day; omit for the recipe as written"),
					"meal_slot":   str("Which meal of the day: breakfast, lunch, dinner or snack. Omit for dinner. A day can hold one entry per slot, so list a lunch and a dinner as two entries."),
					"leftover_of": str("The YYYY-MM-DD day whose cooking this meal eats again — set it on every day that is leftovers of an earlier day's pot, and give that day the same recipe_id. Omit when the meal is cooked fresh. Clearing the cooking day clears its leftovers with it."),
				}, "date"),
			},
			"confirm": map[string]any{"type": "boolean", "description": "Set true to go ahead and replace days that are already planned"},
		}, "days"),
		NeedsAuth: true,
		Mutating:  true,
		Run:       toolApplyWeekPlan,
	},
	"set_plan_day": {
		Description: "Put one recipe on one day of the user's saved meal plan. Only call this when the user has asked for that one specific day to change; to apply a whole plan use apply_week_plan.",
		Params: obj(map[string]any{
			"date":        str("The day to fill, YYYY-MM-DD"),
			"recipe_id":   integer("A saved recipe's id"),
			"draft_ref":   str("A draft_ref to save first, then plan"),
			"servings":    integer("Portions wanted that day; omit for the recipe as written"),
			"meal_slot":   str("Which meal of the day: breakfast, lunch, dinner or snack. Omit for dinner. This replaces only that slot, leaving the day's other meals alone."),
			"leftover_of": str("The YYYY-MM-DD day whose cooking this meal eats again, with the same recipe_id. Omit when it is cooked fresh."),
		}, "date"),
		NeedsAuth: true,
		Run:       toolSetPlanDay,
	},
	"clear_plan_day": {
		Description: "Remove what is planned on one day of the user's saved meal plan. Pass meal_slot to clear only that meal; omit it to clear the whole day, every meal included.",
		Params: obj(map[string]any{
			"date":      str("The day to clear, YYYY-MM-DD"),
			"meal_slot": str("Clear only this meal: breakfast, lunch, dinner or snack. Omit to clear the entire day."),
		}, "date"),
		NeedsAuth: true,
		Mutating:  true,
		Run:       toolClearPlanDay,
	},
	"get_pantry": {
		Description: "Read what the user has in the house: name, amount, where it is kept, when it expires, whether it is a staple they always have, whether they have marked it running low (a staple running low is back on the grocery list), and its substitution class where one is known (penne and spaghetti are both pasta_dry). Use it to answer \"what can I make tonight\" and \"what is about to go off\" without making them go and look. A shared class means a swap you may offer, never one you may assume.",
		Params:      obj(map[string]any{}),
		NeedsAuth:   true,
		Run:         toolGetPantry,
	},
	"scan_pantry": {
		Description: "Read a photo the user attached — a fridge, freezer or cupboard shelf, or a supermarket receipt — into a list of foods with amounts. It changes nothing: show the user what came back, then pass the lines they want kept to add_pantry_items.",
		Params: obj(map[string]any{
			"image_ref": str("The ref from an [attached photo] marker in the user's message, e.g. 'image1'"),
		}, "image_ref"),
		NeedsAuth: true,
		Run:       toolScanPantry,
	},
	"add_pantry_items": {
		Description: "Put foods into the user's pantry. Re-adding something they already have replaces that line's amount rather than making a second one. Use it for what the user tells you they have ('I bought two kilos of flour') and for the lines they keep from a scan_pantry photo.",
		Params: obj(map[string]any{
			"items": map[string]any{
				"type":        "array",
				"description": "The foods to put in the pantry",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":       str("The generic name of the food, e.g. 'milk' — never a brand"),
						"quantity":   map[string]any{"type": "number", "description": "How much, in the unit below. Omit if unknown."},
						"unit":       str("g, kg, ml, l, or piece. Omit if unknown."),
						"category":   map[string]any{"type": "string", "enum": model.PantryCategories, "description": "Where it is kept"},
						"expires_on": str("YYYY-MM-DD, only if actually known"),
					},
					"required": []string{"name"},
				},
			},
		}, "items"),
		NeedsAuth: true,
		Mutating:  true,
		Run:       toolAddPantryItems,
	},
	"mark_meal_cooked": {
		Description: "Record that a meal on the user's plan was cooked and eaten. It leaves the week's grocery list and its ingredients come out of the pantry. Pass meal_slot when the day holds more than one meal. Optionally rate the recipe 1-5.",
		Params: obj(map[string]any{
			"date":      str("The day that was cooked, YYYY-MM-DD"),
			"meal_slot": str("Which meal: breakfast, lunch, dinner or snack. Omit when the day holds only one."),
			"rating":    integer("How the cook went, 1-5. Omit unless the user said."),
		}, "date"),
		NeedsAuth: true,
		Mutating:  true,
		Run:       toolMarkMealCooked,
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
		Mutating:  true,
		Run:       toolSetPreferences,
	},
	"note_preference": {
		Description: "Record one durable like or dislike the user let slip in passing (\"ugh, not mushrooms again\") without interrupting them. One value at a time. Use remove=true to take one back. For anything else — skill level, household size, week scheduling — use set_preferences.",
		Params: obj(map[string]any{
			"field":  str("Which list: disliked_ingredients or dietary_prefs"),
			"value":  str("The single ingredient or diet, e.g. 'mushrooms' or 'vegetarian'"),
			"remove": map[string]any{"type": "boolean", "description": "Take this value back off the list instead of adding it"},
		}, "field", "value"),
		NeedsAuth: true,
		Run:       toolNotePreference,
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
func toolDefinitions(only ...string) []openai.ChatCompletionToolParam {
	names := make([]string, 0, len(chatTools))
	if len(only) > 0 {
		names = append(names, only...)
	} else {
		for name := range chatTools {
			names = append(names, name)
		}
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
		FromMessage bool   `json:"from_message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	var source string
	if a.FromMessage {
		if source = lastUserText(s.messages); source == "" {
			return nil, nil, errors.New("the user's message holds no text; ask them to paste the recipe")
		}
	}
	if a.Description == "" && a.URL == "" && a.ImageRef == "" && source == "" {
		return nil, nil, errors.New("give a description, a url, an image_ref or from_message")
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
		SourceText:        source,
		Image:             image,
		PreferredLanguage: a.Language,
	})
	if err != nil {
		// A source we can read nothing from, and one we read fine that held no recipe,
		// both say so in words the user can act on. Neither is a card: nothing was
		// produced to show.
		if errors.Is(err, utils.ErrUnreadableSource) {
			return nil, nil, utils.ErrUnreadableSource
		}
		if errors.Is(err, ErrNoRecipeInSource) {
			return nil, nil, ErrNoRecipeInSource
		}
		return nil, nil, errors.New("could not produce a recipe from that")
	}
	// Which of the three inputs the user actually gave is the provenance, and this is
	// the only place that still knows it.
	origin := &model.GenerationOrigin{Prompt: cmp.Or(source, a.Description), SourceType: "text", SourceURL: a.URL}
	switch {
	case a.URL != "":
		origin.SourceType = "url"
	case a.ImageRef != "":
		origin.SourceType = "image"
	case source != "":
		origin.SourceType = "import"
	}
	return s.addDraft(doc, 0, origin, "")
}

// lastUserText is the latest user message as the user wrote it: the attachment markers
// HandleChat appends ("[attached link] …", "[attached photo] …") are ours, not theirs.
func lastUserText(messages []model.ConversationMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		var keep []string
		for _, line := range strings.Split(messages[i].Content, "\n") {
			if !strings.HasPrefix(line, "[attached link] ") && !strings.HasPrefix(line, "[attached photo] ") {
				keep = append(keep, line)
			}
		}
		return strings.TrimSpace(strings.Join(keep, "\n"))
	}
	return ""
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
	return s.addDraft(*result.Recipe, 0, s.draftInfo[strings.TrimSpace(a.Target)].origin, a.Target)
}

func toolTranslateRecipe(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Target   string `json:"target"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	lang := strings.ToLower(strings.TrimSpace(a.Language))
	if lang == "" {
		return nil, nil, errors.New("give the language to translate into, as an ISO 639-1 code")
	}
	doc, err := s.resolveRecipe(ctx, a.Target)
	if err != nil {
		return nil, nil, err
	}
	if strings.EqualFold(doc.Language, lang) {
		return nil, nil, errors.New("that recipe is already written in that language")
	}

	messages, err := buildRefineMessages(model.GenerationOrigin{Prompt: doc.Title, SourceType: "text"}, doc, nil,
		"Translate this recipe into the language with ISO 639-1 code '"+lang+"'. Translate every text field: title, summary, section names, ingredient names, steps and notes. Change nothing else \u2014 same ingredients, same quantities, same steps, same dish.")
	if err != nil {
		return nil, nil, errors.New("could not prepare that translation")
	}
	// buildRefineMessages takes the output language from the document, which here is the
	// language being translated *out of*. The target language is the whole point, so the
	// system message is rebuilt for it.
	messages[0] = openai.SystemMessage(buildSystemPrompt(lang) + refineJudgmentInstructions)

	result, err := generateRefineFromMessages(ctx, messages)
	if err != nil || result.Status != "applied" || result.Recipe == nil {
		return nil, nil, errors.New("could not translate that recipe")
	}
	out := *result.Recipe
	// The prompt asks for it, but a translation whose language field still says "en" is a
	// sibling nothing can tell apart from its parent, so it is stamped here as well.
	out.Language = lang

	// A translation of a *saved* recipe is a sibling of it, never a replacement: parking
	// it as a variant means saving links it with variant_of_recipe_id and leaves the
	// original where it was (BACKLOG.md 14.2). It also settles the duplicate question,
	// which a translation would otherwise fail on its way past a same-title parent.
	return s.addDraft(out, savedRecipeID(a.Target), s.draftInfo[strings.TrimSpace(a.Target)].origin, a.Target)
}

func toolReviewCookFlow(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	doc, err := s.resolveRecipe(ctx, a.Target)
	if err != nil {
		return nil, nil, err
	}
	if len(doc.Steps) == 0 {
		return nil, nil, errors.New("that recipe has no steps to re-plan")
	}
	out, err := ReviewCookFlow(ctx, doc)
	if err != nil {
		return nil, nil, errors.New("could not re-plan the cooking flow for that recipe")
	}
	// The result is a new draft, not a write: like every other proposal in this catalog it
	// is the user's to save. A saved-id target has no provenance to inherit.
	return s.addDraft(out, 0, s.draftInfo[strings.TrimSpace(a.Target)].origin, a.Target)
}

func toolSearchMyRecipes(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Query       string   `json:"query"`
		Tags        []string `json:"tags"`
		Ingredients []string `json:"ingredients"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	recipes, err := storage.GetRecipes(s.userID)
	if err != nil {
		return nil, nil, errors.New("could not read your saved recipes")
	}

	// Ingredients are the one filter GetRecipes cannot answer from the list payload — it
	// carries no ingredient names — so it is a second, indexed lookup (BACKLOG 7.4).
	var usedBy map[int][]model.IngredientHit
	if len(a.Ingredients) > 0 {
		usedBy, err = storage.RecipesUsingIngredients(ctx, s.userID, a.Ingredients)
		if err != nil {
			return nil, nil, errors.New("could not read your saved recipes")
		}
	}

	type hit struct {
		ID    int      `json:"id"`
		Title string   `json:"title"`
		Tags  []string `json:"tags"`
		// Uses names which of the asked-for ingredients this recipe needs, so the model can
		// say "this one uses the spinach and the feta" without loading the whole recipe.
		Uses []string `json:"uses,omitempty"`
		// Swaps are the ingredients this recipe asks for that one of the asked-for names
		// could stand in for — same class, different food (BACKLOG 17.4b). A recipe that
		// only matched through these is only cookable if the user accepts the swap, so
		// the model must name it rather than present the recipe as ready to cook.
		Swaps []swapHit `json:"swaps,omitempty"`
		// rank orders the hits, it is not part of the tool's answer.
		rank int
	}
	hits := []hit{}
	query := strings.ToLower(strings.TrimSpace(a.Query))
	for _, r := range recipes {
		rank := matchRank(r, query)
		if rank == 0 {
			continue
		}
		if !hasAllTags(r.Tags, a.Tags) {
			continue
		}
		if usedBy != nil && len(usedBy[r.ID]) == 0 {
			continue
		}
		uses, swaps := splitHits(usedBy[r.ID])
		hits = append(hits, hit{ID: r.ID, Title: r.Recipename, Tags: r.Tags, Uses: uses, Swaps: swaps, rank: rank})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].rank > hits[j].rank })
	return map[string]any{"count": len(hits), "recipes": hits}, nil, nil
}

// matchRank scores a free-text query against a recipe the library already holds in
// memory — GetRecipes carries the rendered document, so searching the body costs no
// extra query (BACKLOG 18.4).
//
// 2 is the name or a tag: what the user almost certainly meant. 1 is somewhere in the
// document — "the one with the miso" is a steps question, and matching only the title
// answered it with nothing — but a staple in step four is weaker evidence than a word
// in the name, so body hits sort last instead of drowning the real ones. 0 is no match.
func matchRank(r model.Recipe, query string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 2
	}
	if strings.Contains(strings.ToLower(r.Recipename), query) {
		return 2
	}
	for _, tag := range r.Tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return 2
		}
	}
	if strings.Contains(strings.ToLower(r.Recipe), query) {
		return 1
	}
	return 0
}

// swapHit is one substitution the search assumed: the recipe wants Needs, the user
// offered Have. Kept as a pair so the model can say "with your penne for its spaghetti"
// (BACKLOG 17.4b).
type swapHit struct {
	Needs string `json:"needs"`
	Have  string `json:"have"`
}

// splitHits separates what a recipe actually uses from what it would need a swap for.
// A recipe can appear in both lists — it uses your spinach and would take your penne
// for its spaghetti — and the two must not be added together.
func splitHits(hits []model.IngredientHit) ([]string, []swapHit) {
	var uses []string
	var swaps []swapHit
	for _, h := range hits {
		if h.SwapFor == "" {
			uses = append(uses, h.Item)
			continue
		}
		swaps = append(swaps, swapHit{Needs: h.Item, Have: h.SwapFor})
	}
	return uses, swaps
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

// cookedEntry is one line of meal_history: enough for the model to say "you had that on
// Tuesday and rated it 2" without loading a single recipe.
type cookedEntry struct {
	ID       int      `json:"id"`
	Title    string   `json:"title"`
	Tags     []string `json:"tags,omitempty"`
	CookedOn string   `json:"cooked_on"`
	DaysAgo  int      `json:"days_ago"`
	Rating   *int16   `json:"rating,omitempty"`
	Vote     *int16   `json:"vote,omitempty"`
}

// recentlyCooked picks the recipes cooked within the last days days, most recent first.
// recipes.last_cooked_at holds only the most recent cook, which is exactly the question
// being asked ("have I had this lately?") — no separate history table earns its keep here.
func recentlyCooked(recipes []model.Recipe, now time.Time, days int) []cookedEntry {
	out := []cookedEntry{}
	for _, r := range recipes {
		ago, ok := daysSinceCooked(r, now)
		if !ok || ago > days {
			continue
		}
		out = append(out, cookedEntry{
			ID: r.ID, Title: r.Recipename, Tags: r.Tags,
			CookedOn: r.LastCookedAt.UTC().Format("2006-01-02"), DaysAgo: ago,
			Rating: r.CookedRating, Vote: r.MyVote,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DaysAgo < out[j].DaysAgo })
	return out
}

// toolMealHistory is BACKLOG 11.2: without it the agent cycles the same handful of
// recipes, because nothing in the thread remembers last Tuesday.
func toolMealHistory(_ context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Days int `json:"days"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	if a.Days <= 0 {
		a.Days = recentCookWindowDays
	}
	recipes, err := storage.GetRecipes(s.userID)
	if err != nil {
		return nil, nil, errors.New("could not read your saved recipes")
	}
	cooked := recentlyCooked(recipes, time.Now(), a.Days)
	return map[string]any{"days": a.Days, "count": len(cooked), "cooked": cooked}, nil, nil
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

func toolSimilarRecipes(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	out, err := storage.SimilarRecipes(ctx, s.userID, a.ID, 10)
	if err != nil {
		return nil, nil, errors.New("could not search your library")
	}
	return map[string]any{"recipes": out}, nil, nil
}

func toolSimilarIngredients(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Name == "" {
		return nil, nil, errors.New("could not read the arguments")
	}
	canonical, related, err := storage.SimilarIngredients(ctx, s.userID, a.Name)
	if err != nil {
		return nil, nil, errors.New("could not search your library")
	}
	return map[string]any{"canonical": canonical, "related": related}, nil, nil
}

func toolNeighbours(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Type  string `json:"type"`
		Node  string `json:"node"`
		Depth int    `json:"depth"`
	}
	if err := json.Unmarshal(args, &a); err != nil || a.Type == "" || a.Node == "" {
		return nil, nil, errors.New("could not read the arguments")
	}
	edges, err := storage.Neighbours(ctx, s.userID, a.Type, a.Node, a.Depth)
	if err != nil {
		return nil, nil, errors.New("could not search your library")
	}
	return map[string]any{"edges": edges}, nil, nil
}

func toolRenameRecipe(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	title := strings.TrimSpace(a.Title)
	if title == "" {
		return nil, nil, errors.New("give the new title")
	}
	// Scoped by user_id, so another user's id is simply not found.
	recipe, err := storage.GetRecipeByID(ctx, s.userID, a.ID)
	if err != nil {
		return nil, nil, errors.New("no such recipe")
	}
	if recipe.Structured == nil {
		return nil, nil, errors.New("that recipe has no structured document to rename")
	}
	if recipe.Structured.Title == title {
		return map[string]any{"id": a.ID, "title": title, "unchanged": true}, nil, nil
	}

	// The whole document goes back through the normal update path rather than a bespoke
	// title-only UPDATE, so the rename lands in recipe_versions and the timeline like
	// every other edit. manuallyEdited is true: the user asked for this name, no model
	// regenerated the recipe.
	doc := *recipe.Structured
	doc.Title = title
	updated, err := storage.UpdateRecipeStructured(ctx, s.userID, a.ID, doc, RenderMarkdown(doc), true, "renamed to "+title)
	if err != nil {
		return nil, nil, errors.New("could not rename that recipe")
	}
	return map[string]any{"id": a.ID, "title": title},
		&artifact{Kind: "recipe", Data: map[string]any{"recipe_id": a.ID, "document": updated.Structured}}, nil
}

func toolSaveRecipe(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		DraftRef  string   `json:"draft_ref"`
		Tags      []string `json:"tags"`
		Confirm   bool     `json:"confirm"`
		VariantOf int      `json:"variant_of"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}

	// BACKLOG.md 8.3: report a look-alike back once instead of saving, so the model can
	// offer "a variant of that one" or "a separate recipe". The second call carries
	// confirm, so a save is deferred, never refused. Only this tool checks — the plan
	// tools save drafts through saveDraft too, and a week plan must not stop to ask.
	if !a.Confirm && a.VariantOf == 0 {
		if dup := s.duplicateOf(ctx, a.DraftRef); dup != nil {
			return map[string]any{"saved": false, "duplicate_of": dup}, nil, nil
		}
	}
	if a.VariantOf > 0 {
		if err := s.markVariantOf(ctx, a.DraftRef, a.VariantOf); err != nil {
			return nil, nil, err
		}
	}

	id, doc, err := s.saveDraft(ctx, a.DraftRef, a.Tags)
	if err != nil {
		return nil, nil, err
	}
	// Re-emit the draft's card carrying its new recipe_id. The client folds artifacts by
	// draft_ref, so this replaces the "unsaved draft" card rather than adding a second —
	// without it a recipe the model just saved keeps offering to save itself.
	return map[string]any{"recipe_id": id, "title": doc.Title},
		&artifact{Kind: "recipe", Data: map[string]any{
			"draft_ref": strings.TrimSpace(a.DraftRef),
			"recipe_id": id,
			"document":  doc,
		}}, nil
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
	// Servings is portions wanted that day (BACKLOG 6.2); 0 (omitted) means "as the
	// recipe is written" and leaves the column NULL.
	Servings int `json:"servings"`
	// MealSlot is which meal of the day this fills (BACKLOG 6.10); "" means dinner.
	MealSlot string `json:"meal_slot"`
	// LeftoverOf names the day whose pot this meal eats again (BACKLOG 9.11). A date
	// rather than an item id: the model has the dates in front of it and would have to
	// look an id up. "" means cooked fresh.
	LeftoverOf string `json:"leftover_of"`
}

// resolvePlanDays turns the model's day arguments into storage assignments, saving any
// named drafts on the way (idempotently — see saveDraft). Drafts are saved before the
// apply transaction opens, so a failed apply can leave a recipe saved but unplanned;
// re-running reuses it rather than duplicating it.
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
		slot := d.MealSlot
		if slot == "" {
			slot = storage.DefaultMealSlot
		}
		if !storage.ValidMealSlot(slot) {
			return nil, nil, errors.New("meal_slot must be one of breakfast, lunch, dinner, snack")
		}
		// Keyed by (date, slot): one call may plan Thursday's lunch *and* its dinner,
		// but not the same slot twice (BACKLOG 6.10).
		if seen[d.Date+" "+slot] {
			return nil, nil, errors.New("the same day and meal is listed twice: " + d.Date + " " + slot)
		}
		seen[d.Date+" "+slot] = true
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
		day := storage.PlannedDay{RecipeID: recipeID, PlannedOn: plannedOn, MealSlot: slot}
		if d.LeftoverOf != "" {
			src, err := time.Parse(time.DateOnly, d.LeftoverOf)
			if err != nil {
				return nil, nil, errors.New("leftover_of must be a date like 2026-09-14")
			}
			if !src.Before(plannedOn) {
				return nil, nil, errors.New("leftover_of must be an earlier day than the day eating the leftovers")
			}
			day.LeftoverOf = &src
		}
		if d.Servings > 0 {
			servings := d.Servings
			day.Servings = &servings
		}
		out = append(out, day)
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
	// Keyed by (date, slot), the same key the apply upserts on: a day's breakfast is not
	// a conflict for the dinner we are about to write (BACKLOG 6.4).
	bySlot := map[string]model.MealPlanItem{}
	for _, item := range existing {
		bySlot[item.PlannedOn+" "+item.MealSlot] = item
	}

	conflicts := []map[string]any{}
	for _, d := range days {
		slot := d.MealSlot
		if slot == "" {
			slot = storage.DefaultMealSlot
		}
		item, ok := bySlot[d.PlannedOn.Format(time.DateOnly)+" "+slot]
		if ok && item.RecipeID != d.RecipeID {
			conflicts = append(conflicts, map[string]any{"date": item.PlannedOn, "meal_slot": item.MealSlot, "current_recipe": item.RecipeTitle})
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

func toolGetPantry(ctx context.Context, s *chatSession, _ json.RawMessage) (any, *artifact, error) {
	items, err := storage.ListPantryItems(ctx, s.userID)
	if err != nil {
		return nil, nil, errors.New("could not read your pantry")
	}
	return map[string]any{"items": items}, nil, nil
}

// toolScanPantry turns an attached photo into a list of pantry lines. It is deliberately
// NOT Mutating: it writes nothing, so there is nothing to approve. The write is
// add_pantry_items, and that one is gated — which is also what makes the approval card
// show the actual parsed list, since by then the items are the tool call's own arguments
// and the user can have the model drop the ones they do not want.
func toolScanPantry(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		ImageRef string `json:"image_ref"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	data, ok := s.images[a.ImageRef]
	if !ok {
		return nil, nil, errors.New("that photo isn't attached to this message; ask the user to send it again")
	}
	items, err := ScanPantryImage(ctx, data)
	if err != nil {
		return nil, nil, errors.New("could not read that photo")
	}
	return map[string]any{"items": items}, nil, nil
}

// toolAddPantryItems writes the lines the user agreed to. Every row goes through
// UpsertPantryItem, so a name and unit are normalised exactly as the pantry screen
// normalises them and a food they already hold keeps one line — and then through the
// same background alias/grams learning a saved recipe triggers, which is what makes a
// scanned "Zwiebeln" match a recipe's "onion" and a 500 g bag count against 300 g of it.
func toolAddPantryItems(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Items []model.PantryItem `json:"items"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	if len(a.Items) == 0 {
		return nil, nil, errors.New("give at least one item")
	}
	if len(a.Items) > maxScannedItems {
		return nil, nil, fmt.Errorf("that is more than %d items; add them in smaller batches", maxScannedItems)
	}

	added := []model.PantryItem{}
	skipped := []string{}
	for _, in := range a.Items {
		item, err := storage.UpsertPantryItem(ctx, s.userID, in)
		if err != nil {
			// A single unreadable line (a blank name, an unparseable date) is not a
			// reason to lose the rest of the shelf — it comes back named so the model
			// can say which one it could not take.
			if errors.Is(err, storage.ErrEmptyPantryItemName) || errors.Is(err, storage.ErrInvalidExpiry) {
				skipped = append(skipped, in.Name)
				continue
			}
			return nil, nil, errors.New("could not write to your pantry")
		}
		added = append(added, item)
	}

	LearnPantryItems(added...)

	return map[string]any{"added": added, "skipped": skipped}, nil, nil
}

// toolMarkMealCooked is the agent's half of the planner's "mark as cooked": same storage
// call, same transaction, same refusal to guess at an amount it cannot convert. The
// applied/skipped lists come back so the model can say what it did to the pantry —
// having the assistant quietly empty someone's fridge is the failure mode here.
func toolMarkMealCooked(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	a, item, err := resolveCookedMeal(ctx, s, args)
	if err != nil {
		return nil, nil, err
	}
	if a.Rating != nil && (*a.Rating < 1 || *a.Rating > 5) {
		return nil, nil, errors.New("a rating is 1 to 5")
	}

	applied, skipped, err := storage.MarkMealCooked(ctx, s.userID, item.ID, a.Rating)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errors.New("that meal is already marked as cooked")
		}
		return nil, nil, errors.New("could not mark that meal as cooked")
	}
	if applied == nil {
		applied = []model.PantryChange{}
	}
	if skipped == nil {
		skipped = []string{}
	}
	return map[string]any{
		"date": a.Date, "meal_slot": item.MealSlot, "recipe_title": item.RecipeTitle,
		"pantry_used": applied, "pantry_unchanged": skipped,
	}, nil, nil
}

// cookedMealArgs is mark_meal_cooked's argument shape, shared with its approval preview.
type cookedMealArgs struct {
	Date     string `json:"date"`
	MealSlot string `json:"meal_slot"`
	Rating   *int16 `json:"rating"`
}

// resolveCookedMeal turns "Monday's dinner" into the one plan item it names. A day
// holding several meals with no meal_slot given is an error rather than a guess: marking
// the wrong meal cooked deducts the wrong things from the pantry.
//
// Deliberately allows a day in the past — unlike clear_plan_day, which refuses one. "I
// cooked that on Sunday" is the normal way to say this, and the whole point of the tool.
func resolveCookedMeal(ctx context.Context, s *chatSession, args json.RawMessage) (cookedMealArgs, model.MealPlanItem, error) {
	var a cookedMealArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return a, model.MealPlanItem{}, errors.New("could not read the arguments")
	}
	if a.MealSlot != "" && !storage.ValidMealSlot(a.MealSlot) {
		return a, model.MealPlanItem{}, errors.New("meal_slot must be one of breakfast, lunch, dinner, snack")
	}
	day, err := time.Parse(time.DateOnly, a.Date)
	if err != nil {
		return a, model.MealPlanItem{}, errors.New("date must be a date like 2026-09-14")
	}

	items, err := storage.GetMealPlanRange(ctx, s.userID, day, day)
	if err != nil {
		return a, model.MealPlanItem{}, errors.New("could not read your plan")
	}
	matches := []model.MealPlanItem{}
	for _, item := range items {
		if a.MealSlot != "" && item.MealSlot != a.MealSlot {
			continue
		}
		if item.CookedAt != nil {
			continue
		}
		// A leftover is not cooked; the pot it came from was. Marking one would deduct a
		// second set of ingredients for a meal that used none.
		if item.SourceItemID != nil {
			continue
		}
		matches = append(matches, item)
	}
	switch len(matches) {
	case 0:
		return a, model.MealPlanItem{}, errors.New("nothing is planned there that still needs marking as cooked")
	case 1:
		return a, matches[0], nil
	default:
		return a, model.MealPlanItem{}, errors.New("that day holds more than one meal — say which one (breakfast, lunch, dinner or snack)")
	}
}

func toolClearPlanDay(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Date string `json:"date"`
		// MealSlot narrows the clear to one meal; "" clears the whole day (BACKLOG 6.10 —
		// without it, "clear Thursday's lunch" deletes that day's dinner too).
		MealSlot string `json:"meal_slot"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	if a.MealSlot != "" && !storage.ValidMealSlot(a.MealSlot) {
		return nil, nil, errors.New("meal_slot must be one of breakfast, lunch, dinner, snack")
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
	removed := 0
	for _, item := range items {
		if a.MealSlot != "" && item.MealSlot != a.MealSlot {
			continue
		}
		if err := storage.DeleteMealPlanItem(ctx, s.userID, item.ID); err != nil {
			return nil, nil, errors.New("could not clear that day")
		}
		removed++
	}
	return map[string]any{"date": a.Date, "removed": removed}, nil, nil
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
	// Same bound the DB CHECK and PATCH /preferences enforce — said here so the model
	// gets "20 is the most" back rather than an opaque save failure.
	if merged.HouseholdSize != nil && (*merged.HouseholdSize < 1 || *merged.HouseholdSize > 20) {
		return nil, nil, errors.New("household_size must be between 1 and 20")
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

// notablePreferenceFields are the two list fields note_preference may touch. Everything
// else on UserPreferences (skill level, household size, week scheduling) stays on
// set_preferences, which asks first — those are settings, not things overheard.
var notablePreferenceFields = map[string]func(*model.UserPreferences) *[]string{
	"disliked_ingredients": func(p *model.UserPreferences) *[]string { return &p.DislikedIngredients },
	"dietary_prefs":        func(p *model.UserPreferences) *[]string { return &p.DietaryPrefs },
}

// applyNotedPreference adds or removes one value from one list, case-insensitively, and
// reports whether anything actually moved — so the agent can say "already noted" rather
// than claim a change it did not make.
func applyNotedPreference(current model.UserPreferences, field, value string, remove bool) (model.UserPreferences, []string, bool, error) {
	pick, ok := notablePreferenceFields[strings.ToLower(strings.TrimSpace(field))]
	if !ok {
		return current, nil, false, errors.New("field must be disliked_ingredients or dietary_prefs")
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return current, nil, false, errors.New("give a value")
	}
	list := pick(&current)
	changed := false
	if remove {
		kept := make([]string, 0, len(*list))
		for _, v := range *list {
			if strings.EqualFold(v, value) {
				changed = true
				continue
			}
			kept = append(kept, v)
		}
		*list = kept
	} else {
		for _, v := range *list {
			if strings.EqualFold(v, value) {
				return current, *list, false, nil
			}
		}
		*list = append(*list, value)
		changed = true
	}
	return current, *list, changed, nil
}

// toolNotePreference is BACKLOG 11.1: a dislike said in passing is the most valuable
// signal in the product and it used to die with the thread. Deliberately not Mutating —
// one additive value is cheap to undo (the same tool, remove=true) and an approval card
// per overheard "not mushrooms again" would be worse than the silence it replaces.
func toolNotePreference(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Field  string `json:"field"`
		Value  string `json:"value"`
		Remove bool   `json:"remove"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("give a field and a value")
	}
	current, err := storage.GetPreferences(ctx, s.userID)
	if err != nil {
		return nil, nil, errors.New("could not read your preferences")
	}
	merged, list, changed, err := applyNotedPreference(current, a.Field, a.Value, a.Remove)
	if err != nil {
		return nil, nil, err
	}
	if changed {
		if err := storage.UpdatePreferences(ctx, s.userID, merged); err != nil {
			return nil, nil, errors.New("could not save your preferences")
		}
	}
	return map[string]any{"field": a.Field, "value": a.Value, "removed": a.Remove, "changed": changed, "now": list}, nil, nil
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
		if errors.Is(err, utils.ErrUnreadableSource) {
			return nil, nil, utils.ErrUnreadableSource
		}
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
// compact summary the model reads, plus the card — so a generation is on the user's
// screen the moment it exists, without a second model round to call display_recipe.
// Failures that mean "there is no recipe" (an unreadable source, ErrNoRecipeInSource)
// return before reaching here, which is what keeps an empty card from rendering.
//
// variantOf is the saved recipe this document is a twist or a translation of, or 0; see
// parkDraft. derivedFrom is the draft_ref or `recipe:<id>` this document was made *out of* — a
// transform or a flow review — and empty for a fresh generation. It is the whole of the
// server's part in BACKLOG 10.3: the client already holds the before-card, so naming it
// is enough for the card to show what changed rather than a silent replacement. No diff
// is computed here and no document is sent twice.
func (s *chatSession) addDraft(doc model.RecipeDocument, variantOf int, origin *model.GenerationOrigin, derivedFrom string) (any, *artifact, error) {
	ref := s.parkDraft(doc, variantOf, origin)
	if derivedFrom = strings.TrimSpace(derivedFrom); derivedFrom != "" {
		info := s.draftInfo[ref]
		info.derivedFrom = derivedFrom
		s.draftInfo[ref] = info
	}

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
		"shown":         true,
	}, recipeCard(s, ref, doc), nil
}

// toolOfferOptions renders option chips and ends the turn: the next move is the user's.
func toolOfferOptions(_ context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	type option struct {
		Label  string `json:"label"`
		Detail string `json:"detail,omitempty"`
	}
	var a struct {
		Prompt  string   `json:"prompt"`
		Options []option `json:"options"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	var items []option
	for _, o := range a.Options {
		if o.Label = strings.TrimSpace(o.Label); o.Label != "" {
			o.Detail = strings.TrimSpace(o.Detail)
			items = append(items, o)
		}
	}
	if len(items) < 2 {
		return nil, nil, errors.New("give at least two options")
	}
	if len(items) > 6 {
		items = items[:6]
	}
	s.endTurn = true
	return map[string]any{"shown": true, "count": len(items)}, &artifact{Kind: "options", Data: map[string]any{"prompt": strings.TrimSpace(a.Prompt), "items": items}}, nil
}

// recipeCard is the recipe artifact for a draft_ref or a saved recipe, shared by addDraft
// and display_recipe.
func recipeCard(s *chatSession, ref string, doc model.RecipeDocument) *artifact {
	data := map[string]any{"document": doc}
	if _, isDraft := s.drafts[ref]; isDraft {
		info := s.draftInfo[ref]
		data["draft_ref"] = ref
		if info.derivedFrom != "" {
			data["derived_from"] = info.derivedFrom
		}
		// A draft already saved shows as saved, not as one more thing to save.
		if info.savedID != 0 {
			data["recipe_id"] = info.savedID
		}
	} else {
		data["recipe_id"] = savedRecipeID(ref)
	}
	return &artifact{Kind: "recipe", Data: data}
}

// toolDisplayRecipe is the only way a recipe reaches the user's screen. It renders no
// new document: it takes one the session already holds (a draft_ref) or one the user
// already owns (a saved id) and emits the card for it.
func toolDisplayRecipe(ctx context.Context, s *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Target string `json:"target"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	ref := strings.TrimSpace(a.Target)
	doc, err := s.resolveRecipe(ctx, ref)
	if err != nil {
		return nil, nil, err
	}

	return map[string]any{"displayed": ref, "title": doc.Title}, recipeCard(s, ref, doc), nil
}

// savedRecipeID reads the saved recipe id out of a target ref ("recipe:12" or "12"),
// or 0 when the ref names a draft instead.
func savedRecipeID(ref string) int {
	id, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(ref), "recipe:"))
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// resolveRecipe turns a draft_ref or a saved recipe id into a document. A saved id is
// looked up scoped to this session's user, so another user's id resolves to not-found.
func (s *chatSession) resolveRecipe(ctx context.Context, ref string) (model.RecipeDocument, error) {
	ref = strings.TrimSpace(ref)
	if doc, ok := s.drafts[ref]; ok {
		return doc, nil
	}
	id := savedRecipeID(ref)
	if id == 0 {
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

// duplicateOf reports an existing recipe of this user's that the named draft looks like a
// repeat of — same source URL, or the same title (BACKLOG.md 8.3). A draft that has
// already been saved, or one already marked a variant, is never a duplicate of anything:
// the question has been settled for it. nil means "go ahead and save".
func (s *chatSession) duplicateOf(ctx context.Context, ref string) *storage.DuplicateMatch {
	ref = strings.TrimSpace(ref)
	doc, ok := s.drafts[ref]
	if !ok {
		return nil // saveDraft reports the bad ref
	}
	info := s.draftInfo[ref]
	if info.savedID != 0 || info.variantOf != 0 {
		return nil
	}
	var sourceURL string
	if info.origin != nil {
		sourceURL = info.origin.SourceURL
	}
	dup, err := storage.FindDuplicate(ctx, s.userID, doc.Title, sourceURL)
	if err != nil {
		return nil
	}
	return dup
}

// markVariantOf records that the draft is a twist on an existing recipe, so saveDraft
// writes variant_of_recipe_id. The parent is looked up scoped to this session's user, so
// an id the model invented (or another user's) is not-found rather than a foreign link.
func (s *chatSession) markVariantOf(ctx context.Context, ref string, parentID int) error {
	ref = strings.TrimSpace(ref)
	if _, ok := s.drafts[ref]; !ok {
		return errors.New("no such draft: " + ref)
	}
	if _, err := storage.GetRecipeByID(ctx, s.userID, parentID); err != nil {
		return errors.New("no such recipe")
	}
	if s.draftInfo == nil {
		s.draftInfo = map[string]draftInfo{}
	}
	info := s.draftInfo[ref]
	info.variantOf = parentID
	s.draftInfo[ref] = info
	return nil
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
	LearnGrams(doc.Ingredients)
	LearnAliases(doc.Ingredients)
	if s.draftInfo == nil {
		s.draftInfo = map[string]draftInfo{}
	}
	telemetry.RecordChatRecipeSaved(ctx)
	info.savedID = created.ID
	s.draftInfo[ref] = info
	s.drafts[ref] = doc
	return created.ID, doc, nil
}

// approvalPreview is what a mutating call will replace, read before it runs so the
// approval card can mark it (BACKLOG.md 10.2). Read-only by construction — it must not
// resolve drafts the way resolvePlanDays does, because nothing has been approved yet and
// saving a draft is itself a write.
//
// An empty list is not a failure: it means the call replaces nothing, and the card shows
// only what will be written. Errors are swallowed for the same reason — a failed preview
// must not block the approval, only leave it unannotated.
func approvalPreview(ctx context.Context, s *chatSession, name string, args json.RawMessage) []map[string]any {
	switch name {
	case "apply_week_plan":
		var a struct {
			Days []planDayArg `json:"days"`
		}
		if json.Unmarshal(args, &a) != nil || len(a.Days) == 0 {
			return nil
		}
		wanted := map[string]int{} // (date, slot) -> recipe id the plan wants there, 0 for a draft
		var first, last time.Time
		for _, d := range a.Days {
			day, err := time.Parse(time.DateOnly, d.Date)
			if err != nil || day.Before(today()) {
				continue
			}
			slot := d.MealSlot
			if slot == "" {
				slot = storage.DefaultMealSlot
			}
			wanted[d.Date+" "+slot] = d.RecipeID
			if first.IsZero() || day.Before(first) {
				first = day
			}
			if day.After(last) {
				last = day
			}
		}
		if first.IsZero() {
			return nil
		}
		return replacedItems(ctx, s, first, last, func(item model.MealPlanItem) bool {
			id, ok := wanted[item.PlannedOn+" "+item.MealSlot]
			// A draft (id 0) has no saved recipe yet, so it always replaces what is there.
			return ok && item.RecipeID != id
		})
	case "mark_meal_cooked":
		// The meal itself is not "replaced", but the pantry lines it will empty are the
		// part the user needs to see before agreeing — this writes to their fridge, and
		// the deduction is the half they did not ask for in words.
		_, item, err := resolveCookedMeal(ctx, s, args)
		if err != nil {
			return nil
		}
		recipe, err := storage.GetRecipeByID(ctx, s.userID, item.RecipeID)
		if err != nil {
			return nil
		}
		var yield *int
		if recipe.Structured != nil {
			yield = recipe.Structured.Servings
		}
		applied, _, err := storage.PreviewDeductPantry(ctx, s.userID, item.RecipeID, model.PortionScale(item.Servings, yield))
		if err != nil {
			return nil
		}
		out := []map[string]any{}
		for _, c := range applied {
			row := map[string]any{"pantry_item": c.Name, "using": c.Used}
			if c.Unit != nil {
				row["unit"] = *c.Unit
			}
			if c.Removed {
				row["uses_up"] = true
			}
			out = append(out, row)
		}
		return out
	case "clear_plan_day":
		var a struct {
			Date     string `json:"date"`
			MealSlot string `json:"meal_slot"`
		}
		if json.Unmarshal(args, &a) != nil {
			return nil
		}
		day, err := time.Parse(time.DateOnly, a.Date)
		if err != nil {
			return nil
		}
		return replacedItems(ctx, s, day, day, func(item model.MealPlanItem) bool {
			return a.MealSlot == "" || item.MealSlot == a.MealSlot
		})
	}
	return nil
}

// replacedItems renders the planned items in [first, last] that keep matches, in the
// shape wouldOverwrite uses — one vocabulary for "this day already holds something".
func replacedItems(ctx context.Context, s *chatSession, first, last time.Time, keep func(model.MealPlanItem) bool) []map[string]any {
	existing, err := storage.GetMealPlanRange(ctx, s.userID, first, last)
	if err != nil {
		return nil
	}
	out := []map[string]any{}
	for _, item := range existing {
		if keep(item) {
			out = append(out, map[string]any{"date": item.PlannedOn, "meal_slot": item.MealSlot, "current_recipe": item.RecipeTitle})
		}
	}
	return out
}
