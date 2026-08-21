package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/openai/openai-go"
)

func HandleGenerateRecipe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "invalid method", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(10 << 20) // 10MB
	if err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	prompt := r.FormValue("description")
	url := r.FormValue("url")

	file, _, err := r.FormFile("image")
	var image multipart.File
	if err == nil {
		defer file.Close()
		image = file
	}

	input := model.GenerateRecipeInput{
		Prompt: prompt,
		URL:    url,
		Image:  image,
	}

	generatedRecipe, name, err := GenerateRecipe(input)

	resp := model.Recipe{
		Recipename: name,
		Recipe:     generatedRecipe,
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(resp)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		slog.Error("Error encoding JSON response", "error", err)
		return
	}
}

func HandleAddRecipe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	var req model.Recipe
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.Recipename == "" || req.Recipe == "" {
		http.Error(w, "Missing recipename or recipe", http.StatusBadRequest)
		return
	}

	if req.Category == "" {
		req.Category = goopenAIgenerateRecipeCategory(req.Recipe)
	}

	created, err := storage.AddRecipe(r.Context(), user.ID, req.Recipename, req.Recipe, req.Category)
	if err != nil {
		http.Error(w, "Error adding recipe", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(created); err != nil {
		slog.Error("Error encoding JSON response", "error", err)
	}
}

func HandlerIsRecipeRelated(r *http.Request) bool {
	client := OpenAIclient()

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("Error reading request body", "error", err)
		return false
	}

	slog.Debug("Raw request body", "body", string(bodyBytes))

	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	var req model.GenerateRecipeInput
	err = json.Unmarshal(bodyBytes, &req)
	if err != nil {
		slog.Error("Error decoding request", "error", err)
		return false
	}

	var systemmessage openai.ChatCompletionMessageParamUnion
	systemmessage = openai.SystemMessage(judgeSystemMessage)

	var usermessage openai.ChatCompletionMessageParamUnion
	usermessage = openai.UserMessage("Is this input related to a service? Only answer with 'yes' or 'no'" + req.Prompt)

	completion, err := client.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			systemmessage,
			usermessage,
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
	})
	if err != nil {
		slog.Error("Error judging input", "error", err)
		return false
	}

	slog.Debug("Completion response", "content", strings.ToLower(completion.Choices[0].Message.Content))
	return strings.Contains(strings.ToLower(completion.Choices[0].Message.Content), "yes")
}
