package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	_ "mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/utils"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	goopenai "github.com/sashabaranov/go-openai"
)

const (
	englishSystemMessage = "You are an agent that creates recipes for users" +
		"keep roughly the following markdown format:" +
		"# <Recipe Name>\n" + "## Ingredients/Zutaten\n" + "- **<UNIT>** Ingredient \n" + "## Preparation/Zubereitung" +
		"### (preparation, mise en place, etc.) \n" +
		"- Steps\n" +
		"### (cooking, sauteeing, etc.) " +
		"- Steps\n" +
		"All ingredients need to be in metric units."

	judgeSystemMessage = "You are a judge AI agent that decides whether input is related to cooking or not."
)

func OpenAIclient() *openai.Client {
	client := openai.NewClient(
		option.WithAPIKey(os.Getenv("OPENAI_KEY")),
	)

	return client
}

func GenerateRecipe(input model.GenerateRecipeInput) (generatedRecipe string, recipeName string, err error) {
	source := "text"
	if input.Image != nil {
		source = "image"
		generatedRecipe, err = GenerateRecipeByImage(input.Image, input.Prompt)
	} else if input.URL != "" {
		source = "url"
		generatedRecipe, err = GenerateRecipeByLink(input.URL, input.Prompt)
	} else {
		language, _ := utils.GetLanguage(input.Prompt)
		slog.Debug("generate: detected language", "language", language)
		generatedRecipe, err = openAIgenerateRecipe(input.Prompt, language)
	}

	if err != nil {
		return "", "", err
	}

	slog.Debug("generate: recipe body ready", "source", source, "chars", len(generatedRecipe))

	generateRecipeName, err := OpenAIgenerateRecipeName(generatedRecipe)
	if err != nil {
		return "", "", err
	}

	slog.Debug("generate: name ready", "name", generateRecipeName)
	return generatedRecipe, generateRecipeName, nil
}

func GenerateRecipeByLink(URL string, prompt string) (generatedRecipe string, err error) {
	slog.Debug("generate/url: fetching page", "url", URL)
	websiteContent, err := utils.GetWebsite(URL)
	if err != nil {
		slog.Error("Error getting website", "error", err)
		return "", err
	}
	slog.Debug("generate/url: page fetched", "bytes", len(websiteContent))

	t := time.Now()
	generatedRecipe, err = openAIgenerateRecipeLink(websiteContent, prompt)
	if err != nil {
		slog.Error("Error generating generatedRecipe", "error", err)
		return "", err
	}
	slog.Debug("generate/url: LLM done", "dur_ms", time.Since(t).Milliseconds(), "chars", len(generatedRecipe))
	return generatedRecipe, nil
}

func openAIgenerateRecipeLink(Recipe string, prompt string) (string, error) {
	client := OpenAIclient()

	var systemmessage openai.ChatCompletionMessageParamUnion
	systemmessage = openai.SystemMessage(englishSystemMessage)

	var usermessage openai.ChatCompletionMessageParamUnion
	if prompt != "" {
		usermessage = openai.UserMessage("Change to markdown format: " + Recipe + " and take the additional prompt into account: " + prompt)
	} else {
		usermessage = openai.UserMessage("Change to markdown format: " + Recipe)
	}

	t := time.Now()
	completion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{systemmessage, usermessage}),
		Model:    openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		return "", err
	}
	result := completion.Choices[0].Message.Content
	slog.Debug("llm: link recipe", "model", string(openai.ChatModelGPT4oMini), "dur_ms", time.Since(t).Milliseconds(), "tokens", completion.Usage.TotalTokens)
	return result, nil
}

func OpenAIgenerateRecipeName(Recipe string) (string, error) {
	client := OpenAIclient()

	t := time.Now()
	recipename, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You only respond with a recipe name. 3 words max."),
			openai.UserMessage("Generate a recipe name for: " + Recipe + "keep the name short and precise, no adjectives, no 'recipe' in the name"),
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		return "", err
	}
	result := recipename.Choices[0].Message.Content
	slog.Debug("llm: recipe name", "model", string(openai.ChatModelGPT4oMini), "dur_ms", time.Since(t).Milliseconds(), "tokens", recipename.Usage.TotalTokens, "name", result)
	return result, nil
}

