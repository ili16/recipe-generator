package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/service"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	if os.Getenv("LOG_LEVEL") == "debug" {
		slog.SetLogLoggerLevel(slog.LevelDebug)
	}

	if !validateEnvVars() {
		slog.Error("Missing environment variables")
		os.Exit(1)
	}

	shutdownTelemetry, err := telemetry.Init(context.Background())
	if err != nil {
		slog.Error("Failed to initialize telemetry", "error", err)
		os.Exit(1)
	}
	defer shutdownTelemetry(context.Background())

	storage.InitDBPool()

	authenticator, err := auth.New(auth.ConfigFromEnv(), storage.Users{})
	if err != nil {
		slog.Error("Failed to initialize authenticator", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", HandleHealth)

	mux.HandleFunc("POST /api/v1/generate", service.HandleGenerateRecipe)
	mux.HandleFunc("POST /api/v1/transcribe", service.HandleTranscribeAudio)
	mux.HandleFunc("POST /api/v1/suggest", service.HandleSuggestInput)
	mux.HandleFunc("POST /api/v1/suggest-prefs", service.HandleSuggestPrefs)
	mux.HandleFunc("POST /api/v1/cooking-chat", service.HandleCookingChat)
	mux.HandleFunc("POST /api/v1/refine-recipe", authenticator.Optional(service.HandleRefineRecipe))

	mux.HandleFunc("GET /api/v1/recipes/{id}", authenticator.Require(HandleGetRecipeByID))
	mux.HandleFunc("GET /api/v1/recipes/{id}/history", authenticator.Require(HandleGetRecipeHistory))

	mux.HandleFunc("GET /api/v1/user-info", authenticator.Require(HandleGetUserInfo))
	mux.HandleFunc("GET /api/v1/get-recipes", authenticator.Require(HandleGetRecipes))
	mux.HandleFunc("POST /api/v1/add-recipe", authenticator.Require(service.HandleAddRecipe))
	mux.HandleFunc("DELETE /api/v1/delete-recipe/{id}", authenticator.Require(HandleDeleteRecipe))
	mux.HandleFunc("PATCH /api/v1/update-recipe", authenticator.Require(HandleUpdateRecipe))

	addr := ":" + envOrDefault("PORT", "8080")
	slog.Info("Server is running", "addr", addr)
	handler := otelhttp.NewHandler(withCORS(logRequests(mux)), "recipe-generator")
	if err := http.ListenAndServe(addr, handler); err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func withCORS(next http.Handler) http.Handler {
	origin := os.Getenv("CORS_ORIGIN")
	if origin == "" {
		slog.Warn("CORS_ORIGIN is not set; browser clients will be blocked")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.DebugContext(r.Context(), "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"dur_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// statusRecorder captures the status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func HandleGetUserInfo(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: get user info")
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"userID":   user.ID,
		"oauthID":  user.Subject,
		"email":    user.Email,
		"fullName": user.Name,
		"provider": user.Provider,
	})
}

func validateEnvVars() bool {
	for _, key := range []string{"OPENAI_KEY", "DB_URL", "KEYCLOAK_URL"} {
		if _, found := os.LookupEnv(key); !found {
			slog.Error("Required environment variable missing", "key", key)
			return false
		}
	}
	return true
}

func HandleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "Healthy"})
}

func HandleGetRecipeByID(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "invalid recipe id", http.StatusBadRequest)
		return
	}
	recipe, err := storage.GetRecipeByID(r.Context(), user.ID, recipeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("Error getting recipe by id", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, recipe)
}

func HandleGetRecipeHistory(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "invalid recipe id", http.StatusBadRequest)
		return
	}
	versions, err := storage.GetRecipeVersions(r.Context(), user.ID, recipeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("Error getting recipe history", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, versions)
}

func HandleGetRecipes(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: get recipes")
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	recipes, err := storage.GetRecipes(user.ID)
	if err != nil {
		slog.Error("Error getting recipes", "error", err)
		http.Error(w, "Error getting recipes", http.StatusInternalServerError)
		return
	}
	if recipes == nil {
		recipes = []model.Recipe{}
	}

	writeJSON(w, http.StatusOK, recipes)
}

func HandleDeleteRecipe(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: delete recipe", "id", r.PathValue("id"))
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "Invalid recipe id", http.StatusBadRequest)
		return
	}

	if err := storage.DeleteRecipe(r.Context(), user.ID, recipeID); err != nil {
		slog.Error("Error deleting recipe", "error", err)
		http.Error(w, "Error deleting recipe", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func HandleUpdateRecipe(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handler: update recipe")
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	var updateReq struct {
		ID           int                  `json:"id"`
		Structured   model.RecipeDocument `json:"structured"`
		AISourced    bool                 `json:"ai_sourced,omitempty"`
		ChangePrompt string               `json:"change_prompt,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&updateReq); err != nil {
		slog.Error("Error decoding request body", "error", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if updateReq.ID == 0 || updateReq.Structured.Title == "" {
		http.Error(w, "Missing required fields", http.StatusBadRequest)
		return
	}

	content := service.RenderMarkdown(updateReq.Structured)
	updated, err := storage.UpdateRecipeStructured(r.Context(), user.ID, updateReq.ID, updateReq.Structured, content, !updateReq.AISourced, updateReq.ChangePrompt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Recipe not found", http.StatusNotFound)
			return
		}
		slog.Error("Error updating recipe", "error", err)
		http.Error(w, "Error updating recipe", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("Error encoding JSON response", "error", err)
	}
}
