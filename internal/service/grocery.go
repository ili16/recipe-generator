package service

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
)

// categoryRank orders the aisles for rendering; anything unknown sorts last as "other".
var categoryRank = func() map[string]int {
	m := make(map[string]int, len(model.GroceryCategories))
	for i, c := range model.GroceryCategories {
		m[c] = i
	}
	return m
}()

// servingScale is how much of a recipe a planned day needs: 4 portions of a recipe that
// yields 2 is a factor of 2 on every measured quantity (BACKLOG 6.2). Unknown or
// nonsensical servings on either side mean "as written" — a factor of 1 — because
// guessing a household size is worse than shopping for the recipe as it stands.
func servingScale(ing model.GroceryIngredient) float64 {
	return model.PortionScale(ing.PlannedServings, ing.RecipeServings)
}

// mealLabel names the meal a line serves: "Mon Roast chicken".
func mealLabel(plannedOn, title string) string {
	if d, err := time.Parse("2006-01-02", plannedOn); err == nil {
		return d.Format("Mon") + " " + title
	}
	return title
}

// GroceryPairs renders the (item, unit) conversions a week's ingredients need, so the
// caller can fetch one grams table for the whole list.
func GroceryPairs(ingredients []model.GroceryIngredient) []model.ItemUnit {
	pairs := make([]model.ItemUnit, 0, len(ingredients))
	for _, ing := range ingredients {
		family, _ := model.NormalizeUnit(ing.Unit)
		pairs = append(pairs, model.ItemUnit{Item: model.NormalizeItem(ing.Item), Unit: family})
		// The per-piece row decides whether the line renders as "3 onions" rather than
		// "450 g onion", so it is wanted even for a recipe written by weight.
		pairs = append(pairs, model.ItemUnit{Item: model.NormalizeItem(ing.Item), Unit: ""})
	}
	return pairs
}

// StockPairs renders the (item, unit) conversions a kitchen's holdings need, so the
// caller fetches one grams table covering both sides of 16.5's subtraction.
func StockPairs(stock []model.PantryHolding) []model.ItemUnit {
	pairs := make([]model.ItemUnit, 0, len(stock))
	for _, h := range stock {
		family, _ := model.NormalizeUnit(h.Unit)
		pairs = append(pairs, model.ItemUnit{Item: model.NormalizeItem(h.Name), Unit: family})
	}
	return pairs
}

// measured is one item's worth of amounts that can be added up, collected before any
// decision about what unit to render them in.
type measured struct {
	// entries are the raw (quantity, unit) amounts, already scaled for portions, in plan
	// order — the first one decides the display unit when pieces are not on offer.
	entries []struct {
		// item is the ingredient's own wording, not the group key. ToGrams reads the
		// size modifier off it ("2 large onions" outweigh "2 onions"), and it cannot do
		// that from the key, which is the bare food by construction.
		item string
		qty  float64
		unit *string
	}
	// byFamily is the fallback grouping, identical to what this function did before
	// conversions existed: one running total per unit family.
	byFamily  map[string]float64
	families  []string // families in first-seen order, so the fallback renders stably
	first     model.GroceryIngredient
	forLabels []string
	note      string
}

