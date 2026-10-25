package service

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/utils"
	"github.com/openai/openai-go"
)

// buildSystemPrompt appends a variable language suffix to the static
// llm.StructuredRecipePrompt — static block first, variable suffix after, preserving
// the prompt-caching shape (specs/RECIPE_QUALITY_IMPLEMENTATION.md §3).
func buildSystemPrompt(language string) string {
	if language == "" {
		return llm.StructuredRecipePrompt +
			" Detect the language of the input, set the language field to its ISO 639-1 code, and use only that language for all text fields."
	}
	return llm.StructuredRecipePrompt +
		fmt.Sprintf(" Respond entirely in the language with ISO 639-1 code '%s'. Set the language field to '%s'. Do not mix in any other language, do not add translations in parentheses.", language, language)
}

// buildOriginUserText renders the original generation ask as a user message for the
// refinement conversation. Raw URL/image content isn't retained past the initial
// generation, so url/image turns describe the source rather than resending it.
func buildOriginUserText(origin model.GenerationOrigin) string {
	switch origin.SourceType {
	case "url":
		text := "Extract and structure this recipe from the webpage: " + origin.SourceURL
		if origin.Prompt != "" && origin.Prompt != origin.SourceURL {
			text += "\nAdditional instructions: " + origin.Prompt
		}
		return text
	case "image":
		text := "Extract and structure the recipe shown in the provided image."
		if origin.Prompt != "" {
			text += " Additional instructions: " + origin.Prompt
		}
		return text
	case "import":
		return pastedRecipeAsk + "\n\n" + origin.Prompt
	default:
		return "Generate a recipe for: " + origin.Prompt
	}
}

// refineJudgmentInstructions is appended to the base structured-recipe system prompt
// only for /refine-recipe calls, so the model guards the outcome instead of blindly
// applying every requested change. llm.NoCarryOverTimeNote applies only here and to
// variant generation (mealplan.go) — a fresh generation (text/url/image) has no
// previous value to wrongly carry over, and there the source's own stated time should
// be preferred (see the base prompt's time rules).
var refineJudgmentInstructions = llm.NoCarryOverTimeNote + llm.RefineJudgmentAddendum

// buildRefineMessages threads the full edit conversation into a real multi-turn message
// list: the original ask, the model's first structured output, every accepted edit
// since then paired with the document it produced, and finally the new instruction.
func buildRefineMessages(origin model.GenerationOrigin, initial model.RecipeDocument, history []model.EditTurn, changePrompt string) ([]openai.ChatCompletionMessageParamUnion, error) {
	lang := initial.Language
	for _, turn := range history {
		if turn.Structured.Language != "" {
			lang = turn.Structured.Language
		}
	}

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(buildSystemPrompt(lang) + refineJudgmentInstructions),
		openai.UserMessage(buildOriginUserText(origin)),
	}

	appendDoc := func(doc model.RecipeDocument) error {
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		messages = append(messages, openai.AssistantMessage(string(raw)))
		return nil
	}

	if err := appendDoc(initial); err != nil {
		return nil, fmt.Errorf("marshal initial document: %w", err)
	}
	for _, turn := range history {
		messages = append(messages, openai.UserMessage(turn.ChangePrompt))
		if err := appendDoc(turn.Structured); err != nil {
			return nil, fmt.Errorf("marshal history document: %w", err)
		}
	}
	messages = append(messages, openai.UserMessage(changePrompt))
	return messages, nil
}

// recipeModel is the frontier model used for structured generation & refinement
// (/generate, /refine-recipe) — the tier where quality directly matters most and where
// getting it right on the first pass avoids costlier refine round-trips (see
// specs/RECIPE_QUALITY_IMPLEMENTATION.md §1). Overridable via LLM_MODEL for quick A/B
// comparisons between process runs — every recipe saved while a given value is set gets
// tagged with it (see AddRecipeStructured), so `select ai_model, count(*) from recipes
// group by 1` shows the split.
var recipeModel = openai.ChatModel(cmp.Or(os.Getenv("LLM_MODEL"), "gpt-5.6-luna"))

