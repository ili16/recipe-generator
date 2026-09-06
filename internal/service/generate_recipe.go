package service

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/ili16/recipe-generator/internal/utils"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const structuredSystemPromptBase = "You are a culinary AI. Extract and structure the provided recipe or recipe description. " +
	"If a field cannot be determined from the input, use null.\n\n" +
	"Unit rules — follow strictly:\n" +
	"- Liquids (oil, soy sauce, vinegar, stock, juice): use ml or g, never tablespoons/teaspoons/cups.\n" +
	"- Dry solids (flour, sugar, spices, cheese, vegetables, meat): use g or kg.\n" +
	"- Small spice amounts under 5 g: still use g (e.g. '2 g salt', '1 g pepper').\n" +
	"- Genuinely countable whole items where weight is impractical: use a plain integer with no unit (e.g. '2 eggs', '4 chicken thighs', '30 gyoza wrappers'). Set unit to null.\n" +
	"- Never use 'pieces', 'cloves', 'tablespoons', 'teaspoons', 'cups', 'oz', 'lb', 'fl oz'.\n" +
	"- Garlic: convert cloves to grams (1 clove ≈ 5 g).\n" +
	"- Non-numeric amounts like 'to taste' or 'for garnish': set quantity and unit to null, put the phrase in quantity_text.\n\n" +
	"Time rules — follow strictly:\n" +
	"- prep_minutes is total hands-on and passive/waiting time before active cooking begins, including any " +
	"proofing, resting, marinating, or chilling time described in the steps. cook_minutes is total active " +
	"cooking/baking time.\n" +
	"- Always recompute prep_minutes and cook_minutes from the steps' text and timer_seconds — never carry over " +
	"a previous value if any step's timing changed.\n\n" +
	"ingredient_indices rule: for each step, set ingredient_indices to the list of 0-based indices (into the ingredients array) of every ingredient directly used in that step. Use an empty array if none apply.\n\n" +
	"tags rule: assign every tag from the allowed list that genuinely applies to this recipe — usually several, spanning multiple groups (meal type, dietary, time/difficulty, cooking method, primary ingredient, cuisine/vibe). Do not limit yourself to one tag or one group."

func buildSystemPrompt(language string) string {
	if language == "" {
		return structuredSystemPromptBase +
			" Detect the language of the input, set the language field to its ISO 639-1 code, and use only that language for all text fields."
	}
	return structuredSystemPromptBase +
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
	default:
		return "Generate a recipe for: " + origin.Prompt
	}
}

// refineJudgmentInstructions is appended to the base structured-recipe system prompt
// only for /refine-recipe calls, so the model guards the outcome instead of blindly
// applying every requested change.
const refineJudgmentInstructions = "\n\nYou are applying a user-requested change to an existing recipe. Before regenerating, judge the request:\n" +
	"- If the change would remove or replace something essential to the dish's identity, such that the " +
	"result would no longer reasonably be called the same dish (e.g. removing egg from carbonara), respond " +
	"with status \"rejected\", a short message explaining why, and recipe set to null. Do not modify the recipe.\n" +
	"- If the change is viable but the right substitution is genuinely ambiguous (e.g. removing the defining " +
	"protein from a dish), respond with status \"needs_choice\", a short message, 2-4 concrete short options " +
	"(each a viable substitute, or \"omit entirely\" when that's reasonable), and recipe set to null. Do not " +
	"guess which option the user wants.\n" +
	"- Otherwise, apply the change and respond with status \"applied\", message set to null, options as an " +
	"empty array, and the full regenerated recipe document."

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

const judgeSystemMessage = "You are a judge AI agent that decides whether input is related to cooking or not."

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
// them. Bump by hand whenever structuredSystemPromptBase/refineJudgmentInstructions
// changes materially, in the same commit as the prompt edit.
const promptVersion = "v2"

