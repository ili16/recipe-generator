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
	"github.com/ili16/recipe-generator/internal/billing"
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

// recordCost estimates and persists the USD cost of one LLM call for the authenticated
// caller. No-ops for anonymous requests — there's no user to attach cost to.
func recordCost(ctx context.Context, callType, model string, promptTokens, completionTokens int64) {
	recordCostUSD(ctx, callType, model, billing.CostUSD(model, promptTokens, completionTokens), promptTokens, completionTokens)
}

func recordCostUSD(ctx context.Context, callType, model string, costUSD float64, promptTokens, completionTokens int64) {
	u, ok := auth.FromContext(ctx)
	if !ok {
		return
	}
	if err := storage.RecordUsage(ctx, u.ID, callType, model, promptTokens, completionTokens, costUSD); err != nil {
		slog.Warn("record usage failed", "error", err)
	}
}

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

	recordCostUSD(r.Context(), "transcribe", string(openai.AudioModelWhisper1), billing.TranscribeCostUSD(), 0, 0)

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

	// Track this generation's eventual outcome (saved/refined/declined/abandoned) for
	// authenticated callers only — anonymous requests have no user to attach it to,
	// same policy as cost tracking (recordCost above).
	if u, ok := auth.FromContext(r.Context()); ok {
		if genID, genErr := storage.CreateGenerationEvent(r.Context(), u.ID, source, prompt); genErr == nil {
			resp.GenerationID = genID
		} else {
			slog.Warn("create generation event failed", "error", genErr)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		slog.Error("Error encoding JSON response", "error", err)
		return
	}
}

// writeSSE writes one Server-Sent Event, JSON-encoding payload so arbitrary text
// (newlines, quotes) can never break the "data:" line framing, and flushes immediately
// so the client sees it without buffering. Mirrors cmd/mealplan.go's writeSSE — kept as
// a separate copy since that one lives in package main and this handler needs the
// service-package helpers (isRecipeRelated, recordCost, storage.CreateGenerationEvent).
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Error encoding SSE payload", "error", err)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

