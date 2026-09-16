package storage

import (
	"context"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so a conversion lookup reads the
// same whether it stands alone (the grocery list) or runs inside a caller's transaction
// (DeductPantry, which must see the same table its deduction is about to be based on).
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// ingredient_grams is global, not per-kitchen: how much an onion weighs is not a fact
// about a household. So nothing here takes a userID and nothing goes through inScope —
// the one table in this package where that is correct rather than an oversight.

// GramsTable reads the conversions needed to answer a request, plus every wildcard row,
// in one query. Callers pass the (item, unit) pairs they are about to convert and hand
// the result to model.ToGrams; a pair with no row is simply absent from the map, which
// is how "we don't know" reaches the caller.
//
// The wildcards come along unasked because they are the fallback for every pair and
// there are ~20 of them — a second query to decide whether to fetch them would cost more
// than fetching them.
func GramsTable(ctx context.Context, pairs []model.ItemUnit) (map[model.ItemUnit]float64, error) {
	return gramsTableTx(ctx, Pool, pairs)
}

// gramsTableTx is GramsTable against any querier — see querier above.
func gramsTableTx(ctx context.Context, q querier, pairs []model.ItemUnit) (map[model.ItemUnit]float64, error) {
	names := make([]string, 0, len(pairs))
	units := make([]string, 0, len(pairs))
	for _, p := range pairs {
		names = append(names, p.Item)
		units = append(units, p.Unit)
	}

	rows, err := q.Query(ctx, `
		SELECT name, unit, grams FROM ingredient_grams
		WHERE name = $3
		   OR (name, unit) IN (SELECT unnest($1::text[]), unnest($2::text[]))`,
		names, units, model.GramsWildcard)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[model.ItemUnit]float64{}
	for rows.Next() {
		var k model.ItemUnit
		var grams float64
		if err := rows.Scan(&k.Item, &k.Unit, &grams); err != nil {
			return nil, err
		}
		out[k] = grams
	}
	return out, rows.Err()
}

// UpsertGrams writes conversions the model worked out. DO NOTHING, not DO UPDATE: a row
// already in the table was either hand-seeded or resolved once and is now the answer
// everything else has been consistent with — letting a later model call quietly move it
// would make two grocery lists of the same week disagree. A wrong row is fixed by
// editing that row, which is the point of storing source.
func UpsertGrams(ctx context.Context, rows []model.GramsRow) error {
	if len(rows) == 0 {
		return nil
	}
	names := make([]string, len(rows))
	units := make([]string, len(rows))
	grams := make([]float64, len(rows))
	sources := make([]string, len(rows))
	for i, r := range rows {
		names[i], units[i], grams[i], sources[i] = r.Item, r.Unit, r.Grams, r.Source
	}
	_, err := Pool.Exec(ctx, `
		INSERT INTO ingredient_grams (name, unit, grams, source)
		SELECT * FROM unnest($1::text[], $2::text[], $3::numeric[], $4::text[])
		ON CONFLICT (name, unit) DO NOTHING`,
		names, units, grams, sources)
	return err
}

// MissingGramsPairs returns every (item, unit) that appears in a live recipe or in
// somebody's pantry and cannot be converted — no row of its own and no wildcard for its
// unit. This is the backfill's worklist (cmd/backfill-grams).
//
// "g" and the units NormalizeUnit folds into it are excluded here rather than in the
// caller, because they are not missing: they are the identity.
func MissingGramsPairs(ctx context.Context) ([]model.ItemUnit, error) {
	rows, err := Pool.Query(ctx, `
		WITH used AS (
			SELECT normalize_item(ri.item) AS name, lower(btrim(COALESCE(ri.unit, ''))) AS unit
			FROM recipe_ingredients ri
			JOIN recipes r ON r.id = ri.recipe_id AND r.deleted_at IS NULL
			WHERE normalize_item(ri.item) <> ''
			UNION
			SELECT name, COALESCE(unit, '') FROM pantry_items
		)
		SELECT DISTINCT u.name, u.unit FROM used u
		WHERE u.unit NOT IN ('g', 'kg', 'l', 'ml')
		  AND NOT EXISTS (SELECT 1 FROM ingredient_grams g WHERE g.name = u.name AND g.unit = u.unit)
		  AND NOT EXISTS (SELECT 1 FROM ingredient_grams g WHERE g.name = $1 AND g.unit = u.unit)
		ORDER BY 1, 2`, model.GramsWildcard)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.ItemUnit
	for rows.Next() {
		var p model.ItemUnit
		if err := rows.Scan(&p.Item, &p.Unit); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
