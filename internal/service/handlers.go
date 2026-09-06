package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/openai/openai-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// namedReader wraps an io.Reader with a filename so the OpenAI SDK sends the correct Content-Disposition.
type namedReader struct {
	io.Reader
	name string
}

func (n namedReader) Name() string { return n.name }

func HandleTranscribeAudio(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: transcribe audio")
	if r.Method != http.MethodPost {
		http.Error(w, "invalid method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "audio file required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ctx, span := telemetry.StartSpan(r.Context(), "llm.audio_transcription")
	span.SetAttributes(attribute.String("llm.model", string(openai.AudioModelWhisper1)))
	defer span.End()

	client := OpenAIclient()
	transcription, err := client.Audio.Transcriptions.New(ctx, openai.AudioTranscriptionNewParams{
		File:  namedReader{Reader: file, name: header.Filename},
		Model: openai.AudioModelWhisper1,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		slog.Error("transcription failed", "error", err)
		http.Error(w, "transcription failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"text": transcription.Text}); err != nil {
		slog.Error("Error encoding JSON response", "error", err)
	}
}

func HandleGenerateRecipe(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: generate recipe")
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
	language := r.FormValue("language")

	file, _, err := r.FormFile("image")
	var image multipart.File
	if err == nil {
		defer file.Close()
		image = file
	}

	source := "text"
	if image != nil {
		source = "image"
	} else if url != "" {
		source = "url"
	}
	slog.Debug("handler: generate recipe source", "source", source)

	if prompt != "" && !isRecipeRelated(r.Context(), prompt, "") {
		http.Error(w, "Not a cooking-related request", http.StatusUnprocessableEntity)
		return
	}

	input := model.GenerateRecipeInput{
		Prompt:            prompt,
		URL:               url,
		Image:             image,
		PreferredLanguage: language,
	}

	start := time.Now()
	doc, err := GenerateRecipe(input)
	telemetry.RecordGenerate(r.Context(), source, time.Since(start).Milliseconds(), err == nil)
	if err != nil {
		slog.Error("Error generating recipe", "error", err)
		http.Error(w, "Error generating recipe", http.StatusInternalServerError)
		return
	}

	resp := model.Recipe{
		Recipename: doc.Title,
		Recipe:     RenderMarkdown(doc),
		Structured: &doc,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		slog.Error("Error encoding JSON response", "error", err)
		return
	}
}

func HandleAddRecipe(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: add recipe")
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	var req struct {
		Recipename string                  `json:"recipename"`
		Recipe     string                  `json:"recipe"`
		Tags       []string                `json:"tags,omitempty"`
		Structured *model.RecipeDocument   `json:"structured,omitempty"`
		Origin     *model.GenerationOrigin `json:"origin,omitempty"`
		History    []model.EditTurn        `json:"history,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.Recipename == "" || req.Recipe == "" {
		http.Error(w, "Missing recipename or recipe", http.StatusBadRequest)
		return
	}

	slog.Debug("handler: add recipe saving", "user_id", user.ID, "structured", req.Structured != nil, "history", len(req.History))
	var created model.Recipe
	var err error
	if req.Structured != nil {
		content := RenderMarkdown(*req.Structured)
		created, err = storage.AddRecipeStructured(r.Context(), user.ID, *req.Structured, content, req.Origin, req.History, string(recipeModel), promptVersion)
		telemetry.RecordSave(r.Context(), max(0, len(req.History)-1), err == nil)
	} else {
		if len(req.Tags) == 0 {
			req.Tags = classifyTags(r.Context(), req.Recipe)
		}
		created, err = storage.AddRecipe(r.Context(), user.ID, req.Recipename, req.Recipe, req.Tags)
		telemetry.RecordSave(r.Context(), 0, err == nil)
	}
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

// HandleSuggestInput returns a short LLM-generated sentence completion for the input box.
func HandleSuggestInput(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Input) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Input) > 300 {
		req.Input = req.Input[:300]
	}
	if !isRecipeRelated(r.Context(), req.Input, "") {
		http.Error(w, "Not a cooking-related request", http.StatusUnprocessableEntity)
		return
	}
	suggestion, err := suggestCompletion(r.Context(), req.Input)
	if err != nil {
		slog.Warn("suggest completion failed", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Suggestion string `json:"suggestion"`
	}{Suggestion: suggestion})
}

func suggestCompletion(ctx context.Context, input string) (string, error) {
	return goopenAIChatCompletion(ctx,
		"You are a recipe assistant. Complete the user's dish description naturally in 3-8 words. Output only the completion — not the original text, no quotes, no punctuation at the start.",
		input,
		string(assistModel),
		"suggest_input",
	)
}

// PrefQuestion is a single contextual clarifying question returned by /suggest-prefs.
type PrefQuestion struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Options []string `json:"options"`
}

const suggestPrefsSystemPrompt = `You are a recipe assistant. Given a partial dish description, suggest 2–3 short clarifying questions to personalise the recipe.

Return ONLY a JSON array (no markdown, no explanation):
[{"id":"snake_case","label":"Short label","type":"either-or","options":["A","B","C"]}]

Rules:
- "type" is "either-or" (pick one) or "add-on" (pick many)
- 2-3 questions max, 2-4 options each, option text ≤ 3 words
- Never ask about something already stated in the input
- If input contains "vegan", omit vegetarian/dairy options (already implied)
- Always include a servings question unless the user stated a quantity
- Focus on what actually changes this specific dish (cut of meat, cooking method, sauce style…)

Examples
Input: steak with bearnaise
Output: [{"id":"servings","label":"Servings","type":"either-or","options":["1-2","3-4","5+"]},{"id":"cut","label":"Which cut?","type":"either-or","options":["Ribeye","Filet mignon","NY strip"]},{"id":"doneness","label":"Doneness","type":"either-or","options":["Rare","Medium-rare","Well done"]}]

Input: vegan cheesecake
Output: [{"id":"servings","label":"Servings","type":"either-or","options":["1-2","3-4","5+"]},{"id":"crust","label":"Crust type","type":"either-or","options":["Graham cracker","Oreo","Nut-based"]}]

Input: quick pasta for 4
Output: [{"id":"protein","label":"Protein","type":"either-or","options":["Chicken","Shrimp","Vegetarian"]},{"id":"sauce","label":"Sauce style","type":"either-or","options":["Tomato","Cream","Olive oil"]}]`

// HandleSuggestPrefs returns contextual clarifying questions for the current input.
func HandleSuggestPrefs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Input) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Input) > 300 {
		req.Input = req.Input[:300]
	}
	if !isRecipeRelated(r.Context(), req.Input, "") {
		http.Error(w, "Not a cooking-related request", http.StatusUnprocessableEntity)
		return
	}
	raw, err := goopenAIChatCompletion(r.Context(), suggestPrefsSystemPrompt, req.Input, string(assistModel), "suggest_prefs")
	if err != nil {
		slog.Warn("suggest-prefs failed", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Strip markdown code fences if model wraps the JSON
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "["); idx >= 0 {
		raw = raw[idx:]
	}
	if idx := strings.LastIndex(raw, "]"); idx >= 0 {
		raw = raw[:idx+1]
	}
	var questions []PrefQuestion
	if err := json.Unmarshal([]byte(raw), &questions); err != nil {
		slog.Warn("suggest-prefs parse failed", "error", err, "raw", raw)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Questions []PrefQuestion `json:"questions"`
	}{Questions: questions})
}

// HandleCookingChat answers cooking questions with full recipe context — handles both step-specific
// and general questions (e.g. "what if I forgot the eggs?").
func HandleCookingChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RecipeName string `json:"recipe_name"`
		Recipe     string `json:"recipe"`
		Question   string `json:"question"`
		StepText   string `json:"step_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Question) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Question) > 500 {
		req.Question = req.Question[:500]
	}
	if !isRecipeRelated(r.Context(), req.Question, "") {
		http.Error(w, "Not a cooking-related request", http.StatusUnprocessableEntity)
		return
	}
	recipeCtx := req.Recipe
	if len(recipeCtx) > 2000 {
		recipeCtx = recipeCtx[:2000]
	}
	sys := fmt.Sprintf("You are a helpful cooking assistant. The user is currently cooking \"%s\". Answer their question concisely and practically (2-4 sentences).", req.RecipeName)
	if recipeCtx != "" {
		sys += "\n\nRecipe:\n" + recipeCtx
	}
	if step := strings.TrimSpace(req.StepText); step != "" {
		sys += "\n\nCurrent step: " + step
	}
	answer, err := goopenAIChatCompletion(r.Context(), sys, req.Question, string(assistModel), "cooking_chat")
	if err != nil {
		slog.Warn("cooking-chat llm failed", "error", err)
		http.Error(w, "failed to answer", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Answer string `json:"answer"`
	}{Answer: strings.TrimSpace(answer)})
}

// HandleRefineRecipe applies a natural-language instruction to a recipe and returns a
// new structured document. The full conversation — the original generation ask, the
// document it produced, and every accepted edit since — is threaded into the LLM call
// as real multi-turn history so each new edit stays consistent with everything before
// it, instead of only seeing the current recipe text and the latest instruction.
func HandleRefineRecipe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Origin       model.GenerationOrigin `json:"origin"`
		Initial      model.RecipeDocument   `json:"initial"`
		History      []model.EditTurn       `json:"history"`
		ChangePrompt string                 `json:"change_prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.ChangePrompt) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.ChangePrompt) > 500 {
		req.ChangePrompt = req.ChangePrompt[:500]
	}
	currentDoc := req.Initial
	if n := len(req.History); n > 0 {
		currentDoc = req.History[n-1].Structured
	}
	judgeContext := buildOriginUserText(req.Origin)
	for _, turn := range req.History {
		judgeContext += "\n\nPrior change: " + turn.ChangePrompt
	}
	judgeContext += "\n\nCurrent recipe:\n" + RenderMarkdown(currentDoc)
	if !isRecipeRelated(r.Context(), req.ChangePrompt, judgeContext) {
		http.Error(w, "Not a cooking-related request", http.StatusUnprocessableEntity)
		return
	}

	messages, err := buildRefineMessages(req.Origin, req.Initial, req.History, req.ChangePrompt)
	if err != nil {
		slog.Error("refine-recipe: build messages failed", "error", err)
		http.Error(w, "failed to refine recipe", http.StatusInternalServerError)
		return
	}

	start := time.Now()
	result, err := generateRefineFromMessages(r.Context(), messages)
	if err != nil {
		telemetry.RecordRefine(r.Context(), time.Since(start).Milliseconds(), "error")
		slog.Error("refine-recipe failed", "error", err)
		http.Error(w, "failed to refine recipe", http.StatusInternalServerError)
		return
	}
	telemetry.RecordRefine(r.Context(), time.Since(start).Milliseconds(), result.Status)
	slog.InfoContext(r.Context(), "refine outcome", "status", result.Status, "change_prompt", req.ChangePrompt)

	resp := struct {
		Status     string                `json:"status"`
		Message    *string               `json:"message,omitempty"`
		Options    []string              `json:"options,omitempty"`
		Recipename string                `json:"recipename,omitempty"`
		Recipe     string                `json:"recipe,omitempty"`
		Structured *model.RecipeDocument `json:"structured,omitempty"`
	}{
		Status:  result.Status,
		Message: result.Message,
		Options: result.Options,
	}
	if result.Status == "applied" && result.Recipe != nil {
		resp.Recipename = result.Recipe.Title
		resp.Recipe = RenderMarkdown(*result.Recipe)
		resp.Structured = result.Recipe
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