func GenerateRecipeByImage(file multipart.File, prompt string) (recipe string, err error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("failed to read image: %w", err)
	}

	mime := http.DetectContentType(data)
	if mime == "application/octet-stream" {
		mime = "image/jpeg"
	}
	slog.Debug("generate/image: read", "bytes", len(data), "mime", mime)

	image := base64.StdEncoding.EncodeToString(data)
	t := time.Now()
	recipe, err = goopenAIgenerateRecipeImage(image, mime, prompt)
	if err != nil {
		fmt.Println("Error generating service:", err)
		return "", err
	}
	slog.Debug("generate/image: LLM done", "dur_ms", time.Since(t).Milliseconds(), "chars", len(recipe))
	return recipe, nil
}

func goopenAIgenerateRecipeImage(RecipeBase64 string, mime string, prompt string) (recipe string, err error) {
	client := goopenai.NewClient(os.Getenv("OPENAI_KEY"))

	var SystemMessage string
	if prompt != "" {
		SystemMessage = englishSystemMessage + " Also take the following prompt into account: " + prompt
	} else {
		SystemMessage = englishSystemMessage
	}

	t := time.Now()
	response, err := client.CreateChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model: goopenai.GPT4oMini,
		Messages: []goopenai.ChatCompletionMessage{
			{
				Role: goopenai.ChatMessageRoleUser,
				MultiContent: []goopenai.ChatMessagePart{
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: SystemMessage,
					},
					{
						Type: goopenai.ChatMessagePartTypeImageURL,
						ImageURL: &goopenai.ChatMessageImageURL{
							URL: "data:" + mime + ";base64," + RecipeBase64,
						},
					},
				},
			},
		},
	})
	if err != nil {
		return "", err
	}
	result := response.Choices[0].Message.Content
	slog.Debug("llm: image recipe", "model", string(goopenai.GPT4oMini), "dur_ms", time.Since(t).Milliseconds())
	return result, nil
}

func openAIgenerateRecipe(recipeDescription string, language string) (string, error) {
	client := OpenAIclient()

	t := time.Now()
	completion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(englishSystemMessage),
			openai.UserMessage("Generate a recipe for the following description (" + language + "): " + recipeDescription),
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		return "", err
	}
	result := completion.Choices[0].Message.Content
	slog.Debug("llm: text recipe", "model", string(openai.ChatModelGPT4oMini), "dur_ms", time.Since(t).Milliseconds(), "tokens", completion.Usage.TotalTokens, "language", language)
	return result, nil
}

func isRecipeRelated(recipe string) bool {
	result, err := goopenAIChatCompletion(
		context.TODO(),
		judgeSystemMessage,
		"Is this input related to a service? Only answer with 'yes' or 'no'"+recipe,
		openai.ChatModelGPT4oMini,
	)

	if err != nil {
		slog.Error("Error judging input", "error", err)
		return false
	}

	return strings.Contains(strings.ToLower(result), "yes")
}

func goopenAIgenerateRecipeCategory(Recipe string) string {
	client := goopenai.NewClient(os.Getenv("OPENAI_KEY"))

	response, err := client.CreateChatCompletion(context.Background(), goopenai.ChatCompletionRequest{
		Model: goopenai.GPT4oMini,
		Messages: []goopenai.ChatCompletionMessage{
			{
				Role: goopenai.ChatMessageRoleUser,
				MultiContent: []goopenai.ChatMessagePart{
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: "What is the category of this service? Currently only Hauptgericht, Vorspeise, Brot, Dessert are supported. Answer with a single word nothing else",
					},
					{
						Type: goopenai.ChatMessagePartTypeText,
						Text: Recipe,
					},
				},
			},
		},
	})
	if err != nil {
		slog.Error("Error generating service category", "error", err)
		return ""
	}

	categories := []string{"Hauptgericht", "Vorspeise", "Brot", "Dessert"}
	category := response.Choices[0].Message.Content
	for _, c := range categories {
		if strings.Contains(category, c) {
			return c
		}
	}
	slog.Warn("Recipe category not found, defaulting to Sonstiges")
	return "Sonstiges"
}
