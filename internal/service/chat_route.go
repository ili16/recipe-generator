package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/openai/openai-go"
)

// The router: one cheap classification (Decisions API) before the loop decides which
// model and how many tools the turn gets. A vague "5 gourmet duck ideas" no longer pays
// for the frontier model plus a 25-tool catalog, and it is answered with option chips
// instead of an interview. Any failure routes to intentAgent — the full loop — so the
// router can only ever make a turn faster, never make one fail.
type intent string

const (
	intentRecipe  intent = "recipe"  // explicit ask to create/transform a recipe
	intentOptions intent = "options" // wants ideas, or says "just recommend something"
	intentClarify intent = "clarify" // too vague to act on
	intentPrefs   intent = "prefs"
	intentPlan    intent = "plan"
	intentPantry  intent = "pantry"
	intentChat    intent = "chat" // food talk, cooking questions, anything else
	intentAgent   intent = ""     // router unavailable or unsure: the full loop
)

const routeMinConfidence = 0.6

var intentQuestion = llm.Question{
	Type: "choice",
	Name: "intent",
	Instructions: "Classify what the user's LAST message wants from a recipe app assistant. " +
		"Choose recipe only when they explicitly ask to create, generate, write, transform or save a recipe (or send a link, photo or pasted recipe), or confirm one the assistant just offered. " +
		"Choose options when they ask for ideas, suggestions or a recommendation, or say 'just recommend something'. " +
		"Choose clarify only when the request is so vague that no sensible ideas could be offered.",
	Choices: []llm.Choice{
		{Value: string(intentRecipe), Description: "explicitly wants a recipe created, changed, translated or saved, or shared a link/photo/recipe text"},
		{Value: string(intentOptions), Description: "wants several ideas, suggestions or a recommendation of what to cook"},
		{Value: string(intentClarify), Description: "too vague to act on, e.g. a bare 'food' or 'hungry'"},
		{Value: string(intentPrefs), Description: "about the user's own preferences, diet, dislikes or household settings"},
		{Value: string(intentPlan), Description: "about the weekly meal plan, what was cooked, or scheduling meals"},
		{Value: string(intentPantry), Description: "about pantry, fridge contents, groceries or a photo of a shelf or receipt"},
		{Value: string(intentChat), Description: "a cooking question, small talk, or anything else"},
	},
}

// routeIntent classifies the latest user message, with the assistant's last reply for
// context ("yes, that one" means nothing alone).
func routeIntent(ctx context.Context, msgs []model.ConversationMessage) intent {
	var user, assistant string
	for i := len(msgs) - 1; i >= 0; i-- {
		switch {
		case msgs[i].Role == "user" && user == "":
			user = msgs[i].Content
		case msgs[i].Role == "assistant" && msgs[i].Content != "" && assistant == "" && user != "":
			assistant = msgs[i].Content
		}
	}
	if user == "" {
		return intentAgent
	}
	input := "User: " + user
	if assistant != "" {
		input = "Assistant: " + assistant + "\n" + input
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	answers, err := llm.Decide(ctx, "chat_route", input, []llm.Question{intentQuestion})
	if err != nil {
		slog.WarnContext(ctx, "chat: routing failed, using full loop", "error", err)
		return intentAgent
	}
	a := answers["intent"]
	if a.Type == "refusal" || a.Confidence < routeMinConfidence {
		return intentAgent
	}
	return intent(a.Choice)
}

// routeTools names the tools a routed turn may use. nil means all of them.
var routeTools = map[intent][]string{
	intentOptions: {"offer_options"},
	intentClarify: {"offer_options"},
	intentChat:    {"answer_cooking_question", "get_preferences", "search_my_recipes", "meal_history", "get_recipe", "calculate", "seasonal"},
	intentPrefs:   {"get_preferences", "set_preferences", "note_preference"},
	intentPlan: {"plan_week", "apply_week_plan", "set_plan_day", "clear_plan_day", "mark_meal_cooked",
		"search_my_recipes", "meal_history", "get_recipe", "display_recipe", "get_preferences", "seasonal"},
	intentPantry: {"get_pantry", "scan_pantry", "add_pantry_items", "search_my_recipes", "get_recipe", "display_recipe"},
}

// routedModel is the model a route runs on: the small one for turns that only talk or
// offer chips, the frontier model for anything that creates or plans.
func routedModel(in intent) openai.ChatModel {
	switch in {
	case intentOptions, intentClarify, intentChat, intentPrefs:
		return assistModel
	}
	return recipeModel
}

// routeSteer is the one extra system line a route adds to the prompt.
func routeSteer(in intent) string {
	switch in {
	case intentOptions:
		return "- Routed: the user wants ideas. Call offer_options NOW with 5 concrete dishes, each with a label and a one-line detail (main components, sauce, sides). Do not ask questions first; if they said to just recommend something, offer a few and name your top pick in one sentence."
	case intentClarify:
		return "- Routed: the request is too vague. Call offer_options with ONE short question as prompt and 2-4 tappable answers. Never ask in prose."
	case intentRecipe:
		return "- Routed: the user explicitly wants a recipe. Call generate_recipe (or transform_recipe) immediately; do not ask questions. One short sentence after the card."
	}
	return ""
}
