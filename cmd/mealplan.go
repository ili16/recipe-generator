package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/service"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/jackc/pgx/v5"
)

const dateLayout = "2006-01-02"
const timeLayout = "15:04:05"
const defaultMealStartTime = "18:00:00"
const maxMealPlanRangeDays = 42

// HandleGetMealPlan returns every meal-plan item in [starts_on, ends_on]. ends_on
// defaults to starts_on+6d (a week) when omitted, so the original Week view call
// keeps working unmodified.
func HandleGetMealPlan(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	startsOn, err := time.Parse(dateLayout, r.URL.Query().Get("starts_on"))
	if err != nil {
		http.Error(w, "invalid or missing starts_on (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	endsOn := startsOn.AddDate(0, 0, 6)
	if raw := r.URL.Query().Get("ends_on"); raw != "" {
		endsOn, err = time.Parse(dateLayout, raw)
		if err != nil {
			http.Error(w, "invalid ends_on (YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
	}
	if endsOn.Before(startsOn) || endsOn.Sub(startsOn).Hours()/24 > maxMealPlanRangeDays {
		http.Error(w, "ends_on must be on or after starts_on, within 42 days", http.StatusBadRequest)
		return
	}

	items, err := storage.GetMealPlanRange(r.Context(), user.ID, startsOn, endsOn)
	if err != nil {
		slog.Error("Error getting meal plan range", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"starts_on": startsOn.Format(dateLayout),
		"ends_on":   endsOn.Format(dateLayout),
		"items":     items,
	})
}

// HandleAddMealPlanItem assigns a saved recipe to a day (upsert-by-day — replaces
// whatever was already on that day), creating that week's meal_plans row on first use.
func HandleAddMealPlanItem(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		RecipeID  int    `json:"recipe_id"`
		PlannedOn string `json:"planned_on"`
		StartTime string `json:"start_time,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RecipeID <= 0 {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	plannedOn, err := time.Parse(dateLayout, req.PlannedOn)
	if err != nil {
		http.Error(w, "invalid planned_on (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	startTime := defaultMealStartTime
	if req.StartTime != "" {
		if _, err := time.Parse(timeLayout, req.StartTime); err != nil {
			http.Error(w, "invalid start_time (HH:MM:SS)", http.StatusBadRequest)
			return
		}
		startTime = req.StartTime
	}

	globalPrefs, err := storage.GetPreferences(r.Context(), user.ID)
	if err != nil {
		slog.Error("Error loading preferences", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	item, err := storage.AddMealPlanItem(r.Context(), user.ID, req.RecipeID, plannedOn, startTime, globalPrefs.WeekStartDay)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "recipe not found", http.StatusNotFound)
			return
		}
		slog.Error("Error adding meal plan item", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, item)
}

// HandlePatchMealPlanItem moves an item to a new day and/or time (full-replace — both
// fields required), used by the Day view's drag interaction.
func HandlePatchMealPlanItem(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	itemID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || itemID <= 0 {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}

	var req struct {
		PlannedOn string `json:"planned_on"`
		StartTime string `json:"start_time"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	plannedOn, err := time.Parse(dateLayout, req.PlannedOn)
	if err != nil {
		http.Error(w, "invalid planned_on (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	if _, err := time.Parse(timeLayout, req.StartTime); err != nil {
		http.Error(w, "invalid start_time (HH:MM:SS)", http.StatusBadRequest)
		return
	}

	item, err := storage.UpdateMealPlanItem(r.Context(), user.ID, itemID, plannedOn, req.StartTime)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("Error updating meal plan item", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, item)
}

// HandleDeleteMealPlanItem removes one meal-plan item.
func HandleDeleteMealPlanItem(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	itemID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || itemID <= 0 {
		http.Error(w, "invalid item id", http.StatusBadRequest)
		return
	}

	if err := storage.DeleteMealPlanItem(r.Context(), user.ID, itemID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("Error deleting meal plan item", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// loadMealPlanSignal gathers the candidate recipes + soft personalization signal shared
// by /meal-plan/suggest and /meal-plan/chat, with the user's per-week scheduling
// overrides (if any) for the week containing startsOn merged over their global
// defaults. A missing cooking profile (not yet computed by the background worker) or
// unset preferences are not errors — both zero values are fine soft-signal inputs.
func loadMealPlanSignal(ctx context.Context, userID int, startsOn time.Time) ([]model.Recipe, model.CookingProfile, model.UserPreferences, error) {
	candidates, err := storage.GetRecipes(userID)
	if err != nil {
		return nil, model.CookingProfile{}, model.UserPreferences{}, err
	}
	profile, _, err := storage.GetCookingProfile(ctx, userID)
	if err != nil {
		return nil, model.CookingProfile{}, model.UserPreferences{}, err
	}
	prefs, err := storage.GetPreferences(ctx, userID)
	if err != nil {
		return nil, model.CookingProfile{}, model.UserPreferences{}, err
	}
	weekPrefs, err := storage.GetWeekPreferences(ctx, userID, storage.StartOfWeek(startsOn, prefs.WeekStartDay))
	if err != nil {
		return nil, model.CookingProfile{}, model.UserPreferences{}, err
	}
	if weekPrefs.NoFoodDays != nil {
		prefs.MealPlanNoFoodDays = *weekPrefs.NoFoodDays
	}
	if weekPrefs.NoCookDays != nil {
		prefs.MealPlanNoCookDays = *weekPrefs.NoCookDays
	}
	if weekPrefs.BatchDays != nil {
		prefs.MealPlanBatchDays = *weekPrefs.BatchDays
	}
	return candidates, profile, prefs, nil
}

// loadPriorMeal returns the most recently planned meal-plan item in the 3 days strictly
// before startsOn, so a no-cook day at the very start of a range can carry forward a
// dish cooked just before the week boundary (e.g. "cook Sunday for Monday"). No prior
// item is not an error.
func loadPriorMeal(ctx context.Context, userID int, startsOn time.Time) (*model.MealPlanItem, error) {
	items, err := storage.GetMealPlanRange(ctx, userID, startsOn.AddDate(0, 0, -3), startsOn.AddDate(0, 0, -1))
	if err != nil || len(items) == 0 {
		return nil, err
	}
	last := items[len(items)-1] // GetMealPlanRange orders by planned_on, start_time
	return &last, nil
}

// loadAssignedRecipeDocs fetches the structured content (ingredients) for every distinct
// saved-recipe assignment in assignments, so a chat-driven targeted edit has real
// ingredients to work from instead of just a title. Recipes with no structured data, or
// that fail to load, are silently skipped rather than failing the whole chat call.
func loadAssignedRecipeDocs(ctx context.Context, userID int, assignments []model.MealPlanAssignment) map[int]model.RecipeDocument {
	docs := make(map[int]model.RecipeDocument)
	for _, a := range assignments {
		if a.RecipeID == nil {
			continue
		}
		if _, ok := docs[*a.RecipeID]; ok {
			continue
		}
		recipe, err := storage.GetRecipeByID(ctx, userID, *a.RecipeID)
		if err != nil {
			continue
		}
		if recipe.Structured != nil {
			docs[*a.RecipeID] = *recipe.Structured
		}
	}
	return docs
}

// parseMealPlanRange reads+validates starts_on/ends_on from a decoded request body.
func parseMealPlanRange(startsOnRaw, endsOnRaw string) (startsOn, endsOn time.Time, err error) {
	startsOn, err = time.Parse(dateLayout, startsOnRaw)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid or missing starts_on (YYYY-MM-DD)")
	}
	endsOn, err = time.Parse(dateLayout, endsOnRaw)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("invalid or missing ends_on (YYYY-MM-DD)")
	}
	if endsOn.Before(startsOn) || endsOn.Sub(startsOn).Hours()/24 > maxMealPlanRangeDays {
		return time.Time{}, time.Time{}, errors.New("ends_on must be on or after starts_on, within 42 days")
	}
	return startsOn, endsOn, nil
}

// HandleSuggestMealPlan proposes a plan for a date range from the caller's saved
// recipes (LLM-calling — wired under rate limit + billing in cmd/main.go).
func HandleSuggestMealPlan(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		StartsOn string `json:"starts_on"`
		EndsOn   string `json:"ends_on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	startsOn, endsOn, err := parseMealPlanRange(req.StartsOn, req.EndsOn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	candidates, profile, prefs, err := loadMealPlanSignal(r.Context(), user.ID, startsOn)
	if err != nil {
		slog.Error("Error loading meal plan signal", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	priorMeal, err := loadPriorMeal(r.Context(), user.ID, startsOn)
	if err != nil {
		slog.Error("Error loading prior meal", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	suggestion, err := service.SuggestMealPlan(r.Context(), candidates, profile, prefs, startsOn, endsOn, priorMeal)
	if err != nil {
		slog.Error("Error suggesting meal plan", "error", err)
		http.Error(w, "failed to suggest a meal plan", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, suggestion)
}

// HandleMealPlanChat refines an in-progress plan proposal with a new instruction —
// stateless, the caller resends the full history each call (LLM-calling).
func HandleMealPlanChat(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		StartsOn string                   `json:"starts_on"`
		EndsOn   string                   `json:"ends_on"`
		Initial  model.MealPlanSuggestion `json:"initial"`
		History  []model.MealPlanChatTurn `json:"history"`
		Message  string                   `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	startsOn, endsOn, err := parseMealPlanRange(req.StartsOn, req.EndsOn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	candidates, profile, prefs, err := loadMealPlanSignal(r.Context(), user.ID, startsOn)
	if err != nil {
		slog.Error("Error loading meal plan signal", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	priorMeal, err := loadPriorMeal(r.Context(), user.ID, startsOn)
	if err != nil {
		slog.Error("Error loading prior meal", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	assignedRecipes := loadAssignedRecipeDocs(r.Context(), user.ID, req.Initial.Assignments)

	suggestion, err := service.MealPlanChat(r.Context(), candidates, profile, prefs, startsOn, endsOn, priorMeal, assignedRecipes, req.Initial, req.History, req.Message)
	if err != nil {
		slog.Error("Error in meal plan chat", "error", err)
		http.Error(w, "failed to update the meal plan", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, suggestion)
}

// writeSSE writes one Server-Sent Event, JSON-encoding payload so arbitrary text
// (newlines, quotes) can never break the "data:" line framing, and flushes
// immediately so the client sees it without buffering.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Error encoding SSE payload", "error", err)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

// HandleMealPlanChatReplyStream streams a short conversational acknowledgment of the
// caller's chat message via SSE (LLM-calling). Runs alongside POST /meal-plan/chat,
// which the frontend calls in parallel for the actual structured plan update — this
// endpoint exists purely so the chat feels alive while that heavier call is in flight.
func HandleMealPlanChatReplyStream(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		StartsOn string `json:"starts_on"`
		EndsOn   string `json:"ends_on"`
		Message  string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	startsOn, endsOn, err := parseMealPlanRange(req.StartsOn, req.EndsOn)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	candidates, profile, prefs, err := loadMealPlanSignal(r.Context(), user.ID, startsOn)
	if err != nil {
		slog.Error("Error loading meal plan signal", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
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

	err = service.StreamMealPlanChatReply(r.Context(), candidates, profile, prefs, startsOn, endsOn, req.Message,
		func(chunk string) { writeSSE(w, flusher, "message", chunk) })
	if err != nil {
		slog.Error("Error streaming meal plan chat reply", "error", err)
		writeSSE(w, flusher, "error", err.Error())
		return
	}
	writeSSE(w, flusher, "done", struct{}{})
}

// HandleGenerateVariant proposes a new, unsaved recipe variant of an existing saved
// recipe (LLM-calling). The frontend persists it via POST /add-recipe (with
// variant_of_recipe_id) only if the user accepts it.
func HandleGenerateVariant(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Hint string `json:"hint,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // body is optional

	recipe, err := storage.GetRecipeByID(r.Context(), user.ID, recipeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "recipe not found", http.StatusNotFound)
			return
		}
		slog.Error("Error loading recipe for variant", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if recipe.Structured == nil {
		http.Error(w, "recipe has no structured data to vary", http.StatusUnprocessableEntity)
		return
	}

	variant, err := service.GenerateRecipeVariant(r.Context(), *recipe.Structured, req.Hint)
	if err != nil {
		slog.Error("Error generating recipe variant", "error", err)
		http.Error(w, "failed to generate a variant", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"variant_of_recipe_id": recipeID,
		"recipename":           variant.Title,
		"recipe":               service.RenderMarkdown(variant),
		"structured":           variant,
	})
}

// HandleGetWeekPreferences returns the caller's per-week scheduling overrides for the
// week (per their configured week_start_day) containing starts_on. A field is nil when
// that week has no override — the client falls back to the caller's global preferences.
func HandleGetWeekPreferences(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	requested, err := time.Parse(dateLayout, r.URL.Query().Get("starts_on"))
	if err != nil {
		http.Error(w, "invalid or missing starts_on (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	globalPrefs, err := storage.GetPreferences(r.Context(), user.ID)
	if err != nil {
		slog.Error("Error loading preferences", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	weekPrefs, err := storage.GetWeekPreferences(r.Context(), user.ID, storage.StartOfWeek(requested, globalPrefs.WeekStartDay))
	if err != nil {
		slog.Error("Error loading week preferences", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, weekPrefs)
}

// HandlePatchWeekPreferences sets the caller's per-week scheduling overrides for the
// week containing starts_on. Full-replace, like PATCH /preferences: a null field
// reverts that setting to the caller's global default, a concrete value overrides it.
func HandlePatchWeekPreferences(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	requested, err := time.Parse(dateLayout, r.URL.Query().Get("starts_on"))
	if err != nil {
		http.Error(w, "invalid or missing starts_on (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	var prefs model.MealPlanWeekPreferences
	if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	if prefs.NoFoodDays != nil {
		for _, day := range *prefs.NoFoodDays {
			if !weekdayNames[day] {
				http.Error(w, "invalid no_food_days entry: "+day, http.StatusBadRequest)
				return
			}
		}
	}
	if prefs.NoCookDays != nil {
		for _, day := range *prefs.NoCookDays {
			if !weekdayNames[day] {
				http.Error(w, "invalid no_cook_days entry: "+day, http.StatusBadRequest)
				return
			}
		}
	}
	if prefs.BatchDays != nil {
		switch *prefs.BatchDays {
		case 1, 2, 3:
		default:
			http.Error(w, "batch_days must be 1, 2, or 3", http.StatusBadRequest)
			return
		}
	}

	globalPrefs, err := storage.GetPreferences(r.Context(), user.ID)
	if err != nil {
		slog.Error("Error loading preferences", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	startsOn := storage.StartOfWeek(requested, globalPrefs.WeekStartDay)
	endsOn := startsOn.AddDate(0, 0, 6)
	if err := storage.UpdateWeekPreferences(r.Context(), user.ID, startsOn, endsOn, prefs); err != nil {
		slog.Error("Error updating week preferences", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}