func OpenAIclient() openai.Client {
	return openai.NewClient(option.WithAPIKey(os.Getenv("OPENAI_KEY")))
}

func recipeJSONSchema() map[string]any {
	nullable := func(typ string) map[string]any {
		return map[string]any{
			"anyOf": []any{map[string]any{"type": typ}, map[string]any{"type": "null"}},
		}
	}
	ingredientSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"section":       nullable("string"),
			"item":          map[string]any{"type": "string"},
			"quantity":      nullable("number"),
			"quantity_text": nullable("string"),
			"unit":          nullable("string"),
			"optional":      map[string]any{"type": "boolean"},
		},
		"required":             []string{"section", "item", "quantity", "quantity_text", "unit", "optional"},
		"additionalProperties": false,
	}
	stepSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sort_order":         map[string]any{"type": "integer"},
			"step_text":          map[string]any{"type": "string"},
			"timer_seconds":      nullable("integer"),
			"temperature_c":      nullable("integer"),
			"ingredient_indices": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
		},
		"required":             []string{"sort_order", "step_text", "timer_seconds", "temperature_c", "ingredient_indices"},
		"additionalProperties": false,
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":    map[string]any{"type": "string"},
			"summary":  nullable("string"),
			"language": map[string]any{"type": "string"},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string", "enum": AllTagSlugs},
			},
			"servings":     nullable("integer"),
			"prep_minutes": nullable("integer"),
			"cook_minutes": nullable("integer"),
			"difficulty": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "string", "enum": []string{"easy", "medium", "hard"}},
					map[string]any{"type": "null"},
				},
			},
			"ingredients": map[string]any{"type": "array", "items": ingredientSchema},
			"steps":       map[string]any{"type": "array", "items": stepSchema},
		},
		"required": []string{
			"title", "summary", "language", "tags",
			"servings", "prep_minutes", "cook_minutes", "difficulty",
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
	client := OpenAIclient()

	slog.DebugContext(ctx, "llm: refine completion start", "model", recipeModel, "messages", len(messages))
	t := time.Now()

	ctx, span := telemetry.StartSpan(ctx, "llm.chat_completion")
	span.SetAttributes(attribute.String("llm.model", string(recipeModel)), attribute.String("llm.call_type", "refine"))
	defer span.End()

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    recipeModel,
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "refine_result",
					Schema: refineJSONSchema(),
					Strict: param.NewOpt(true),
				},
			},
		},
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.RecordLLMCall(ctx, "refine", string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.RefineResult{}, fmt.Errorf("refine completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		telemetry.RecordLLMCall(ctx, "refine", string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.RefineResult{}, errors.New("no choices returned")
	}
	telemetry.RecordLLMUsage(ctx, "refine", string(recipeModel), completion.Usage.PromptTokens, completion.Usage.CompletionTokens)
	recordCost(ctx, "refine", string(recipeModel), completion.Usage.PromptTokens, completion.Usage.CompletionTokens)

	raw := completion.Choices[0].Message.Content
	slog.DebugContext(ctx, "llm: refine raw output", "json", raw)

	var result model.RefineResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		telemetry.RecordLLMCall(ctx, "refine", string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.RefineResult{}, fmt.Errorf("parse refine response: %w", err)
	}
	if result.Status == "applied" && result.Recipe == nil {
		telemetry.RecordLLMCall(ctx, "refine", string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.RefineResult{}, errors.New("applied status missing recipe")
	}
	slog.DebugContext(ctx, "llm: refine result", "status", result.Status, "dur_ms", time.Since(t).Milliseconds(), "tokens", completion.Usage.TotalTokens)
	telemetry.RecordLLMCall(ctx, "refine", string(recipeModel), time.Since(t).Milliseconds(), true)
	return result, nil
}

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
// consistent with everything that came before.
func generateStructuredFromMessages(ctx context.Context, callType string, messages []openai.ChatCompletionMessageParamUnion) (model.RecipeDocument, error) {
	client := OpenAIclient()

	slog.DebugContext(ctx, "llm: structured completion start", "model", recipeModel, "messages", len(messages), "call_type", callType)
	t := time.Now()

	ctx, span := telemetry.StartSpan(ctx, "llm.chat_completion")
	span.SetAttributes(attribute.String("llm.model", string(recipeModel)), attribute.String("llm.call_type", callType))
	defer span.End()

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    recipeModel,
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "recipe",
					Schema: recipeJSONSchema(),
					Strict: param.NewOpt(true),
				},
			},
		},
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.RecordLLMCall(ctx, callType, string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.RecipeDocument{}, fmt.Errorf("structured completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		telemetry.RecordLLMCall(ctx, callType, string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.RecipeDocument{}, errors.New("no choices returned")
	}
	telemetry.RecordLLMUsage(ctx, callType, string(recipeModel), completion.Usage.PromptTokens, completion.Usage.CompletionTokens)
	recordCost(ctx, callType, string(recipeModel), completion.Usage.PromptTokens, completion.Usage.CompletionTokens)

	raw := completion.Choices[0].Message.Content
	slog.DebugContext(ctx, "llm: structured raw output", "json", raw)

	var doc model.RecipeDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		telemetry.RecordLLMCall(ctx, callType, string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.RecipeDocument{}, fmt.Errorf("parse structured response: %w", err)
	}
	if doc.Title == "" && len(doc.Ingredients) == 0 && len(doc.Steps) == 0 {
		telemetry.RecordLLMCall(ctx, callType, string(recipeModel), time.Since(t).Milliseconds(), false)
		return model.RecipeDocument{}, errors.New("no recipe found in the provided content")
	}
	slog.DebugContext(ctx, "llm: structured recipe", "model", recipeModel,
		"dur_ms", time.Since(t).Milliseconds(),
		"tokens", completion.Usage.TotalTokens,
		"title", doc.Title)
	telemetry.RecordLLMCall(ctx, callType, string(recipeModel), time.Since(t).Milliseconds(), true)
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

func GenerateRecipe(input model.GenerateRecipeInput) (model.RecipeDocument, error) {
	if input.Image != nil {
		return generateFromImage(input.Image, input.Prompt)
	}
	if input.URL != "" {
		return generateFromURL(input.URL, input.Prompt)
	}
	return generateFromText(input.Prompt, input.PreferredLanguage)
}

func generateFromText(prompt, preferredLanguage string) (model.RecipeDocument, error) {
	language := resolveLanguage(prompt, preferredLanguage)
	slog.Debug("generate: text", "prompt_len", len(prompt), "language", language)
	return generateStructured(context.TODO(), language, openai.TextContentPart("Generate a recipe for: "+prompt))
}

func generateFromURL(url, prompt string) (model.RecipeDocument, error) {
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
	return generateStructured(context.TODO(), "", openai.TextContentPart(userText+"\n\n"+content))
}

func generateFromImage(file multipart.File, prompt string) (model.RecipeDocument, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return model.RecipeDocument{}, fmt.Errorf("read image: %w", err)
	}
	mime := http.DetectContentType(data)
	if mime == "application/octet-stream" {
		mime = "image/jpeg"
	}
	slog.Debug("generate/image: read", "bytes", len(data), "mime", mime)
	userText := "Extract and structure the recipe shown in this image."
	if prompt != "" {
		userText += " Additional instructions: " + prompt
	}
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	return generateStructured(context.TODO(), "",
		openai.TextContentPart(userText),
		openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: dataURL}),
	)
}

