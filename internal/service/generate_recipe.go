package service

import (
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
	"github.com/ili16/recipe-generator/internal/utils"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
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
	"ingredient_indices rule: for each step, set ingredient_indices to the list of 0-based indices (into the ingredients array) of every ingredient directly used in that step. Use an empty array if none apply."

func buildSystemPrompt(language string) string {
	if language == "" {
		return structuredSystemPromptBase +
			" Detect the language of the input, set the language field to its ISO 639-1 code, and use only that language for all text fields."
	}
	return structuredSystemPromptBase +
		fmt.Sprintf(" Respond entirely in the language with ISO 639-1 code '%s'. Set the language field to '%s'. Do not mix in any other language, do not add translations in parentheses.", language, language)
}

const judgeSystemMessage = "You are a judge AI agent that decides whether input is related to cooking or not."

// recipeModel is the model used for all recipe generation and classification calls.
const recipeModel openai.ChatModel = "gpt-4.1-mini"

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
			"category": map[string]any{
				"type": "string",
				"enum": []string{"main", "starter", "bread", "dessert", "other"},
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
			"title", "summary", "language", "category",
			"servings", "prep_minutes", "cook_minutes", "difficulty",
			"ingredients", "steps",
		},
		"additionalProperties": false,
	}
}

func generateStructured(ctx context.Context, language string, parts ...openai.ChatCompletionContentPartUnionParam) (model.RecipeDocument, error) {
	client := OpenAIclient()

	slog.Debug("llm: structured completion start", "model", recipeModel, "parts", len(parts), "language", language)
	t := time.Now()
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(buildSystemPrompt(language)),
			openai.UserMessage(parts),
		},
		Model: recipeModel,
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
		return model.RecipeDocument{}, fmt.Errorf("structured completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return model.RecipeDocument{}, errors.New("no choices returned")
	}

	raw := completion.Choices[0].Message.Content
	slog.Debug("llm: structured raw output", "json", raw)

	var doc model.RecipeDocument
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return model.RecipeDocument{}, fmt.Errorf("parse structured response: %w", err)
	}
	if doc.Title == "" && len(doc.Ingredients) == 0 && len(doc.Steps) == 0 {
		return model.RecipeDocument{}, errors.New("no recipe found in the provided content")
	}
	slog.Debug("llm: structured recipe", "model", recipeModel,
		"dur_ms", time.Since(t).Milliseconds(),
		"tokens", completion.Usage.TotalTokens,
		"title", doc.Title)
	return doc, nil
}

func GenerateRecipe(input model.GenerateRecipeInput) (model.RecipeDocument, error) {
	if input.Image != nil {
		return generateFromImage(input.Image, input.Prompt)
	}
	if input.URL != "" {
		return generateFromURL(input.URL, input.Prompt)
	}
	return generateFromText(input.Prompt)
}

func generateFromText(prompt string) (model.RecipeDocument, error) {
	language, err := utils.GetLanguage(prompt)
	if err != nil {
		language = ""
	}
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

func classifyCategory(ctx context.Context, content string) string {
	slog.Debug("llm: classify category start")
	result, err := goopenAIChatCompletion(ctx,
		"What is the category of this recipe? Only answer with one of: main, starter, bread, dessert, other. Single word only.",
		content,
		recipeModel,
	)
	if err != nil {
		slog.Warn("category classification failed, defaulting to other", "error", err)
		return "other"
	}
	for _, c := range []string{"main", "starter", "bread", "dessert"} {
		if strings.Contains(result, c) {
			return c
		}
	}
	return "other"
}

func isRecipeRelated(ctx context.Context, recipe string) bool {
	slog.Debug("llm: judge relevance start", "input_len", len(recipe))
	result, err := goopenAIChatCompletion(
		ctx,
		judgeSystemMessage,
		"Is this input related to cooking? Only answer with 'yes' or 'no': "+recipe,
		recipeModel,
	)
	if err != nil {
		slog.Error("Error judging input", "error", err)
		return true // fail open to avoid blocking on judge errors
	}
	return strings.Contains(strings.ToLower(result), "yes")
}
