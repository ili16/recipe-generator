package service

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/billing"
	"github.com/ili16/recipe-generator/internal/llm"
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

	client := llm.Client()
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

	llm.RecordCostUSD(r.Context(), "transcribe", string(openai.AudioModelWhisper1), billing.TranscribeCostUSD(), 0, 0)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"text": transcription.Text}); err != nil {
		slog.Error("Error encoding JSON response", "error", err)
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
		Recipename        string                  `json:"recipename"`
		Recipe            string                  `json:"recipe"`
		Structured        *model.RecipeDocument   `json:"structured,omitempty"`
		Origin            *model.GenerationOrigin `json:"origin,omitempty"`
		History           []model.EditTurn        `json:"history,omitempty"`
		VariantOfRecipeID *int                    `json:"variant_of_recipe_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	// structured is required (BACKLOG 3.11). The untyped branch wrote a recipe with no
	// RecipeDocument — the one thing that can create a row cooking mode cannot cook —
	// and nothing has reached it since saving moved into the agent's save_recipe tool.
	if req.Recipename == "" || req.Recipe == "" || req.Structured == nil {
		http.Error(w, "Missing recipename, recipe or structured", http.StatusBadRequest)
		return
	}

	slog.Debug("handler: add recipe saving", "user_id", user.ID, "history", len(req.History))
	content := RenderMarkdown(*req.Structured)
	created, err := storage.AddRecipeStructured(r.Context(), user.ID, *req.Structured, content, req.Origin, req.History, string(recipeModel), promptVersion, req.VariantOfRecipeID)
	telemetry.RecordSave(r.Context(), max(0, len(req.History)-1), err == nil)
	if err == nil {
		LearnGrams(req.Structured.Ingredients)
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
