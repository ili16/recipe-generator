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

	// Advisory (BACKLOG 13.2): without it the planner just offers to share again, which
	// is idempotent anyway.
	shareToken, err := storage.PlanShareToken(r.Context(), user.ID, startsOn, endsOn)
	if err != nil {
		slog.Warn("meal plan: could not read share token", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"starts_on":   startsOn.Format(dateLayout),
		"ends_on":     endsOn.Format(dateLayout),
		"items":       items,
		"share_token": shareToken,
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

	lines, err := groceryLinesFor(r.Context(), user.ID, startsOn, endsOn)
	if err != nil {
		slog.Error("Error getting planned ingredients", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"starts_on": startsOn.Format(dateLayout),
		"ends_on":   endsOn.Format(dateLayout),
		"lines":     lines,
	})
}

// groceryLinesFor builds one user's shopping list for [startsOn, endsOn]. Shared by the
// owner's own list and by a shared plan link (BACKLOG 13.2), which passes the plan
// owner's id — one definition of what the week needs, so the visitor's page cannot drift
// from the cook's.
//
// Only the planned ingredients are load-bearing: staples, stock, conversions and aliases
// are advisory. Without staples the list shows salt, without stock it shops for what is
// already in the fridge, without conversions it splits onions across two lines and
// without aliases a zwiebel and an onion stay apart. None of that is worth failing a
// shopping list over, so each degrades to its zero value rather than a 500.
func groceryLinesFor(ctx context.Context, userID int, startsOn, endsOn time.Time) ([]model.GroceryLine, error) {
	ingredients, err := storage.GetPlannedIngredients(ctx, userID, startsOn, endsOn)
	if err != nil {
		return nil, err
	}

	staples, err := storage.StapleNames(ctx, userID)
	if err != nil {
		slog.Warn("grocery list: could not read staples", "error", err)
	}
	stock, err := storage.PantryStock(ctx, userID)
	if err != nil {
		slog.Warn("grocery list: could not read pantry stock", "error", err)
	}
	grams, err := storage.GramsTable(ctx, append(service.GroceryPairs(ingredients), service.StockPairs(stock)...))
	if err != nil {
		slog.Warn("grocery list: could not read conversions", "error", err)
	}
	aliases, err := storage.AliasTable(ctx, service.GroceryAliasNames(ingredients))
	if err != nil {
		slog.Warn("grocery list: could not read aliases", "error", err)
	}

	return service.BuildGroceryList(ingredients, staples, grams, aliases, stock), nil
}

// HandleSharePlan turns on the read-only link for the caller's week and returns its
// token (BACKLOG 13.2). Idempotent — a second tap returns the token already sent.
func HandleSharePlan(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	startsOn, endsOn, ok := parsePlanRange(w, r)
	if !ok {
		return
	}
	token, err := storage.SharePlan(r.Context(), user.ID, startsOn, endsOn)
	if err != nil {
		slog.Error("Error sharing meal plan", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"share_token": token})
}

// HandleUnsharePlan revokes the week's link; it stops resolving immediately.
func HandleUnsharePlan(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	startsOn, _, ok := parsePlanRange(w, r)
	if !ok {
		return
	}
	if err := storage.UnsharePlan(r.Context(), user.ID, startsOn); err != nil {
		slog.Error("Error unsharing meal plan", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleGetSharedPlan serves a shared week and its grocery list to anyone holding the
// token — no auth, no account, nothing to install (BACKLOG 13.2). The token resolves to
// the owner, and every read below is the owner's ordinary scoped read.
func HandleGetSharedPlan(w http.ResponseWriter, r *http.Request) {
	ownerID, startsOn, endsOn, err := storage.GetSharedPlan(r.Context(), r.PathValue("token"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("Error getting shared plan", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	items, err := storage.GetMealPlanRange(r.Context(), ownerID, startsOn, endsOn)
	if err != nil {
		slog.Error("Error getting shared plan range", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The list is the half with the real use — coordinating with someone who does not
	// have the app — but it is derived, so a failure here still leaves a readable week.
	lines, err := groceryLinesFor(r.Context(), ownerID, startsOn, endsOn)
	if err != nil {
		slog.Warn("shared plan: could not build grocery list", "error", err)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"starts_on": startsOn.Format(dateLayout),
		"ends_on":   endsOn.Format(dateLayout),
		"items":     items,
		"lines":     lines,
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
	globalPrefs, err := storage.GetPreferences(r.Context(), user.ID)
	if err != nil {
		slog.Error("Error loading preferences", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	item, err := storage.AddMealPlanItem(r.Context(), user.ID, req.RecipeID, plannedOn, globalPrefs.WeekStartDay, req.MealSlot, req.Servings)
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

// HandleMarkMealCooked records that a planned meal was cooked, and takes what it used
// out of the pantry (storage.MarkMealCooked — one transaction). Optional
// `{"rating": 1..5}` rates the recipe at the same time, the way cooking mode's Done
// screen does.
//
// The response carries what the pantry deduction did and what it refused to guess at,
// because the alternative — quietly editing someone's fridge — is the thing that would
// make this feature untrustworthy. 404 when the item isn't the caller's *or* is already
// marked: re-tapping must not deduct twice.
func HandleMarkMealCooked(w http.ResponseWriter, r *http.Request) {
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
		Rating *int16 `json:"rating"`
	}
	// An empty body is the common case (mark it cooked, no rating), so a decode failure
	// is only an error when something was actually sent and it was wrong.
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	if req.Rating != nil && (*req.Rating < 1 || *req.Rating > 5) {
		http.Error(w, "rating must be 1-5", http.StatusBadRequest)
		return
	}

	applied, skipped, err := storage.MarkMealCooked(r.Context(), user.ID, itemID, req.Rating)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("Error marking meal cooked", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if skipped == nil {
		skipped = []string{}
	}
	if applied == nil {
		applied = []model.PantryChange{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pantry": map[string]any{"applied": applied, "skipped": skipped},
	})
}

// HandleUnmarkMealCooked undoes a mis-tap. The meal goes back on the week and back onto
// the grocery list; the pantry is deliberately not restored (storage.UnmarkMealCooked
// says why), which the client tells the user rather than leaving them to discover.
func HandleUnmarkMealCooked(w http.ResponseWriter, r *http.Request) {
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

	if err := storage.UnmarkMealCooked(r.Context(), user.ID, itemID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("Error unmarking meal cooked", "error", err)
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

// HandleSetBatchCook makes one cooking day cover a set of later days: the cook scales up,
// each covered day gets a leftover row pointing at it, and the whole thing is one atomic
// write (BACKLOG 9.13). `days` is the complete set of covered days, so the same call is
// also the undo — drop a day from the list and its leftover is cleared.
func HandleSetBatchCook(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	itemID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || itemID <= 0 {
		http.Error(w, "Invalid item id", http.StatusBadRequest)
		return
	}

	var req struct {
		// Days is every day this pot covers, YYYY-MM-DD. Empty means "only the day it is
		// cooked on" — which is how the control undoes itself.
		Days []string `json:"days"`
		// Servings is what the cooking day scales to. Omit to leave it alone.
		Servings *int `json:"servings,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	if req.Servings != nil && (*req.Servings < 1 || *req.Servings > 99) {
		http.Error(w, "servings must be between 1 and 99", http.StatusBadRequest)
		return
	}
	days := make([]time.Time, 0, len(req.Days))
	for _, raw := range req.Days {
		d, err := time.Parse(dateLayout, raw)
		if err != nil {
			http.Error(w, "invalid day (YYYY-MM-DD): "+raw, http.StatusBadRequest)
			return
		}
		days = append(days, d)
	}

	items, err := storage.SetBatchCook(r.Context(), user.ID, itemID, days, req.Servings)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Meal plan item not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("Error setting batch cook", "error", err, "itemID", itemID)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

/* ── Ticked-off grocery lines (BACKLOG 15.9) ───────────────────────────────── */

// parseTickRequest reads the week and line the three tick routes all address. week_start
// is the shopping week, not the plan: the list is re-derived from the plan on every load
// (6.1), so the plan's identity is not a stable thing to hang a tick on.
func parseTickRequest(w http.ResponseWriter, r *http.Request) (weekStart time.Time, lineKey string, ok bool) {
	weekStart, err := time.Parse(dateLayout, r.URL.Query().Get("week_start"))
	if err != nil {
		http.Error(w, "invalid or missing week_start (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	lineKey = r.URL.Query().Get("line_key")
	return weekStart, lineKey, true
}

func HandleGetGroceryTicks(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	weekStart, _, ok := parseTickRequest(w, r)
	if !ok {
		return
	}

	ticks, err := storage.ListGroceryTicks(r.Context(), user.ID, weekStart)
	if err != nil {
		slog.Error("Error listing grocery ticks", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticks": ticks})
}

// HandleTickGroceryLine marks a line bought for the whole kitchen and writes it into the
// pantry. The pantry side of the tick lives here and not in the client (where 7.2 put it)
// for the reason 15.9 exists: with two shoppers, a client-side write is one write per
// device, and the first untick deletes a row the other shopper still believes in. One
// server-side write behind one unique index is one pantry row.
func HandleTickGroceryLine(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	weekStart, _, ok := parseTickRequest(w, r)
	if !ok {
		return
	}

	// The line_key and the pantry item it becomes arrive together: the client already
	// knows both, and deriving the item here would mean re-aggregating the whole week to
	// tick one line.
	var in struct {
		LineKey string           `json:"line_key"`
		Item    model.PantryItem `json:"item"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if in.LineKey == "" {
		http.Error(w, "line_key is required", http.StatusBadRequest)
		return
	}

	tick, err := storage.TickGroceryLine(r.Context(), user.ID, weekStart, in.LineKey, in.Item)
	if err != nil {
		slog.Error("Error ticking grocery line", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, tick)
}

func HandleUntickGroceryLine(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	weekStart, lineKey, ok := parseTickRequest(w, r)
	if !ok {
		return
	}
	if lineKey == "" {
		http.Error(w, "line_key is required", http.StatusBadRequest)
		return
	}

	// A line already unticked -- by the other shopper, a second between the two taps --
	// is the state the caller asked for, so it is a 204 and not a 404.
	if err := storage.UntickGroceryLine(r.Context(), user.ID, weekStart, lineKey); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Error("Error unticking grocery line", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
