package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
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
// parsePlanRange reads the [starts_on, ends_on] query pair shared by the meal plan and
// the grocery list. ends_on defaults to starts_on+6d (a week). It writes the 400 itself
// and reports ok=false, so a handler only has to return.
func parsePlanRange(w http.ResponseWriter, r *http.Request) (startsOn, endsOn time.Time, ok bool) {
	startsOn, err := time.Parse(dateLayout, r.URL.Query().Get("starts_on"))
	if err != nil {
		http.Error(w, "invalid or missing starts_on (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	endsOn = startsOn.AddDate(0, 0, 6)
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
	return startsOn, endsOn, true
}

func HandleGetMealPlan(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	startsOn, endsOn, ok := parsePlanRange(w, r)
	if !ok {
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

// HandleGetGroceryList returns the week's shopping list: every ingredient of every
// recipe planned in [starts_on, ends_on], consolidated into one line per item
// (BACKLOG 6.1). Read-only and derived — nothing about it is stored, so it always
// reflects the plan as it stands right now.
func HandleGetGroceryList(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	startsOn, endsOn, ok := parsePlanRange(w, r)
	if !ok {
		return
	}

	ingredients, err := storage.GetPlannedIngredients(r.Context(), user.ID, startsOn, endsOn)
	if err != nil {
		slog.Error("Error getting planned ingredients", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"starts_on": startsOn.Format(dateLayout),
		"ends_on":   endsOn.Format(dateLayout),
		"lines":     service.BuildGroceryList(ingredients),
	})
}

// HandleAddMealPlanItem assigns a saved recipe to one meal slot of a day (upsert by
// (day, slot) — replaces whatever was already in that slot, leaving the day's other
// meals alone), creating that week's meal_plans row on first use.
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
		// Servings is how many portions are wanted that day; omit to leave it as the
		// recipe is written. Since this endpoint upserts by day, re-POSTing the same
		// recipe with a new servings is how a day's servings get changed (BACKLOG 6.2).
		Servings *int `json:"servings,omitempty"`
		// MealSlot is which meal of the day this is; omit for dinner (BACKLOG 6.4).
		MealSlot string `json:"meal_slot,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RecipeID <= 0 {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	if req.Servings != nil && (*req.Servings < 1 || *req.Servings > 99) {
		http.Error(w, "servings must be between 1 and 99", http.StatusBadRequest)
		return
	}
	if req.MealSlot == "" {
		req.MealSlot = storage.DefaultMealSlot
	}
	if !storage.ValidMealSlot(req.MealSlot) {
		http.Error(w, "meal_slot must be one of breakfast, lunch, dinner, snack", http.StatusBadRequest)
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
	item, err := storage.AddMealPlanItem(r.Context(), user.ID, req.RecipeID, plannedOn, startTime, globalPrefs.WeekStartDay, req.MealSlot, req.Servings)
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
