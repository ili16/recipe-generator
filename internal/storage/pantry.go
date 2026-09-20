package storage

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// Both are 400s, not 500s: the caller sent a blank name or an unparseable date.
var (
	ErrEmptyPantryItemName = errors.New("pantry item name is empty")
	ErrInvalidExpiry       = errors.New("expires_on is not a YYYY-MM-DD date")
)

const dateLayout = "2006-01-02"

// Every function here scopes by the caller's kitchen -- their household when they are in
// one, themselves otherwise (inScope, scope.go) -- so an item outside it is
// indistinguishable from a missing one (BACKLOG 7.1): pgx.ErrNoRows, which the handlers
// turn into 404. A household has one fridge, so a member sees and edits every line in it;
// the user_id on the row only records who put it there, and is what it reverts to when
// the household ends.

// ListPantryItems returns the caller's kitchen's pantry, expiring items first so 7.4's "use first" is
// the natural reading order; items with no expiry sort last.
func ListPantryItems(ctx context.Context, userID int) ([]model.PantryItem, error) {
	rows, err := Pool.Query(ctx, `
		SELECT id, name, quantity, unit, category, expires_on, staple
		FROM pantry_items WHERE `+inScope("pantry_items", "$1")+`
		ORDER BY expires_on ASC NULLS LAST, name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []model.PantryItem{}
	for rows.Next() {
		item, err := scanPantryItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// UpsertPantryItem adds an item, or replaces the quantity and expiry of the one already
// held under the same normalised name and unit — re-adding milk you already have is one
// line with a new amount, not two lines (BACKLOG 7.1). Name and unit are normalised the
// way the grocery list normalises them, which is what makes 7.3's match possible.
//
// expiresOn is "YYYY-MM-DD" or empty for none.
func UpsertPantryItem(ctx context.Context, userID int, in model.PantryItem) (model.PantryItem, error) {
	name := model.NormalizeItem(in.Name)
	if name == "" {
		return model.PantryItem{}, ErrEmptyPantryItemName
	}

	family, factor := model.NormalizeUnit(in.Unit)
	var unit *string
	if family != "" {
		unit = &family
	}
	qty := in.Quantity
	if qty != nil {
		scaled := *qty * factor
		qty = &scaled
	}

	category := "spices_dry"
	if slices.Contains(model.PantryCategories, in.Category) {
		category = in.Category
	}

	expires, err := parseDatePtr(in.ExpiresOn)
	if err != nil {
		return model.PantryItem{}, err
	}

	row := Pool.QueryRow(ctx, `
		INSERT INTO pantry_items (user_id, household_id, name, quantity, unit, category, expires_on, staple)
		VALUES ($1, `+stampHousehold+`, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (COALESCE(household_id::text, 'u' || user_id), name, COALESCE(unit, '')) DO UPDATE
		SET quantity = EXCLUDED.quantity, category = EXCLUDED.category,
		    expires_on = EXCLUDED.expires_on, staple = EXCLUDED.staple, updated_at = NOW()
		RETURNING id, name, quantity, unit, category, expires_on, staple`,
		userID, name, qty, unit, category, expires, in.Staple)
	return scanPantryItem(row)
}

// DeletePantryItem removes one item from the caller's kitchen. Anything outside it is
// not-found.
func DeletePantryItem(ctx context.Context, userID, itemID int) error {
	tag, err := Pool.Exec(ctx, `DELETE FROM pantry_items WHERE id = $2 AND `+inScope("pantry_items", "$1"), userID, itemID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// scanPantryItem reads the seven columns every query above selects, in that order, and
// renders expires_on as a plain date string.
func scanPantryItem(row pgx.Row) (model.PantryItem, error) {
	var item model.PantryItem
	var expires *time.Time
	if err := row.Scan(&item.ID, &item.Name, &item.Quantity, &item.Unit, &item.Category, &expires, &item.Staple); err != nil {
		return model.PantryItem{}, err
	}
	if expires != nil {
		s := expires.Format(dateLayout)
		item.ExpiresOn = &s
	}
	return item, nil
}

// parseDatePtr turns an optional "YYYY-MM-DD" into a nullable date. A malformed date is
// the caller's error, not a silently dropped expiry.
func parseDatePtr(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	d, err := time.Parse(dateLayout, *s)
	if err != nil {
		return nil, ErrInvalidExpiry
	}
	return &d, nil
}

// RecipesUsingIngredients maps recipe id → which of names that recipe uses, across the
// live recipes the caller can see. names are matched as normalised ingredient names (model.NormalizeItem), the
// same key the pantry and the grocery list use — so "use up the spinach" finds the recipe
// that lists "Spinach", whatever amount it asks for (BACKLOG 7.4).
//
// A recipe absent from the map uses none of them.
func RecipesUsingIngredients(ctx context.Context, userID int, names []string) (map[int][]string, error) {
	normalised := make([]string, 0, len(names))
	for _, n := range names {
		if n := model.NormalizeItem(n); n != "" {
			normalised = append(normalised, n)
		}
	}
	if len(normalised) == 0 {
		return map[int][]string{}, nil
	}

	rows, err := Pool.Query(ctx, `
		SELECT DISTINCT ri.recipe_id, normalize_item(ri.item)
		FROM recipe_ingredients ri
		JOIN recipes r ON r.id = ri.recipe_id
		WHERE `+inScope("r", "$1")+` AND r.deleted_at IS NULL
		  AND canonical_item(ri.item) IN (SELECT canonical_item(n) FROM unnest($2::text[]) n)`,
		userID, normalised)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int][]string{}
	for rows.Next() {
		var recipeID int
		var item string
		if err := rows.Scan(&recipeID, &item); err != nil {
			return nil, err
		}
		out[recipeID] = append(out[recipeID], item)
	}
	return out, rows.Err()
}

// StapleNames is the set of normalised names the caller's kitchen always has. The
// grocery list drops them (you do not shop for salt) and a cooked meal does not deduct
// them (you do not measure what is left of it). They stay ordinary pantry rows
// everywhere else, which is why the pantry match still counts them — that is the whole
// reason for marking one.
func StapleNames(ctx context.Context, userID int) (map[string]bool, error) {
	rows, err := Pool.Query(ctx,
		`SELECT name FROM pantry_items WHERE `+inScope("pantry_items", "$1")+` AND staple`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// DeductPantry takes what a cooked recipe used out of the caller's pantry, inside the
// caller's transaction so that marking a meal cooked and emptying the fridge for it
// either both happen or neither does.
//
// scale is the same portions ratio the grocery list scales by (planned servings over the
// recipe's own yield), so cooking a 2-portion recipe for 4 takes twice as much out.
//
// What it will not do is guess. An ingredient is skipped, not estimated, when:
//   - either side's unit has no conversion to grams (model.ToGrams says no), or
//   - the pantry row has no quantity at all — "I have flour" does not say how much.
//
// Staples are never touched: they are the things you do not measure.
//
// Returns what it changed and the names it left alone, because the one thing worse than
// not deducting is deducting silently — the caller shows both to the user.
func DeductPantry(ctx context.Context, tx pgx.Tx, userID, recipeID int, scale float64) ([]model.PantryChange, []string, error) {
	// What the recipe needs, summed per item: an ingredient listed in two sections is one
	// demand on one pantry line.
	rows, err := tx.Query(ctx, `
		SELECT canonical_item(item), item, quantity, unit FROM recipe_ingredients
		WHERE recipe_id = $1 AND normalize_item(item) <> '' AND quantity IS NOT NULL`, recipeID)
	if err != nil {
		return nil, nil, err
	}
	type demand struct {
		grams float64
		known bool
	}
	needed := map[string]*demand{}
	var pairs []model.ItemUnit
	type rawNeed struct {
		// item is the bare food and the key everything joins on; written is the
		// ingredient's own wording, which is the only thing that still carries the size
		// modifier ("2 large onions" weigh more than "2 onions", BACKLOG 17.1).
		item    string
		written string
		qty     float64
		unit    *string
	}
	var raws []rawNeed
	for rows.Next() {
		var r rawNeed
		if err := rows.Scan(&r.item, &r.written, &r.qty, &r.unit); err != nil {
			rows.Close()
			return nil, nil, err
		}
		family, _ := model.NormalizeUnit(r.unit)
		pairs = append(pairs, model.ItemUnit{Item: r.item, Unit: family})
		raws = append(raws, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(raws) == 0 {
		return nil, nil, nil
	}

	// The pantry lines that could be affected: same name, not a staple, in the caller's
	// kitchen. FOR UPDATE because two members of a household can cook at once.
	names := make([]string, 0, len(raws))
	for _, r := range raws {
		names = append(names, r.item)
	}
	pantryRows, err := tx.Query(ctx, `
		SELECT id, canonical_item(name), name, quantity, unit FROM pantry_items
		WHERE `+inScope("pantry_items", "$1")+` AND NOT staple
		  AND canonical_item(name) = ANY($2::text[])
		FOR UPDATE`, userID, names)
	if err != nil {
		return nil, nil, err
	}
	type held struct {
		id int
		// key is the canonical food, which is what the recipe's demand is keyed on.
		// name is what its owner actually typed, and is what gets reported back to
		// them -- being told parmesan was used when the jar says parmigiano reggiano
		// is a small lie the join does not need us to tell.
		key  string
		name string
		qty  *float64
		unit *string
	}
	var have []held
	for pantryRows.Next() {
		var h held
		if err := pantryRows.Scan(&h.id, &h.key, &h.name, &h.qty, &h.unit); err != nil {
			pantryRows.Close()
			return nil, nil, err
		}
		pairs = append(pairs, model.ItemUnit{Item: h.name, Unit: derefUnit(h.unit)})
		have = append(have, h)
	}
	pantryRows.Close()
	if err := pantryRows.Err(); err != nil {
		return nil, nil, err
	}
	if len(have) == 0 {
		return nil, nil, nil
	}

	table, err := gramsTableTx(ctx, tx, pairs)
	if err != nil {
		return nil, nil, err
	}

	// Convert the demand. An unconvertible line makes the whole item unknown rather than
	// deducting only the part we could read — half a deduction is a wrong one.
	for _, r := range raws {
		d, ok := needed[r.item]
		if !ok {
			d = &demand{known: true}
			needed[r.item] = d
		}
		g, ok := model.ToGrams(table, r.written, r.unit, r.qty*scale)
		if !ok {
			d.known = false
			continue
		}
		d.grams += g
	}

	applied := []model.PantryChange{}
	skipped := []string{}
	for _, h := range have {
		d, ok := needed[h.key]
		if !ok {
			continue
		}
		hadGrams := 0.0
		convertible := ok && d.known && h.qty != nil
		if convertible {
			hadGrams, convertible = model.ToGrams(table, h.name, h.unit, *h.qty)
		}
		if !convertible {
			skipped = append(skipped, h.name)
			continue
		}

		left := hadGrams - d.grams
		if left <= 0 {
			if _, err := tx.Exec(ctx, `DELETE FROM pantry_items WHERE id = $1`, h.id); err != nil {
				return nil, nil, err
			}
			applied = append(applied, model.PantryChange{Name: h.name, Used: *h.qty, Unit: h.unit, Removed: true})
			continue
		}

		// Back into the row's own unit. Weights are already grams by the time they are
		// stored (UpsertPantryItem normalises kg away), so this matters for the units
		// that survive normalisation: "6 eggs" minus 2 must come back as 4 eggs, not as
		// 200 of anything.
		remaining, ok := model.FromGrams(table, h.name, h.unit, left)
		if !ok {
			skipped = append(skipped, h.name)
			continue
		}
		remaining = math.Round(remaining*1000) / 1000
		if _, err := tx.Exec(ctx,
			`UPDATE pantry_items SET quantity = $2, updated_at = NOW() WHERE id = $1`, h.id, remaining); err != nil {
			return nil, nil, err
		}
		applied = append(applied, model.PantryChange{
			Name: h.name, Used: math.Round((*h.qty-remaining)*1000) / 1000, Unit: h.unit, Remaining: &remaining,
		})
	}
	return applied, skipped, nil
}

// derefUnit renders a nullable pantry unit as the ItemUnit key's "" for a countable item.
func derefUnit(u *string) string {
	if u == nil {
		return ""
	}
	return *u
}
