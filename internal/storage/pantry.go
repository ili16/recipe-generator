package storage

import (
	"context"
	"errors"
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
		SELECT id, name, quantity, unit, category, expires_on
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
		INSERT INTO pantry_items (user_id, household_id, name, quantity, unit, category, expires_on)
		VALUES ($1, `+stampHousehold+`, $2, $3, $4, $5, $6)
		ON CONFLICT (COALESCE(household_id::text, 'u' || user_id), name, COALESCE(unit, '')) DO UPDATE
		SET quantity = EXCLUDED.quantity, category = EXCLUDED.category,
		    expires_on = EXCLUDED.expires_on, updated_at = NOW()
		RETURNING id, name, quantity, unit, category, expires_on`,
		userID, name, qty, unit, category, expires)
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

// scanPantryItem reads the six columns every query above selects, in that order, and
// renders expires_on as a plain date string.
func scanPantryItem(row pgx.Row) (model.PantryItem, error) {
	var item model.PantryItem
	var expires *time.Time
	if err := row.Scan(&item.ID, &item.Name, &item.Quantity, &item.Unit, &item.Category, &expires); err != nil {
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
		SELECT DISTINCT ri.recipe_id, lower(btrim(ri.item))
		FROM recipe_ingredients ri
		JOIN recipes r ON r.id = ri.recipe_id
		WHERE `+inScope("r", "$1")+` AND r.deleted_at IS NULL
		  AND lower(btrim(ri.item)) = ANY($2::text[])`, userID, normalised)
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