// BuildGroceryList consolidates a week's planned ingredients into shopping lines
// (BACKLOG 6.1): one line per item, quantities summed and scaled to the servings wanted
// on each day (6.2), each line tagged with the meals that need it.
//
// staples are the names this kitchen always has (salt, pepper, oil). They are dropped
// outright — a staple is not a shopping decision, and leaving it on the list is what
// made marking one pointless.
//
// grams is the conversion table (model.ToGrams). With it, "2 onions" on Monday and
// "150 g onion" on Thursday are one line instead of two; without it — an empty map, a
// thin table, an ingredient nothing can weigh — every item falls back to exactly the old
// behaviour, one line per unit family. That fallback is per item, so an unweighable
// ingredient never degrades the rest of the list.
//
// Amounts that cannot be summed ("to taste", "for garnish") are folded into that item's
// line as a note rather than dropped, and only become a line of their own when the item
// has no measured amount anywhere in the week.
//
// stock is what the kitchen already holds (BACKLOG 16.5). A line it covers part of is
// reduced to the shortfall and carries its full Need, so the list can say "you have 300 g
// of the 500 g" rather than quietly shopping for less.
func BuildGroceryList(ingredients []model.GroceryIngredient, staples map[string]bool, grams map[model.ItemUnit]float64, aliases map[string]model.Alias, stock []model.PantryHolding) []model.GroceryLine {
	// What the kitchen already holds, in grams, per canonical food (BACKLOG 16.5). Only
	// rows that can be weighed count: an unweighable holding leaves its line alone rather
	// than reducing it by a guess. Nothing is subtracted on the unconsolidated path —
	// if the demand itself could not be summed, a subtraction from it is not a number.
	held := map[string]float64{}
	for _, h := range stock {
		if g, ok := model.ToGrams(grams, h.Name, h.Unit, h.Quantity); ok {
			held[h.Key] += g
		}
	}

	items := map[string]*measured{}
	var order []string // items in first-seen order, which the final sort then overrides

	get := func(ing model.GroceryIngredient, item string) *measured {
		m, ok := items[item]
		if !ok {
			m = &measured{byFamily: map[string]float64{}, first: ing}
			items[item] = m
			order = append(order, item)
		}
		return m
	}

	addFor := func(m *measured, ing model.GroceryIngredient) {
		label := mealLabel(ing.PlannedOn, ing.RecipeTitle)
		for _, existing := range m.forLabels {
			if existing == label {
				return
			}
		}
		m.forLabels = append(m.forLabels, label)
	}

	// Measured amounts first, so a "to taste" mention in an earlier meal still attaches
	// to the item that carries the real quantity rather than splitting off its own line.
	for _, ing := range ingredients {
		item := model.CanonicalItem(aliases, ing.Item)
		if item == "" || staples[item] || ing.Quantity == nil {
			continue
		}
		m := get(ing, item)
		qty := *ing.Quantity * servingScale(ing)
		m.entries = append(m.entries, struct {
			item string
			qty  float64
			unit *string
		}{ing.Item, qty, ing.Unit})

		family, factor := model.NormalizeUnit(ing.Unit)
		if _, seen := m.byFamily[family]; !seen {
			m.families = append(m.families, family)
		}
		m.byFamily[family] += qty * factor
		addFor(m, ing)
	}

	for _, ing := range ingredients {
		item := model.CanonicalItem(aliases, ing.Item)
		if item == "" || staples[item] || ing.Quantity != nil {
			continue
		}
		m := get(ing, item)
		if ing.QuantityText != nil {
			if note := strings.TrimSpace(*ing.QuantityText); note != "" && !strings.Contains(m.note, note) {
				if m.note != "" {
					m.note += ", "
				}
				m.note += note
			}
		}
		addFor(m, ing)
	}

	out := make([]model.GroceryLine, 0, len(order))
	for _, item := range order {
		out = append(out, linesFor(item, items[item], grams, held[item])...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return categoryRank[out[i].Category] < categoryRank[out[j].Category]
		}
		return strings.ToLower(out[i].Item) < strings.ToLower(out[j].Item)
	})
	return out
}

