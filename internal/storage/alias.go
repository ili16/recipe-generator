package storage

import (
	"context"
	"errors"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// ingredient_alias is global for the same reason ingredient_grams is: what parmesan is
// called, and what you could use instead of spaghetti, are not facts about a household.
// Nothing here takes a userID and nothing goes through inScope.

// AliasTable reads the alias rows for the names a request is about to key on, in one
// query. A name with no row is simply absent, and model.CanonicalItem then resolves it to
// itself — so a cold table changes no behaviour anywhere.
func AliasTable(ctx context.Context, names []string) (map[string]model.Alias, error) {
	if len(names) == 0 {
		return map[string]model.Alias{}, nil
	}
	rows, err := Pool.Query(ctx, `
		SELECT name, canonical, class FROM ingredient_alias
		WHERE name = ANY($1::text[])`, names)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]model.Alias{}
	for rows.Next() {
		var a model.Alias
		if err := rows.Scan(&a.Name, &a.Canonical, &a.Class); err != nil {
			return nil, err
		}
		out[a.Name] = a
	}
	return out, rows.Err()
}

// UpsertAliases writes what the model worked out. DO NOTHING, not DO UPDATE — the same
// contract as UpsertGrams and for the same reason: a name already resolved is the answer
// every stored gram row, grocery line and pantry match has been consistent with, and
// letting a later call quietly move it would make two lists of the same week disagree.
// A wrong row is fixed by editing that row, which is what source is for.
func UpsertAliases(ctx context.Context, rows []model.Alias) error {
	if len(rows) == 0 {
		return nil
	}
	names := make([]string, len(rows))
	canon := make([]string, len(rows))
	class := make([]*string, len(rows))
	for i, r := range rows {
		names[i], canon[i], class[i] = r.Name, r.Canonical, r.Class
	}
	// source is left to its 'llm' default, so a hand-seeded row stays distinguishable.
	_, err := Pool.Exec(ctx, `
		INSERT INTO ingredient_alias (name, canonical, class)
		SELECT * FROM unnest($1::text[], $2::text[], $3::text[])
		ON CONFLICT (name) DO NOTHING`,
		names, canon, class)
	return err
}

// MissingAliasNames is the backfill's worklist (cmd/backfill-aliases): every food that
// appears in a live recipe or in somebody's pantry and has no alias row yet.
func MissingAliasNames(ctx context.Context) ([]string, error) {
	rows, err := Pool.Query(ctx, `
		WITH used AS (
			SELECT normalize_item(ri.item) AS name
			FROM recipe_ingredients ri
			JOIN recipes r ON r.id = ri.recipe_id AND r.deleted_at IS NULL
			WHERE normalize_item(ri.item) <> ''
			UNION
			SELECT name FROM pantry_items
		)
		SELECT DISTINCT u.name FROM used u
		WHERE u.name <> ''
		  AND NOT EXISTS (SELECT 1 FROM ingredient_alias a WHERE a.name = u.name)
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// NearestCanonical is the pg_trgm fallback, and it sits strictly BELOW the exact join:
// callers reach it only for a name no alias row answers. It is for inflection and typos
// ("zwiebeln" for "zwiebel") that are not worth a hand-written list and not worth a model
// call either.
//
// The threshold is deliberately high. A near miss here becomes a merged shopping line or
// a deduction from the wrong jar, so the failure to avoid is a false match, not a missed
// one — "" means no answer and every caller must treat it as such.
func NearestCanonical(ctx context.Context, name string) (string, error) {
	var out string
	err := Pool.QueryRow(ctx, `
		SELECT canonical FROM ingredient_alias
		WHERE similarity(canonical, $1) >= 0.75
		ORDER BY similarity(canonical, $1) DESC, canonical
		LIMIT 1`, name).Scan(&out)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return out, err
}
