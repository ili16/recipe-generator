package storage

import (
	"context"
	"time"
)

// CreateGenerationEvent starts tracking one /generate call's funnel outcome. Returns the
// new event's id (a UUID, as a string) so the caller can hand it back to the client.
func CreateGenerationEvent(ctx context.Context, userID int, sourceType, prompt string) (string, error) {
	var id string
	err := Pool.QueryRow(ctx, `
		INSERT INTO generation_events (user_id, source_type, prompt)
		VALUES ($1, $2, $3)
		RETURNING id`,
		userID, sourceType, prompt,
	).Scan(&id)
	return id, err
}

// MarkRefined flags a generation as having had at least one applied refinement. It does
// not resolve the outcome — the row might still end up "saved" or, via the sweep, decided
// between "refined" and "abandoned". No-op if generationID doesn't match a pending row
// owned by userID.
func MarkRefined(ctx context.Context, userID int, generationID string) error {
	if generationID == "" {
		return nil
	}
	_, err := Pool.Exec(ctx, `
		UPDATE generation_events SET had_refine = TRUE
		WHERE id = $1 AND user_id = $2 AND outcome = 'pending'`,
		generationID, userID)
	return err
}

// Resolve sets a generation's terminal outcome. No-op (no error) if generationID is
// empty or doesn't match a pending row owned by userID — callers never need to branch
// on "did this exist".
func Resolve(ctx context.Context, userID int, generationID, outcome string, recipeID *int) error {
	if generationID == "" {
		return nil
	}
	_, err := Pool.Exec(ctx, `
		UPDATE generation_events
		SET outcome = $3, recipe_id = $4, resolved_at = NOW()
		WHERE id = $1 AND user_id = $2 AND outcome = 'pending'`,
		generationID, userID, outcome, recipeID)
	return err
}

// SweepStale resolves every generation_events row still "pending" older than staleAfter
// into "refined" (if it ever had an applied refinement) or "abandoned" (if not), and
// returns how many rows landed in each outcome so the caller can emit telemetry from one
// query instead of a second lookup.
func SweepStale(ctx context.Context, staleAfter time.Duration) (map[string]int64, error) {
	rows, err := Pool.Query(ctx, `
		WITH resolved AS (
			UPDATE generation_events
			SET outcome = CASE WHEN had_refine THEN 'refined' ELSE 'abandoned' END,
			    resolved_at = NOW()
			WHERE outcome = 'pending' AND created_at < NOW() - ($1 * INTERVAL '1 second')
			RETURNING outcome
		)
		SELECT outcome, count(*) FROM resolved GROUP BY outcome`,
		staleAfter.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int64)
	for rows.Next() {
		var outcome string
		var n int64
		if err := rows.Scan(&outcome, &n); err != nil {
			return nil, err
		}
		counts[outcome] = n
	}
	return counts, rows.Err()
}