// assistModel is the small/cheap model for judgment-gating and UX-latency-critical
// calls (on-topic judge, category classification, suggestions, cooking chat) — tasks
// that run on every request regardless of generation quality, where a fast/cheap tier
// is fine as long as the task itself is genuinely simple. Overridable via
// LLM_ASSIST_MODEL for A/B comparisons.
var assistModel = openai.ChatModel(cmp.Or(os.Getenv("LLM_ASSIST_MODEL"), "gpt-5.4-nano"))

// promptVersion tags saved recipes with the structured-prompt revision that produced
// them — llm.PromptVersion, a content hash of the prompt files, changes automatically
// whenever one of them changes; no more hand-bumping.
var promptVersion = llm.PromptVersion

func recipeJSONSchema() map[string]any {
	ingredientSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"section":       llm.Nullable("string"),
			"item":          map[string]any{"type": "string"},
			"quantity":      llm.Nullable("number"),
			"quantity_text": llm.Nullable("string"),
			"unit":          llm.Nullable("string"),
			"optional":      map[string]any{"type": "boolean"},
			"category": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string", "enum": model.GroceryCategories},
					map[string]any{"type": "null"},
				},
			},
		},
		"required":             []string{"section", "item", "quantity", "quantity_text", "unit", "optional", "category"},
		"additionalProperties": false,
	}
	stepSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sort_order":         map[string]any{"type": "integer"},
			"step_text":          map[string]any{"type": "string"},
			"timer_seconds":      llm.Nullable("integer"),
			"temperature_c":      llm.Nullable("integer"),
			"ingredient_indices": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			"phase": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string", "enum": []string{"prep", "cook", "wait"}},
					map[string]any{"type": "null"},
				},
			},
			"parallel_group": llm.Nullable("integer"),
		},
		"required":             []string{"sort_order", "step_text", "timer_seconds", "temperature_c", "ingredient_indices", "phase", "parallel_group"},
		"additionalProperties": false,
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":    map[string]any{"type": "string"},
			"summary":  llm.Nullable("string"),
			"language": map[string]any{"type": "string"},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string", "enum": AllTagSlugs},
			},
			"cuisine":       llm.Nullable("string"),
			"techniques":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"servings":      llm.Nullable("integer"),
			"total_minutes": llm.Nullable("integer"),
			"prep_minutes":  llm.Nullable("integer"),
			"cook_minutes":  llm.Nullable("integer"),
			"difficulty": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string", "enum": []string{"easy", "medium", "hard"}},
					map[string]any{"type": "null"},
				},
			},
			"calories":  llm.Nullable("integer"),
			"protein_g": llm.Nullable("integer"),
			"carbs_g":   llm.Nullable("integer"),
			"fat_g":     llm.Nullable("integer"),

			"ingredients": map[string]any{"type": "array", "items": ingredientSchema},
			"steps":       map[string]any{"type": "array", "items": stepSchema},
		},
		"required": []string{
			"title", "summary", "language", "tags", "cuisine", "techniques",
			"servings", "total_minutes", "prep_minutes", "cook_minutes", "difficulty",
			"calories", "protein_g", "carbs_g", "fat_g",
			"ingredients", "steps",
		},
		"additionalProperties": false,
	}
}

// refineJSONSchema wraps recipeJSONSchema() in a status envelope so a refine call can
// report "rejected"/"needs_choice" instead of always returning a regenerated document.
func refineJSONSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type": "string",
				"enum": []string{"applied", "rejected", "needs_choice"},
			},
			"message": map[string]any{
				"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}},
			},
			"options": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"recipe": map[string]any{
				"anyOf": []any{recipeJSONSchema(), map[string]any{"type": "null"}},
			},
		},
		"required":             []string{"status", "message", "options", "recipe"},
		"additionalProperties": false,
	}
}

