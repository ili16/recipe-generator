package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/billing"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/ratelimit"
	"github.com/ili16/recipe-generator/internal/service"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/ili16/recipe-generator/internal/worker"
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
	worker.Start(time.Duration(envIntOrDefault("WORKER_INTERVAL_MINUTES", 60)) * time.Minute)

	authenticator, err := auth.New(auth.ConfigFromEnv(), storage.Users{})
	if err != nil {
		slog.Error("Failed to initialize authenticator", "error", err)
		os.Exit(1)
	}

	// rl guards the real generation endpoints: a handful of recipes per short window is
	// plenty for a real user, but stops a script hammering the endpoint.
	rl := ratelimit.New(
		envIntOrDefault("RATE_LIMIT_ANON_PER_10MIN", 5),
		envIntOrDefault("RATE_LIMIT_AUTH_PER_10MIN", 15),
		10*time.Minute,
	)
	// llm wraps an LLM-calling handler with rate limiting and budget enforcement, seeing
	// the caller's identity (if any) via authenticator.Optional first.
	llm := func(rl *ratelimit.Limiter, h http.HandlerFunc) http.HandlerFunc {
		return authenticator.Optional(rl.Guard(billing.Guard(h)))
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", HandleHealth)

	mux.HandleFunc("POST /api/v1/transcribe", llm(rl, service.HandleTranscribeAudio))
	// The one conversational endpoint (specs/AGENT_CHAT.md §2). Every per-capability
	// AI route it replaced is gone (BACKLOG.md 3.5, 3.9, 3.13).
	mux.HandleFunc("POST /api/v1/chat", llm(rl, service.HandleChat))

	mux.HandleFunc("GET /api/v1/recipes/trash", authenticator.Require(HandleGetTrash))
	mux.HandleFunc("POST /api/v1/recipes/{id}/restore", authenticator.Require(HandleRestoreRecipe))
	mux.HandleFunc("GET /api/v1/recipes/{id}", authenticator.Require(HandleGetRecipeByID))
	mux.HandleFunc("GET /api/v1/recipes/{id}/history", authenticator.Require(HandleGetRecipeHistory))

	mux.HandleFunc("GET /api/v1/user-info", authenticator.Require(HandleGetUserInfo))
	mux.HandleFunc("GET /api/v1/get-recipes", authenticator.Require(HandleGetRecipes))
	mux.HandleFunc("POST /api/v1/add-recipe", authenticator.Require(service.HandleAddRecipe))
	mux.HandleFunc("DELETE /api/v1/delete-recipe/{id}", authenticator.Require(HandleDeleteRecipe))
	mux.HandleFunc("PATCH /api/v1/update-recipe", authenticator.Require(HandleUpdateRecipe))
	mux.HandleFunc("GET /api/v1/usage", authenticator.Require(HandleGetUsage))
	mux.HandleFunc("GET /api/v1/preferences", authenticator.Require(HandleGetPreferences))
	mux.HandleFunc("PATCH /api/v1/preferences", authenticator.Require(HandlePatchPreferences))
	mux.HandleFunc("PUT /api/v1/recipes/{id}/vote", authenticator.Require(HandleVoteRecipe))
	mux.HandleFunc("DELETE /api/v1/recipes/{id}/vote", authenticator.Require(HandleUnvoteRecipe))
	mux.HandleFunc("PUT /api/v1/recipes/{id}/cooked", authenticator.Require(HandleMarkCooked))
	mux.HandleFunc("DELETE /api/v1/recipes/{id}/cooked", authenticator.Require(HandleUnmarkCooked))
	mux.HandleFunc("POST /api/v1/recipes/{id}/share", authenticator.Require(HandleShareRecipe))
	mux.HandleFunc("DELETE /api/v1/recipes/{id}/share", authenticator.Require(HandleUnshareRecipe))
	// Deliberately unauthenticated (BACKLOG 8.4) — the token is the credential.
	mux.HandleFunc("GET /api/v1/shared/{token}", HandleGetSharedRecipe)
	mux.HandleFunc("GET /api/v1/collections", authenticator.Require(HandleGetCollections))
	mux.HandleFunc("POST /api/v1/collections", authenticator.Require(HandleCreateCollection))
	mux.HandleFunc("DELETE /api/v1/collections/{id}", authenticator.Require(HandleDeleteCollection))
	mux.HandleFunc("PUT /api/v1/collections/{id}/recipes/{recipeId}", authenticator.Require(HandleSetCollectionMembership))
	mux.HandleFunc("DELETE /api/v1/collections/{id}/recipes/{recipeId}", authenticator.Require(HandleSetCollectionMembership))

	mux.HandleFunc("GET /api/v1/meal-plan", authenticator.Require(HandleGetMealPlan))
	mux.HandleFunc("GET /api/v1/grocery-list", authenticator.Require(HandleGetGroceryList))
	mux.HandleFunc("POST /api/v1/meal-plan/items", authenticator.Require(HandleAddMealPlanItem))
	mux.HandleFunc("DELETE /api/v1/meal-plan/items/{id}", authenticator.Require(HandleDeleteMealPlanItem))
	mux.HandleFunc("POST /api/v1/meal-plan/variants/{id}", llm(rl, HandleGenerateVariant))

	mux.HandleFunc("GET /api/v1/admin/users", authenticator.RequireAdmin(HandleAdminListUsers))
	mux.HandleFunc("PATCH /api/v1/admin/users/{id}/budget", authenticator.RequireAdmin(HandleAdminSetBudget))

	addr := ":" + envOrDefault("PORT", "8080")
	handler := otelhttp.NewHandler(withCORS(logRequests(mux)), "recipe-generator")
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		// ReadHeaderTimeout guards against a slowloris client trickling in headers.
		// WriteTimeout is generous rather than tight, since /generate/stream and
		// /chat hold the response open for the whole SSE duration.
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("Server is running", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	<-stop
	slog.Info("Shutting down, draining in-flight requests")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Graceful shutdown failed", "error", err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
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

// Flush forwards to the underlying ResponseWriter's Flusher, if it has one. Without
// this, streaming handlers (SSE) doing `w.(http.Flusher)` behind logRequests would
// fail the type assertion — statusRecorder embeds the http.ResponseWriter interface,
// which doesn't include Flush, so it isn't promoted automatically.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
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

// HandleGetUsage returns the caller's own current-month LLM spend and budget cap, so the
// frontend can render an 80%/100% warning before the caller hits the enforced 429.
func HandleGetUsage(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	spent, cap, err := billing.CurrentUsage(r.Context(), user.ID)
	if err != nil {
		slog.Error("Error getting usage", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	pct := 0.0
	if cap > 0 {
		pct = spent / cap
	}
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	writeJSON(w, http.StatusOK, map[string]any{
		"spent_usd":    spent,
		"cap_usd":      cap,
		"period_start": periodStart.Format(time.RFC3339),
		"pct_used":     pct,
	})
}

// dietaryPrefSlugs is the subset of service.AllTagSlugs valid as a user's own dietary
// preference (as opposed to the full recipe-tagging vocabulary, which also covers meal
// type, cooking method, etc. — not meaningful as a standing user preference).
var dietaryPrefSlugs = map[string]bool{
	"vegan": true, "vegetarian": true, "gluten-free": true, "dairy-free": true,
	"nut-free": true, "keto": true, "low-carb": true,
}

// weekdayNames validates UserPreferences.MealPlanNoFoodDays/MealPlanNoCookDays/
// WeekStartDay entries.
var weekdayNames = map[string]bool{
	"monday": true, "tuesday": true, "wednesday": true, "thursday": true,
	"friday": true, "saturday": true, "sunday": true,
}

// HandleGetPreferences returns the caller's explicit profile settings.
func HandleGetPreferences(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	prefs, err := storage.GetPreferences(r.Context(), user.ID)
	if err != nil {
		slog.Error("Error getting preferences", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// HandlePatchPreferences overwrites the caller's explicit profile settings.
func HandlePatchPreferences(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	var prefs model.UserPreferences
	if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	if prefs.SkillLevel != nil {
		switch *prefs.SkillLevel {
		case "beginner", "intermediate", "advanced":
		default:
			http.Error(w, "invalid skill_level", http.StatusBadRequest)
			return
		}
	}
	for _, slug := range prefs.DietaryPrefs {
		if !dietaryPrefSlugs[slug] {
			http.Error(w, "invalid dietary_prefs entry: "+slug, http.StatusBadRequest)
			return
		}
	}
	for _, day := range prefs.MealPlanNoFoodDays {
		if !weekdayNames[day] {
			http.Error(w, "invalid meal_plan_no_food_days entry: "+day, http.StatusBadRequest)
			return
		}
	}
	noFood := make(map[string]bool, len(prefs.MealPlanNoFoodDays))
	for _, day := range prefs.MealPlanNoFoodDays {
		noFood[day] = true
	}
	for _, day := range prefs.MealPlanNoCookDays {
		if !weekdayNames[day] {
			http.Error(w, "invalid meal_plan_no_cook_days entry: "+day, http.StatusBadRequest)
			return
		}
		// A day in both lists is contradictory: no_food says leave it empty, no_cook says
		// carry the prior dish forward onto it, and enforceNoCookCarryover would write an
		// assignment onto a day it was told to skip.
		if noFood[day] {
			http.Error(w, "day is in both meal_plan_no_food_days and meal_plan_no_cook_days: "+day, http.StatusBadRequest)
			return
		}
	}
	switch prefs.MealPlanBatchDays {
	case 1, 2, 3:
	default:
		http.Error(w, "meal_plan_batch_days must be 1, 2, or 3", http.StatusBadRequest)
		return
	}
	if prefs.WeekStartDay == "" {
		prefs.WeekStartDay = "monday"
	} else if !weekdayNames[prefs.WeekStartDay] {
		http.Error(w, "invalid week_start_day", http.StatusBadRequest)
		return
	}
	if err := storage.UpdatePreferences(r.Context(), user.ID, prefs); err != nil {
		slog.Error("Error updating preferences", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// HandleVoteRecipe upserts the caller's thumbs up(+1)/down(-1) vote on a recipe they own.
func HandleVoteRecipe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "invalid recipe id", http.StatusBadRequest)
		return
	}
	var req struct {
		Vote int16 `json:"vote"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Vote != 1 && req.Vote != -1) {
		http.Error(w, "vote must be 1 or -1", http.StatusBadRequest)
		return
	}
	found, err := storage.SetVote(r.Context(), user.ID, recipeID, req.Vote)
	if err != nil {
		slog.Error("Error setting vote", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "recipe not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleUnvoteRecipe removes the caller's vote on a recipe, if any.
func HandleUnvoteRecipe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "invalid recipe id", http.StatusBadRequest)
		return
	}
	if err := storage.RemoveVote(r.Context(), user.ID, recipeID); err != nil {
		slog.Error("Error removing vote", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleMarkCooked stamps a recipe the caller owns as cooked now (BACKLOG 6.3), which
// holds it back from meal-plan candidates for service.CookedCooldown.
func HandleMarkCooked(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "invalid recipe id", http.StatusBadRequest)
		return
	}
	found, err := storage.MarkCooked(r.Context(), user.ID, recipeID)
	if err != nil {
		slog.Error("Error marking cooked", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "recipe not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleShareRecipe turns on the read-only share link for a recipe the caller owns and
// returns its token (BACKLOG 8.4). Re-sharing returns the same token.
func HandleShareRecipe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "invalid recipe id", http.StatusBadRequest)
		return
	}
	token, err := storage.ShareRecipe(r.Context(), user.ID, recipeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "recipe not found", http.StatusNotFound)
			return
		}
		slog.Error("Error sharing recipe", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"share_token": token})
}

// HandleUnshareRecipe revokes the link; the old token stops resolving immediately.
func HandleUnshareRecipe(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "invalid recipe id", http.StatusBadRequest)
		return
	}
	if err := storage.UnshareRecipe(r.Context(), user.ID, recipeID); err != nil {
		slog.Error("Error unsharing recipe", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleGetSharedRecipe serves a shared recipe to anyone holding the token — no auth.
// An unknown, revoked or trashed token is a 404, same as a recipe that was never shared.
func HandleGetSharedRecipe(w http.ResponseWriter, r *http.Request) {
	recipe, err := storage.GetSharedRecipe(r.Context(), r.PathValue("token"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("Error getting shared recipe", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The reader is not the owner: strip the owner-only signals GetRecipeByID carries.
	recipe.MyVote = nil
	writeJSON(w, http.StatusOK, recipe)
}

// HandleUnmarkCooked clears the caller's cooked mark on a recipe, if any.
func HandleUnmarkCooked(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "invalid recipe id", http.StatusBadRequest)
		return
	}
	if err := storage.UnmarkCooked(r.Context(), user.ID, recipeID); err != nil {
		slog.Error("Error unmarking cooked", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleAdminListUsers returns every user with their current-month spend and effective
// budget cap. Admin-only (authenticator.RequireAdmin).
func HandleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := storage.ListUsersWithUsage(r.Context(), billing.DefaultCapUSD())
	if err != nil {
		slog.Error("Error listing users with usage", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// HandleAdminSetBudget sets or clears a user's monthly budget override. Admin-only.
func HandleAdminSetBudget(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || userID <= 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}

	var req struct {
		MonthlyBudgetUSD *float64 `json:"monthly_budget_usd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	found, err := storage.SetBudgetOverride(r.Context(), userID, req.MonthlyBudgetUSD)
	if err != nil {
		slog.Error("Error setting budget override", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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

// HandleGetTrash lists the caller's soft-deleted recipes (BACKLOG 8.2).
func HandleGetTrash(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	recipes, err := storage.GetTrashedRecipes(r.Context(), user.ID)
	if err != nil {
		slog.Error("Error getting trash", "error", err)
		http.Error(w, "Error getting trash", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, recipes)
}

// HandleRestoreRecipe un-deletes a trashed recipe. A id that isn't a trashed recipe of
// the caller's — another user's, or one already restored — is a 404.
func HandleRestoreRecipe(w http.ResponseWriter, r *http.Request) {
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

	if err := storage.RestoreRecipe(r.Context(), user.ID, recipeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Recipe not found", http.StatusNotFound)
			return
		}
		slog.Error("Error restoring recipe", "error", err)
		http.Error(w, "Error restoring recipe", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

/* ── Collections (BACKLOG 8.1) ─────────────────────────────────────────────── */

func HandleGetCollections(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	collections, err := storage.ListCollections(r.Context(), user.ID)
	if err != nil {
		slog.Error("Error listing collections", "error", err)
		http.Error(w, "Error listing collections", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, collections)
}

func HandleCreateCollection(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	collection, err := storage.CreateCollection(r.Context(), user.ID, req.Name)
	if err != nil {
		if errors.Is(err, storage.ErrEmptyCollectionName) {
			http.Error(w, "Collection name is required", http.StatusBadRequest)
			return
		}
		slog.Error("Error creating collection", "error", err)
		http.Error(w, "Error creating collection", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, collection)
}

func HandleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	collectionID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || collectionID <= 0 {
		http.Error(w, "Invalid collection id", http.StatusBadRequest)
		return
	}

	if err := storage.DeleteCollection(r.Context(), user.ID, collectionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Collection not found", http.StatusNotFound)
			return
		}
		slog.Error("Error deleting collection", "error", err)
		http.Error(w, "Error deleting collection", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleSetCollectionMembership serves both PUT (add) and DELETE (remove) on
// /collections/{id}/recipes/{recipeId} — same ownership check, same 404, one handler.
func HandleSetCollectionMembership(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	collectionID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || collectionID <= 0 {
		http.Error(w, "Invalid collection id", http.StatusBadRequest)
		return
	}
	recipeID, err := strconv.Atoi(r.PathValue("recipeId"))
	if err != nil || recipeID <= 0 {
		http.Error(w, "Invalid recipe id", http.StatusBadRequest)
		return
	}

	if err := storage.SetCollectionMembership(r.Context(), user.ID, collectionID, recipeID, r.Method == http.MethodPut); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}
		slog.Error("Error updating collection membership", "error", err)
		http.Error(w, "Error updating collection", http.StatusInternalServerError)
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
