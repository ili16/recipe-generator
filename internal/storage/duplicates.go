package storage

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// DuplicateMatch is an existing recipe of the caller's that a draft about to be saved
// looks like a repeat of. MatchedOn is "url" or "title" — the client says which so the
// prompt can be specific ("you already saved this page" vs "you already have a
// recipe by this name").
type DuplicateMatch struct {
	RecipeID  int    `json:"recipe_id"`
	Title     string `json:"title"`
	MatchedOn string `json:"matched_on"`
}

// duplicateLookupBudget caps how long a save is willing to wait on the duplicate
// check. The check is advisory — BACKLOG.md 8.3 says a save is never refused and
// never blocks — so a timeout means "no duplicate", not an error.
const duplicateLookupBudget = 500 * time.Millisecond

// FindDuplicate looks for a live recipe of this user's that matches the incoming one by
// source URL (exact, when the draft came from a URL) or by normalised title. A URL match
// wins over a title match: it is the stronger signal and the one 8.3's acceptance names.
// Trashed recipes (8.2) do not match — the user deleted that one on purpose.
//
// Returns (nil, nil) for no match, and also for any error or timeout: a save must not
// fail because the advisory check did.
func FindDuplicate(ctx context.Context, userID int, title, sourceURL string) (*DuplicateMatch, error) {
	ctx, cancel := context.WithTimeout(ctx, duplicateLookupBudget)
	defer cancel()

	var m DuplicateMatch
	err := Pool.QueryRow(ctx, `
		SELECT r.id, r.title,
		       CASE WHEN $3 <> '' AND s.source_url = $3 THEN 'url' ELSE 'title' END AS matched_on
		FROM recipes r
		LEFT JOIN sources s ON s.recipe_id = r.id
		WHERE `+inScope("r", "$1")+`
		  AND r.deleted_at IS NULL
		  AND (($3 <> '' AND s.source_url = $3)
		       OR lower(btrim(r.title)) = lower(btrim($2)))
		ORDER BY matched_on DESC, r.id DESC
		LIMIT 1`,
		userID, title, sourceURL,
	).Scan(&m.RecipeID, &m.Title, &m.MatchedOn)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("duplicate check skipped", "userID", userID, "error", err)
		}
		return nil, nil
	}
	return &m, nil
}
