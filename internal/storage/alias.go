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

// ClassGroups is the audit's worklist (cmd/backfill-aliases -audit): every substitution
// family the model wrote, with its members. Only the model's own rows, so a hand-corrected
// one is never put back up for review.
//
// Groups of one are left out, and that is not an optimisation: a class can only ever
// produce a swap by matching one row against another, so a family with a single member is
// invisible to every reader and there is nothing to get wrong. It also cuts the worklist
// from every food to the few dozen families they fall into -- which is the right unit
// anyway, because a bad class is never wrong in one row. "black pepper -> spice" is
// defensible alone; "spice = {vanilla bean, black pepper}" is the bug.
func ClassGroups(ctx context.Context) (map[string][]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT class, array_agg(name ORDER BY name)
		FROM ingredient_alias
		WHERE class IS NOT NULL AND source = 'llm'
		GROUP BY class HAVING count(*) > 1
		ORDER BY class`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var class string
		var members []string
		if err := rows.Scan(&class, &members); err != nil {
			return nil, err
		}
		out[class] = members
	}
	return out, rows.Err()
}

// CorrectAliasClasses moves foods out of a family they do not belong in. It is the one
// write that overwrites a resolved row, and it is deliberately narrow: only class, only
// rows the model itself wrote, and only when the answer actually differs.
//
// canonical is untouched on purpose. It is an identity every stored gram row, grocery
// line and pantry match has already been consistent with (see UpsertAliases), so moving
// one is a migration and not an audit. class carries none of that weight -- nothing
// merges on it, nothing subtracts on it, it only ever offers.
//
// Returns how many rows actually changed, which is what the audit reports.
func CorrectAliasClasses(ctx context.Context, classes map[string]*string) (int, error) {
	if len(classes) == 0 {
		return 0, nil
	}
	names := make([]string, 0, len(classes))
	next := make([]*string, 0, len(classes))
	for n, c := range classes {
		names = append(names, n)
		next = append(next, c)
	}
	tag, err := Pool.Exec(ctx, `
		UPDATE ingredient_alias a SET class = v.class
		FROM unnest($1::text[], $2::text[]) AS v(name, class)
		WHERE a.name = v.name AND a.source = 'llm'
		  AND a.class IS DISTINCT FROM v.class`, names, next)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
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

// KnownCanonicals is what the resolver is shown so it reuses a food's existing canonical
// instead of minting a second one (BACKLOG 18.0): the most-used first, capped, because the
// prompt has to stay small and the rare ones are the least likely to be the answer.
func KnownCanonicals(ctx context.Context, limit int) ([]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT canonical FROM ingredient_alias
		GROUP BY canonical ORDER BY count(*) DESC, canonical LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClassCanonicals lists, per class, the distinct canonicals in it (only classes with more
// than one) -- the audit's worklist for one food split across canonicals (BACKLOG 18.0).
func ClassCanonicals(ctx context.Context) (map[string][]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT class, array_agg(DISTINCT canonical ORDER BY canonical)
		FROM ingredient_alias WHERE class IS NOT NULL
		GROUP BY class HAVING count(DISTINCT canonical) > 1
		ORDER BY class`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var class string
		var members []string
		if err := rows.Scan(&class, &members); err != nil {
			return nil, err
		}
		out[class] = members
	}
	return out, rows.Err()
}
