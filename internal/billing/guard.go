package billing

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/storage"
)

var defaultCapUSD = envFloat("LLM_BUDGET_DEFAULT_USD", 2.00)

// DefaultCapUSD is the fallback monthly budget for users without an override.
func DefaultCapUSD() float64 { return defaultCapUSD }

// CurrentUsage returns a user's current-month spend and effective cap (their override if
// set, else DefaultCapUSD).
func CurrentUsage(ctx context.Context, userID int) (spent, cap float64, err error) {
	spent, err = storage.CurrentSpend(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	cap = defaultCapUSD
	if override, ok, err := storage.BudgetOverride(ctx, userID); err == nil && ok {
		cap = override
	}
	return spent, cap, nil
}

// Guard blocks a request once the authenticated caller has hit their monthly LLM budget.
// No-ops for anonymous requests — nothing is tracked for them, so nothing to cap; the
// rate limiter is their only protection.
func Guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.FromContext(r.Context())
		if !ok {
			next(w, r)
			return
		}

		spent, cap, err := CurrentUsage(r.Context(), user.ID)
		if err != nil {
			// Fail open: a DB hiccup shouldn't block recipe generation.
			next(w, r)
			return
		}
		if spent >= cap {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "budget_exceeded"})
			return
		}
		next(w, r)
	}
}