// HandleGenerateRecipeStream mirrors HandleGenerateRecipe but streams the LLM's raw
// JSON output over SSE as it's generated (event: delta), then a final event: done
// carrying the same response shape HandleGenerateRecipe returns in one shot today, or
// event: error on failure. Web-only on the frontend (native fetch can't consume a
// streaming body), which falls back to plain POST /generate.
func HandleGenerateRecipeStream(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: generate recipe stream")
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
	slog.Debug("handler: generate recipe stream source", "source", source)

	if prompt != "" && !isRecipeRelated(r.Context(), prompt, "") {
		http.Error(w, "Not a cooking-related request", http.StatusUnprocessableEntity)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	input := model.GenerateRecipeInput{
		Prompt:            prompt,
		URL:               url,
		Image:             image,
		PreferredLanguage: language,
	}

	start := time.Now()
	doc, err := GenerateRecipeStream(input, func(delta string) { writeSSE(w, flusher, "delta", delta) })
	telemetry.RecordGenerate(r.Context(), source, time.Since(start).Milliseconds(), err == nil)
	if err != nil {
		slog.Error("Error generating recipe (stream)", "error", err)
		writeSSE(w, flusher, "error", err.Error())
		return
	}

	resp := model.Recipe{
		Recipename: doc.Title,
		Recipe:     RenderMarkdown(doc),
		Structured: &doc,
	}
	if u, ok := auth.FromContext(r.Context()); ok {
		if genID, genErr := storage.CreateGenerationEvent(r.Context(), u.ID, source, prompt); genErr == nil {
			resp.GenerationID = genID
		} else {
			slog.Warn("create generation event failed", "error", genErr)
		}
	}
	writeSSE(w, flusher, "done", resp)
}

func HandleAddRecipe(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: add recipe")
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	var req struct {
		Recipename        string                  `json:"recipename"`
		Recipe            string                  `json:"recipe"`
		Tags              []string                `json:"tags,omitempty"`
		Structured        *model.RecipeDocument   `json:"structured,omitempty"`
		Origin            *model.GenerationOrigin `json:"origin,omitempty"`
		History           []model.EditTurn        `json:"history,omitempty"`
		GenerationID      string                  `json:"generation_id,omitempty"`
		VariantOfRecipeID *int                    `json:"variant_of_recipe_id,omitempty"`
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
		created, err = storage.AddRecipeStructured(r.Context(), user.ID, *req.Structured, content, req.Origin, req.History, string(recipeModel), promptVersion, req.VariantOfRecipeID)
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

	if req.GenerationID != "" {
		createdID := created.ID
		if resolveErr := storage.Resolve(r.Context(), user.ID, req.GenerationID, "saved", &createdID); resolveErr == nil {
			telemetry.RecordGenerationOutcome(r.Context(), "saved", 1)
		} else {
			slog.Warn("resolve generation event failed", "error", resolveErr)
		}
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
	mode, text, err := suggestCompletion(r.Context(), req.Input)
	if err != nil {
		slog.Warn("suggest completion failed", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Mode string `json:"mode"`
		Text string `json:"text"`
	}{Mode: mode, Text: text})
}

const suggestInputSystemPrompt = `You are a recipe assistant helping someone type a dish description into a single input box. Decide ONE of three modes based on how complete the current input already is, then respond with ONLY JSON (no markdown, no explanation): {"mode":"append"|"rewrite"|"none","text":"..."}

- "append": input is short or clearly unfinished — "text" is a 3-8 word natural continuation. Not the original text, no quotes, no leading punctuation.
- "rewrite": input has some substance but is rambling, vague, or poorly organized — "text" is the WHOLE input rewritten as one clear, well-structured, more detailed dish description (not a continuation of it).
- "none": input is already a clear, sufficiently detailed dish description — "text" is "".

Examples
Input: chicken curry
Output: {"mode":"append","text":"with coconut milk and rice"}

Input: that thing my mom makes with like chicken and rice and it's kinda spicy idk theres a sauce too
Output: {"mode":"rewrite","text":"Spicy chicken and rice with a creamy homestyle sauce"}

Input: creamy garlic butter shrimp pasta with parmesan and fresh basil, for 4 servings
Output: {"mode":"none","text":""}`

func suggestCompletion(ctx context.Context, input string) (string, string, error) {
	raw, err := goopenAIChatCompletion(ctx, suggestInputSystemPrompt, input, string(assistModel), "suggest_input")
	if err != nil {
		return "", "", err
	}
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "{"); idx >= 0 {
		raw = raw[idx:]
	}
	if idx := strings.LastIndex(raw, "}"); idx >= 0 {
		raw = raw[:idx+1]
	}
	var resp struct {
		Mode string `json:"mode"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return "", "", err
	}
	return resp.Mode, resp.Text, nil
}

// PrefQuestion is a single contextual clarifying question returned by /suggest-prefs.
type PrefQuestion struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Options []string `json:"options"`
}

const suggestPrefsSystemPrompt = `You are a recipe assistant helping someone in a single input box meant for describing a dish they want to cook.

First decide: is this input a dish description, even partial or vague ("steak", "vegan cheesecake", "quick pasta for 4")? Or is it a conversational, open-ended message ("surprise me", "what can I cook with pork", "I don't know what to make", a question)?

Respond with ONLY JSON (no markdown, no explanation), one of:
- Dish description: {"mode":"prefs","questions":[{"id":"snake_case","label":"Short label","type":"either-or","options":["A","B","C"]}]}
- Conversational message: {"mode":"chat","reply":"..."}

Rules for "prefs" mode:
- "type" is "either-or" (pick one) or "add-on" (pick many)
- 2-3 questions max, 2-4 options each, option text ≤ 3 words
- Never ask about something already stated in the input
- If input contains "vegan", omit vegetarian/dairy options (already implied)
- Always include a servings question unless the user stated a quantity
- Focus on what actually changes this specific dish (cut of meat, cooking method, sauce style…)

Rules for "chat" mode:
- "reply" is 1-3 short, friendly sentences — like a helpful friend, not a formal assistant
- If they name an ingredient or constraint, suggest 2-3 concrete dishes by name
- If it's fully open ("surprise me"), suggest 2-3 varied concrete dish ideas
- If a conversation history is included, don't repeat a dish already suggested
- Never refuse — steer back to cooking helpfully even if the message is only loosely related

Examples
Input: steak with bearnaise
Output: {"mode":"prefs","questions":[{"id":"servings","label":"Servings","type":"either-or","options":["1-2","3-4","5+"]},{"id":"cut","label":"Which cut?","type":"either-or","options":["Ribeye","Filet mignon","NY strip"]},{"id":"doneness","label":"Doneness","type":"either-or","options":["Rare","Medium-rare","Well done"]}]}

Input: vegan cheesecake
Output: {"mode":"prefs","questions":[{"id":"servings","label":"Servings","type":"either-or","options":["1-2","3-4","5+"]},{"id":"crust","label":"Crust type","type":"either-or","options":["Graham cracker","Oreo","Nut-based"]}]}

Input: quick pasta for 4
Output: {"mode":"prefs","questions":[{"id":"protein","label":"Protein","type":"either-or","options":["Chicken","Shrimp","Vegetarian"]},{"id":"sauce","label":"Sauce style","type":"either-or","options":["Tomato","Cream","Olive oil"]}]}

Input: surprise me
Output: {"mode":"chat","reply":"Feeling adventurous? How about a Thai green curry, a sheet-pan salmon with roasted veggies, or a classic French onion soup — any of those sound good?"}

Input: what can I cook with pork
Output: {"mode":"chat","reply":"Pork's versatile! Pulled pork sandwiches, a garlicky pork stir-fry, or pork chops with apple sauce are all great options — what are you in the mood for?"}`

// ChatTurn is one message in an in-progress /suggest-prefs conversation.
type ChatTurn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// HandleSuggestPrefs returns contextual clarifying questions for a dish description, or —
// when the input reads as conversational/open-ended rather than a dish description — a
// chat-style reply instead. Once a caller is in chat mode it keeps sending the accumulated
// history so follow-up turns stay in context.
func HandleSuggestPrefs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input   string     `json:"input"`
		History []ChatTurn `json:"history"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Input) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Input) > 300 {
		req.Input = req.Input[:300]
	}
	userMsg := req.Input
	if len(req.History) > 0 {
		var b strings.Builder
		for _, t := range req.History {
			fmt.Fprintf(&b, "%s: %s\n", t.Role, t.Text)
		}
		fmt.Fprintf(&b, "user: %s", req.Input)
		userMsg = b.String()
	}
	raw, err := goopenAIChatCompletion(r.Context(), suggestPrefsSystemPrompt, userMsg, string(assistModel), "suggest_prefs")
	if err != nil {
		slog.Warn("suggest-prefs failed", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Strip markdown code fences if model wraps the JSON
	raw = strings.TrimSpace(raw)
	if idx := strings.Index(raw, "{"); idx >= 0 {
		raw = raw[idx:]
	}
	if idx := strings.LastIndex(raw, "}"); idx >= 0 {
		raw = raw[:idx+1]
	}
	var resp struct {
		Mode      string         `json:"mode"`
		Questions []PrefQuestion `json:"questions"`
		Reply     string         `json:"reply"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		slog.Warn("suggest-prefs parse failed", "error", err, "raw", raw)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
		GenerationID string                 `json:"generation_id,omitempty"`
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

	// Flag the generation as having had a refinement — not a terminal funnel outcome by
	// itself (the user might still save or decline), so no telemetry here; the
	// background sweep later resolves it into "refined" or "abandoned" if it stays
	// pending. Anonymous callers have no generation_events row to update.
	if req.GenerationID != "" && result.Status == "applied" {
		if u, ok := auth.FromContext(r.Context()); ok {
			if err := storage.MarkRefined(r.Context(), u.ID, req.GenerationID); err != nil {
				slog.Warn("mark generation refined failed", "error", err)
			}
		}
	}

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