// generateRefineFromMessages runs a refine completion against the status-envelope
// schema. Mirrors generateStructuredFromMessages's call shape but decodes into
// model.RefineResult since a refine response may carry no recipe at all.
func generateRefineFromMessages(ctx context.Context, messages []openai.ChatCompletionMessageParamUnion) (model.RefineResult, error) {
	result, _, err := llm.Call[model.RefineResult](ctx, llm.Spec{
		Model:    recipeModel,
		CallType: "refine",
		Messages: messages,
		Schema:   &llm.Schema{Name: "refine_result", Schema: refineJSONSchema()},
	})
	if err != nil {
		return model.RefineResult{}, err
	}
	if result.Status == "applied" && result.Recipe == nil {
		return model.RefineResult{}, errors.New("applied status missing recipe")
	}
	return result, nil
}

// ReviewCookFlow re-plans how a recipe is cooked: it merges steps too thin to be worth a
// screen, labels each one prep/cook/wait, and marks the steps a cook does at the same
// time. Only the steps array (and the timings that follow from it) changes — the
// ingredients come back untouched, which this verifies rather than trusts, since a flow
// review that quietly rewrote the shopping list would be a much worse bug than a bad flow.
func ReviewCookFlow(ctx context.Context, doc model.RecipeDocument) (model.RecipeDocument, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return model.RecipeDocument{}, fmt.Errorf("marshal document: %w", err)
	}
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(buildSystemPrompt(doc.Language) + llm.CookFlowAddendum),
		openai.UserMessage(string(raw)),
	}
	out, err := generateStructuredFromMessages(ctx, "cook_flow", messages)
	if err != nil {
		return model.RecipeDocument{}, err
	}
	if !sameIngredients(doc.Ingredients, out.Ingredients) {
		return model.RecipeDocument{}, errors.New("cook flow review changed the ingredients")
	}
	// The model is told to return these as given; a stray edit to the title or the
	// nutrition is not worth a second round-trip, so pin them back instead.
	out.Ingredients = doc.Ingredients
	out.Title, out.Tags, out.Servings = doc.Title, doc.Tags, doc.Servings
	out.Cuisine, out.Techniques = doc.Cuisine, doc.Techniques
	out.Calories, out.ProteinG, out.CarbsG, out.FatG = doc.Calories, doc.ProteinG, doc.CarbsG, doc.FatG
	return out, nil
}

// sameIngredients reports whether two ingredient lists name the same things in the same
// amounts. Compared on what a shopper would notice — item, quantity, unit — not on the
// nullable annotations (section, category) a regeneration may legitimately fill in.
func sameIngredients(a, b []model.RecipeIngredient) bool {
	if len(a) != len(b) {
		return false
	}
	eqNum := func(x, y *float64) bool {
		return (x == nil) == (y == nil) && (x == nil || *x == *y)
	}
	eqStr := func(x, y *string) bool {
		return (x == nil) == (y == nil) && (x == nil || *x == *y)
	}
	for i := range a {
		if !strings.EqualFold(strings.TrimSpace(a[i].Item), strings.TrimSpace(b[i].Item)) ||
			!eqNum(a[i].Quantity, b[i].Quantity) ||
			!eqStr(a[i].Unit, b[i].Unit) ||
			a[i].Optional != b[i].Optional {
			return false
		}
	}
	return true
}

// generateStructured runs the structured-recipe completion for a fresh generation (a
// system+user pair built from language/parts). onDelta streams raw JSON text as it
// arrives; nil for a plain non-streaming call — same call, same validation either way.
func generateStructured(ctx context.Context, language string, parts ...openai.ChatCompletionContentPartUnionParam) (model.RecipeDocument, error) {
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(buildSystemPrompt(language)),
		openai.UserMessage(parts),
	}
	return generateStructuredFromMessages(ctx, "generate", messages)
}

// generateStructuredFromMessages runs the structured-recipe completion against an
// arbitrary message list — used both for one-shot generation (a system+user pair) and
// for refinement, where the caller threads the original prompt, every prior accepted
// edit, and the new instruction as a real multi-turn conversation so the model stays
// consistent with everything that came before. onDelta is nil outside the generate path
// (refine and meal-plan-variant calls never stream).
// ErrNoRecipeInSource is a source that was read fine and holds no recipe — the usual
// case is a Reel or a Short whose recipe is spoken in the video or sits in the comments,
// where the caption alone is a hook and a hashtag. The wording is the user's next step,
// because they have one: the video's own screen usually shows the ingredients.
var ErrNoRecipeInSource = errors.New("there was no recipe in that — the ingredients and steps were not in the text. If it is a video, the recipe is probably on screen or in the comments: ask the user to screenshot that part and send the image, or to paste the ingredients and steps")

