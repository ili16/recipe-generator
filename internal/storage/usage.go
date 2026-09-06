package storage

import (
	"context"
	"time"
)

// RecordUsage persists one billable LLM call for an authenticated user. Anonymous
// requests have no user_id to attach usage to, so nothing is stored for them at all —
// data minimization by construction, not by a filter someone has to remember to apply.
func RecordUsage(ctx context.Context, userID int, callType, model string, promptTokens, completionTokens int64, costUSD float64) error {
	_, err := Pool.Exec(ctx, `
		INSERT INTO llm_usage_events (user_id, call_type, model, prompt_tokens, completion_tokens, cost_usd)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, callType, model, promptTokens, completionTokens, costUSD)
	return err
}

// CurrentSpend returns a user's total LLM cost in USD for the current calendar month.
func CurrentSpend(ctx context.Context, userID int) (float64, error) {
	var spent float64
	err := Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_usd), 0) FROM llm_usage_events
		WHERE user_id = $1 AND created_at >= date_trunc('month', NOW())`,
		userID).Scan(&spent)
	return spent, err
}

// BudgetOverride returns a user's monthly_budget_usd_override, or ok=false if unset.
func BudgetOverride(ctx context.Context, userID int) (usd float64, ok bool, err error) {
	var override *float64
	if err := Pool.QueryRow(ctx, `SELECT monthly_budget_usd_override FROM users WHERE id = $1`, userID).Scan(&override); err != nil {
		return 0, false, err
	}
	if override == nil {
		return 0, false, nil
	}
	return *override, true, nil
}

// SetBudgetOverride sets (or, if usd is nil, clears) a user's monthly budget override.
func SetBudgetOverride(ctx context.Context, userID int, usd *float64) (bool, error) {
	tag, err := Pool.Exec(ctx, `UPDATE users SET monthly_budget_usd_override = $2 WHERE id = $1`, userID, usd)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// UserUsage is one row of the admin usage listing.
type UserUsage struct {
	ID          int       `json:"id"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	SpentUSD    float64   `json:"spent_usd"`
	CapUSD      float64   `json:"cap_usd"`
	OverrideUSD *float64  `json:"override_usd,omitempty"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

// ListUsersWithUsage returns every user with their current-month spend and effective cap
// (their override if set, else defaultCapUSD).
func ListUsersWithUsage(ctx context.Context, defaultCapUSD float64) ([]UserUsage, error) {
	rows, err := Pool.Query(ctx, `
		SELECT u.id, COALESCE(u.email, ''), COALESCE(u.name, ''), u.monthly_budget_usd_override, u.last_seen_at,
		       COALESCE(SUM(e.cost_usd) FILTER (WHERE e.created_at >= date_trunc('month', NOW())), 0) AS spent
		FROM users u
		LEFT JOIN llm_usage_events e ON e.user_id = u.id
		GROUP BY u.id
		ORDER BY spent DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserUsage
	for rows.Next() {
		var u UserUsage
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.OverrideUSD, &u.LastSeenAt, &u.SpentUSD); err != nil {
			return nil, err
		}
		u.CapUSD = defaultCapUSD
		if u.OverrideUSD != nil {
			u.CapUSD = *u.OverrideUSD
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