// linesFor renders one item's collected amounts. One line when every amount converts to
// grams, otherwise one per unit family — and the note, which belongs to the item, rides
// on the first line either way.
func linesFor(item string, m *measured, grams map[model.ItemUnit]float64, heldGrams float64) []model.GroceryLine {
	blank := model.GroceryLine{
		Item:     strings.TrimSpace(m.first.Item),
		Category: "other",
		For:      m.forLabels,
	}
	if m.first.Category != nil {
		if _, ok := categoryRank[*m.first.Category]; ok {
			blank.Category = *m.first.Category
		}
	}
	if blank.For == nil {
		blank.For = []string{}
	}

	if len(m.entries) == 0 { // "to taste" and nothing else
		line := blank
		line.Note = m.note
		return []model.GroceryLine{line}
	}

	if qty, need, unit, ok := consolidate(item, m, grams, heldGrams); ok {
		line := blank
		line.Quantity, line.Unit, line.Note, line.Need = &qty, unit, m.note, need
		return []model.GroceryLine{line}
	}

	lines := make([]model.GroceryLine, 0, len(m.families))
	for i, family := range m.families {
		line := blank
		qty := math.Round(m.byFamily[family]*100) / 100
		line.Quantity = &qty
		if family != "" {
			f := family
			line.Unit = &f
		}
		if i == 0 {
			line.Note = m.note
		}
		lines = append(lines, line)
	}
	return lines
}

// consolidate sums an item's amounts through grams and renders the total back in the
// unit you would actually shop in. ok is false when any amount cannot be weighed, which
// is the caller's signal to fall back — a partial sum would be a wrong number, and a
// wrong number on a shopping list is worse than two lines.
// item is the group key -- the bare food -- and is used only to render the total back.
// The amounts themselves are weighed from each entry's own wording, because that is where
// the size modifier lives. Rendering back through the bare name is deliberate: a week
// asking for two large onions and one small one is three onions on the shelf.
func consolidate(item string, m *measured, grams map[model.ItemUnit]float64, heldGrams float64) (float64, *float64, *string, bool) {
	total := 0.0
	countable := false
	for _, e := range m.entries {
		g, ok := model.ToGrams(grams, e.item, e.unit, e.qty)
		if !ok {
			return 0, nil, nil, false
		}
		total += g
		if family, _ := model.NormalizeUnit(e.unit); family == "" {
			countable = true
		}
	}

	// render puts a gram amount back into the unit you would actually shop this item in.
	// Both the shortfall and the full demand go through it, so "2 of 3 onions" are the
	// same kind of number rather than one in pieces and one in grams.
	render := func(g float64) (float64, *string, bool) {
		// Pieces win when the item is bought in them and some recipe asked in them: "3
		// onions" is a shopping instruction, "450 g onion" is a maths problem. Rounded up,
		// because half an onion is not on the shelf.
		if _, ok := grams[model.ItemUnit{Item: item, Unit: ""}]; ok && countable {
			if pieces, ok := model.FromGrams(grams, item, nil, g); ok {
				return math.Ceil(pieces), nil, true
			}
		}
		// Otherwise the unit the week's first mention used, so milk stays in ml and flour in
		// g rather than everything collapsing to grams.
		unit := m.entries[0].unit
		family, _ := model.NormalizeUnit(unit)
		if qty, ok := model.FromGrams(grams, item, unit, g); ok {
			rounded := math.Round(qty*100) / 100
			if family == "" {
				return rounded, nil, true
			}
			return rounded, &family, true
		}
		return 0, nil, false
	}

	qty, unit, ok := render(total)
	if !ok {
		return 0, nil, nil, false
	}
	if heldGrams <= 0 {
		return qty, nil, unit, true
	}

	// The pantry covers part of the week. Shop for the rest — and only claim a reduction
	// when it actually is one: rounding up to whole pieces can leave the shortfall at the
	// same number as the demand, and "you have 1 of 3" next to "buy 3" is a lie.
	shop, _, ok := render(math.Max(0, total-heldGrams))
	if !ok || shop >= qty {
		return qty, nil, unit, true
	}
	return shop, &qty, unit, true
}

// GroceryAliasNames renders the names a week's ingredients need resolved, so the caller
// can fetch one alias table for the whole list — the same shape as GroceryPairs.
func GroceryAliasNames(ingredients []model.GroceryIngredient) []string {
	names := make([]string, 0, len(ingredients))
	for _, ing := range ingredients {
		names = append(names, model.NormalizeItem(ing.Item))
	}
	return names
}