func generateStructuredFromMessages(ctx context.Context, callType string, messages []openai.ChatCompletionMessageParamUnion) (model.RecipeDocument, error) {
	doc, _, err := llm.Call[model.RecipeDocument](ctx, llm.Spec{
		Model:    recipeModel,
		CallType: callType,
		Messages: messages,
		Schema:   &llm.Schema{Name: "recipe", Schema: recipeJSONSchema()},
	})
	if err != nil {
		return model.RecipeDocument{}, err
	}
	// A document with no ingredients or no steps is not a recipe, whatever it calls
	// itself. The model answers a thin source (a login-walled post, a caption that is
	// only a hashtag) with a titled shell — "Unspecified Recipe", nothing in it — and
	// the old guard, which asked for all three to be empty, let that through to be
	// shown and saved. The caller turns this into "I could not read that".
	if len(doc.Ingredients) == 0 || len(doc.Steps) == 0 {
		return model.RecipeDocument{}, ErrNoRecipeInSource
	}
	return doc, nil
}

// minWordsForLanguageDetection is a naive proxy for "the prompt is a sustained
// sentence in a language, not just a short foreign proper noun/dish name".
// ponytail: raw word count, not confidence-weighted — if longer prompts still
// misfire because a foreign proper noun outnumbers the sentence's real
// language words, switch to lingua's confidence-weighted API instead.
const minWordsForLanguageDetection = 4

// resolveLanguage decides the output language for a text prompt: a clear,
// sustained language signal in the prompt itself wins (e.g. a full sentence
// written in German); otherwise it falls back to the caller's preferred
// (browser/device) language, so a short foreign dish name like "sauce
// bearnaise" doesn't hijack the output language away from the user's locale.
func resolveLanguage(prompt, preferredLanguage string) string {
	if len(strings.Fields(prompt)) >= minWordsForLanguageDetection {
		if lang, ok := utils.DetectLanguage(prompt); ok {
			return lang
		}
	}
	return preferredLanguage
}

// GenerateRecipe generates from text/url/image (whichever input carries), non-streaming.
func GenerateRecipe(ctx context.Context, input model.GenerateRecipeInput) (model.RecipeDocument, error) {
	return generateRecipe(ctx, input)
}

func generateRecipe(ctx context.Context, input model.GenerateRecipeInput) (model.RecipeDocument, error) {
	if input.Image != nil {
		return generateFromImage(ctx, input.Image, input.Prompt, input.PreferredLanguage)
	}
	if input.URL != "" {
		return generateFromURL(ctx, input.URL, input.Prompt, input.PreferredLanguage)
	}
	if input.SourceText != "" {
		return generateFromSourceText(ctx, input.SourceText, input.Prompt, input.PreferredLanguage)
	}
	return generateFromText(ctx, input.Prompt, input.PreferredLanguage)
}

// pastedRecipeAsk frames a recipe the user pasted as one to transcribe. Framed like a
// description ("Generate a recipe for: …") the model treats a full recipe as a brief and
// writes its own — a 150 g poolish came back as 500 g, instant yeast as fresh.
const pastedRecipeAsk = "Extract and structure this recipe from the text the user pasted."

func generateFromSourceText(ctx context.Context, text, prompt, preferredLanguage string) (model.RecipeDocument, error) {
	userText := pastedRecipeAsk
	if prompt != "" {
		userText += " Additional instructions: " + prompt
	}
	// The language is the recipe's, not the device's: a German recipe stays German.
	language := resolveLanguage(text, preferredLanguage)
	slog.Debug("generate: pasted recipe", "text_len", len(text), "language", language)
	return generateStructured(ctx, language, openai.TextContentPart(userText+"\n\n"+text))
}

func generateFromText(ctx context.Context, prompt, preferredLanguage string) (model.RecipeDocument, error) {
	language := resolveLanguage(prompt, preferredLanguage)
	slog.Debug("generate: text", "prompt_len", len(prompt), "language", language)
	return generateStructured(ctx, language, openai.TextContentPart("Generate a recipe for: "+prompt))
}