var sectionHeadings = map[string][2]string{
	"de": {"Zutaten", "Zubereitung"},
	"fr": {"Ingrédients", "Préparation"},
	"es": {"Ingredientes", "Preparación"},
	"it": {"Ingredienti", "Preparazione"},
}

func headings(language string) (ingredients, preparation string) {
	if h, ok := sectionHeadings[strings.ToLower(language)]; ok {
		return h[0], h[1]
	}
	return "Ingredients", "Preparation"
}

// RenderMarkdown produces a markdown string from a RecipeDocument for the mobile client.
func RenderMarkdown(doc model.RecipeDocument) string {
	var b strings.Builder
	b.WriteString("# " + doc.Title + "\n\n")
	if doc.Summary != nil && *doc.Summary != "" {
		b.WriteString(*doc.Summary + "\n\n")
	}
	var meta []string
	if doc.Servings != nil {
		meta = append(meta, fmt.Sprintf("Servings: %d", *doc.Servings))
	}
	if doc.PrepMinutes != nil {
		meta = append(meta, fmt.Sprintf("Prep: %d min", *doc.PrepMinutes))
	}
	if doc.CookMinutes != nil {
		meta = append(meta, fmt.Sprintf("Cook: %d min", *doc.CookMinutes))
	}
	if len(meta) > 0 {
		b.WriteString("*" + strings.Join(meta, " · ") + "*\n\n")
	}

	ingHeading, prepHeading := headings(doc.Language)

	b.WriteString("## " + ingHeading + "\n\n")
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
			line += " *(optional)*"
		}
		b.WriteString(line + "\n")
	}

	b.WriteString("\n## " + prepHeading + "\n\n")
	for _, step := range doc.Steps {
		b.WriteString(fmt.Sprintf("%d. %s\n", step.SortOrder, step.StepText))
		if step.TimerSeconds != nil && *step.TimerSeconds > 0 {
			b.WriteString(fmt.Sprintf("   *⏱ %d min*\n", *step.TimerSeconds/60))
		}
	}
	return b.String()
}