func generateFromURL(ctx context.Context, url, prompt, preferredLanguage string) (model.RecipeDocument, error) {
	slog.Debug("generate/url: fetching page", "url", url)
	content, err := utils.GetWebsiteContent(url)
	if err != nil {
		return model.RecipeDocument{}, fmt.Errorf("fetch url: %w", err)
	}
	slog.Debug("generate/url: page fetched", "bytes", len(content))
	userText := "Extract and structure this recipe from the webpage content."
	if prompt != "" {
		userText += " Additional instructions: " + prompt
	}
	language := resolveLanguage(prompt, preferredLanguage)
	doc, err := generateStructured(ctx, language, openai.TextContentPart(userText+"\n\n"+content))
	if !errors.Is(err, ErrNoRecipeInSource) {
		return doc, err
	}

	// The page's text held no recipe. On a reel that usually means the recipe is in the
	// video rather than the page — spoken over it, and written on it — so read both and
	// try once more with everything. Anything that is not a readable video (every
	// ordinary webpage) comes straight back, and the original "no recipe in that" error
	// stands.
	reading, terr := videoTranscript(ctx, url)
	if terr != nil {
		return model.RecipeDocument{}, err
	}
	transcript, screen := strings.TrimSpace(reading.Transcript), strings.TrimSpace(reading.ScreenText)
	if transcript == "" && screen == "" {
		return model.RecipeDocument{}, err
	}
	slog.DebugContext(ctx, "generate/url: retrying with the video itself",
		"transcript_chars", len(transcript), "screen_chars", len(screen))

	// The screen is labelled as the authority on amounts, because it is: the voiceover
	// says "a good glug" over a frame that says 2 tbsp, and without this the model
	// averages the two into a guess (BACKLOG 11.10).
	source := userText + "\n\n" + content
	if transcript != "" {
		source += "\n\nSpoken transcript of the video on that page:\n" + transcript
	}
	if screen != "" {
		source += "\n\nText shown on screen in that video — where this and the transcript disagree" +
			" about an amount, an ingredient or a temperature, this is the one to trust:\n" + screen
	}
	return generateStructured(ctx, language, openai.TextContentPart(source))
}

func generateFromImage(ctx context.Context, file io.Reader, prompt, preferredLanguage string) (model.RecipeDocument, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return model.RecipeDocument{}, fmt.Errorf("read image: %w", err)
	}
	slog.Debug("generate/image: read", "bytes", len(data))
	userText := "Extract and structure the recipe shown in this image."
	if prompt != "" {
		userText += " Additional instructions: " + prompt
	}
	return generateStructured(ctx, resolveLanguage(prompt, preferredLanguage),
		openai.TextContentPart(userText),
		openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: imageDataURL(data)}),
	)
}

// recipeLabels are the words RenderMarkdown puts around the recipe's own words. They
// are display text generated server-side, so they follow the recipe's language (BACKLOG
// 14.3) — not the caller's UI language, because the markdown is stored with the recipe.
type recipeLabels struct {
	Ingredients, Preparation         string
	Servings, Total, Prep, Cook, Min string
	PerServing                       string
	Protein, Carbs, Fat              string
	Optional                         string
}

var englishLabels = recipeLabels{
	"Ingredients", "Preparation",
	"Servings", "Total", "Prep", "Cook", "min",
	"Per serving (estimated)",
	"protein", "carbs", "fat",
	"optional",
}

var labelsByLanguage = map[string]recipeLabels{
	"de": {
		"Zutaten", "Zubereitung",
		"Portionen", "Gesamt", "Vorbereitung", "Kochzeit", "Min.",
		"Pro Portion (geschätzt)",
		"Eiweiß", "Kohlenhydrate", "Fett",
		"optional",
	},
	"fr": {
		"Ingrédients", "Préparation",
		"Portions", "Total", "Préparation", "Cuisson", "min",
		"Par portion (estimé)",
		"protéines", "glucides", "lipides",
		"facultatif",
	},
	"es": {
		"Ingredientes", "Preparación",
		"Raciones", "Total", "Preparación", "Cocción", "min",
		"Por ración (estimado)",
		"proteínas", "carbohidratos", "grasas",
		"opcional",
	},
	"it": {
		"Ingredienti", "Preparazione",
		"Porzioni", "Totale", "Preparazione", "Cottura", "min",
		"Per porzione (stimato)",
		"proteine", "carboidrati", "grassi",
		"facoltativo",
	},
}

func labelsFor(language string) recipeLabels {
	if l, ok := labelsByLanguage[strings.ToLower(language)]; ok {
		return l
	}
	return englishLabels
}

// RenderMarkdown produces a markdown string from a RecipeDocument for the mobile client.
func RenderMarkdown(doc model.RecipeDocument) string {
	lb := labelsFor(doc.Language)
	var b strings.Builder
	b.WriteString("# " + doc.Title + "\n\n")
	if doc.Summary != nil && *doc.Summary != "" {
		b.WriteString(*doc.Summary + "\n\n")
	}
	var meta []string
	if doc.Servings != nil {
		meta = append(meta, fmt.Sprintf("%s: %d", lb.Servings, *doc.Servings))
	}
	if doc.TotalMinutes != nil {
		meta = append(meta, fmt.Sprintf("%s: %d %s", lb.Total, *doc.TotalMinutes, lb.Min))
	}
	if doc.PrepMinutes != nil {
		meta = append(meta, fmt.Sprintf("%s: %d %s", lb.Prep, *doc.PrepMinutes, lb.Min))
	}
	if doc.CookMinutes != nil {
		meta = append(meta, fmt.Sprintf("%s: %d %s", lb.Cook, *doc.CookMinutes, lb.Min))
	}
	if len(meta) > 0 {
		b.WriteString("*" + strings.Join(meta, " · ") + "*\n\n")
	}

	// Estimated per-serving macros, each omitted when the model couldn't estimate it.
	var macros []string
	for _, m := range []struct {
		label string
		value *int
	}{
		{"%d kcal", doc.Calories},
		{"%dg " + lb.Protein, doc.ProteinG},
		{"%dg " + lb.Carbs, doc.CarbsG},
		{"%dg " + lb.Fat, doc.FatG},
	} {
		if m.value != nil {
			macros = append(macros, fmt.Sprintf(m.label, *m.value))
		}
	}
	if len(macros) > 0 {
		b.WriteString("*" + lb.PerServing + ": " + strings.Join(macros, " · ") + "*\n\n")
	}

	b.WriteString("## " + lb.Ingredients + "\n\n")
	currentSection := ""
	for _, ing := range doc.Ingredients {
		sec := ""
		if ing.Section != nil {
			sec = *ing.Section
		}
		if sec != currentSection {
			currentSection = sec
			if sec != "" {
				b.WriteString("\n### " + sec + "\n\n")
			}
		}
		qty := ""
		hasUnit := ing.Unit != nil && *ing.Unit != "" && *ing.Unit != "null"
		if ing.Quantity != nil && *ing.Quantity > 0 {
			if *ing.Quantity == float64(int(*ing.Quantity)) {
				qty = fmt.Sprintf("%d ", int(*ing.Quantity))
			} else {
				qty = fmt.Sprintf("%.1f ", *ing.Quantity)
			}
			if hasUnit {
				qty += *ing.Unit + " "
			}
		} else if ing.QuantityText != nil && *ing.QuantityText != "" {
			// non-numeric amounts like "a pinch", "to taste", "for dusting"
			qty = *ing.QuantityText + " "
		}
		line := "- " + qty + ing.Item
		if ing.Optional {
			line += " *(" + lb.Optional + ")*"
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n## " + lb.Preparation + "\n\n")
	for _, step := range doc.Steps {
		b.WriteString(fmt.Sprintf("%d. %s\n", step.SortOrder, step.StepText))
		if step.TimerSeconds != nil && *step.TimerSeconds > 0 {
			b.WriteString(fmt.Sprintf("   *⏱ %d %s*\n", *step.TimerSeconds/60, lb.Min))
		}
	}
	return b.String()
}