func classifyTags(ctx context.Context, content string) []string {
	slog.Debug("llm: classify tags start")
	result, err := goopenAIChatCompletion(ctx,
		"Which of these recipe tags apply: "+strings.Join(AllTagSlugs, ", ")+
			"? A recipe usually has several, spanning different groups (meal type, dietary, time/difficulty, "+
			"cooking method, primary ingredient, cuisine/vibe). Reply with only the matching tag names, comma-separated.",
		content,
		string(assistModel),
		"classify",
	)
	if err != nil {
		slog.Warn("tag classification failed, defaulting to no tags", "error", err)
		return nil
	}
	var tags []string
	for _, slug := range AllTagSlugs {
		if strings.Contains(result, slug) {
			tags = append(tags, slug)
		}
	}
	return tags
}

// isRecipeRelated judges whether input is cooking-related. recipeContext is optional
// (pass "" when there's no existing recipe, e.g. the initial /generate call); when set,
// it's the original ask plus prior edits plus the current recipe, so short follow-ups
// like "increase proofing time" or one referencing earlier conversation are judged in
// full context instead of in isolation.
func isRecipeRelated(ctx context.Context, input string, recipeContext string) bool {
	hasContext := recipeContext != ""
	slog.DebugContext(ctx, "llm: judge relevance start", "input_len", len(input), "has_context", hasContext)
	prompt := "Is this input related to cooking? Only answer with 'yes' or 'no': " + input
	if hasContext {
		prompt = "The user said this about the recipe below — it may be a requested change, a question, a correction, or a casual comment/complaint. Is it related to cooking or this recipe in any way? Only answer with 'yes' or 'no'.\n\nUser input: " + input + "\n\nConversation so far:\n" + recipeContext
	}
	result, err := goopenAIChatCompletion(
		ctx,
		judgeSystemMessage,
		prompt,
		string(assistModel),
		"judge",
	)
	if err != nil {
		slog.Error("Error judging input", "error", err)
		return true // fail open to avoid blocking on judge errors
	}
	allowed := strings.Contains(strings.ToLower(result), "yes")
	telemetry.RecordJudgeDecision(ctx, hasContext, allowed)
	return allowed
}
