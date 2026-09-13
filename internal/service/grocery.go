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
	if ing.PlannedServings == nil || ing.RecipeServings == nil {
		return 1
	}
	if *ing.PlannedServings <= 0 || *ing.RecipeServings <= 0 {
		return 1
	}
	return float64(*ing.PlannedServings) / float64(*ing.RecipeServings)
}

// mealLabel names the meal a line serves: "Mon Roast chicken".
func mealLabel(plannedOn, title string) string {
	if d, err := time.Parse("2006-01-02", plannedOn); err == nil {
		return d.Format("Mon") + " " + title
	}
	return title
}

// BuildGroceryList consolidates a week's planned ingredients into shopping lines
// (BACKLOG 6.1): one line per item per unit family, quantities summed and scaled to the
// servings wanted on each day (6.2), each line tagged with the meals that need it.
//
// Amounts that cannot be summed ("to taste", "for garnish") are folded into that item's
// line as a note rather than dropped, and only become a line of their own when the item
// has no measured amount anywhere in the week.
func BuildGroceryList(ingredients []model.GroceryIngredient) []model.GroceryLine {
	type key struct{ item, family string }
	lines := []*model.GroceryLine{}
	byKey := map[key]*model.GroceryLine{}
	byItem := map[string]*model.GroceryLine{}

	newLine := func(k key, ing model.GroceryIngredient, unit *string, qty *float64) *model.GroceryLine {
		category := "other"
		if ing.Category != nil {
			if _, ok := categoryRank[*ing.Category]; ok {
				category = *ing.Category
			}
		}
		line := &model.GroceryLine{
			Item:     strings.TrimSpace(ing.Item),
			Category: category,
			Quantity: qty,
			Unit:     unit,
			For:      []string{},
		}
		lines = append(lines, line)
		byKey[k] = line
		if _, ok := byItem[k.item]; !ok {
			byItem[k.item] = line
		}
		return line
	}

	addFor := func(line *model.GroceryLine, ing model.GroceryIngredient) {
		label := mealLabel(ing.PlannedOn, ing.RecipeTitle)
		for _, existing := range line.For {
			if existing == label {
				return
			}
		}
		line.For = append(line.For, label)
	}

	// Measured amounts first, so a "to taste" mention in an earlier meal still attaches
	// to the line that carries the real quantity rather than splitting off its own.
	for _, ing := range ingredients {
		item := model.NormalizeItem(ing.Item)
		if item == "" || ing.Quantity == nil {
			continue
		}
		family, factor := model.NormalizeUnit(ing.Unit)
		k := key{item, family}
		line, ok := byKey[k]
		if !ok {
			unit := &family
			if family == "" {
				unit = nil
			}
			zero := 0.0
			line = newLine(k, ing, unit, &zero)
		}
		*line.Quantity += *ing.Quantity * factor * servingScale(ing)
		addFor(line, ing)
	}

	for _, ing := range ingredients {
		item := model.NormalizeItem(ing.Item)
		if item == "" || ing.Quantity != nil {
			continue
		}
		line, ok := byItem[item]
		if !ok {
			line = newLine(key{item, ""}, ing, nil, nil)
		}
		if ing.QuantityText != nil {
			if note := strings.TrimSpace(*ing.QuantityText); note != "" && !strings.Contains(line.Note, note) {
				if line.Note != "" {
					line.Note += ", "
				}
				line.Note += note
			}
		}
		addFor(line, ing)
	}

	out := make([]model.GroceryLine, 0, len(lines))
	for _, line := range lines {
		if line.Quantity != nil {
			rounded := math.Round(*line.Quantity*100) / 100
			line.Quantity = &rounded
		}
		out = append(out, *line)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return categoryRank[out[i].Category] < categoryRank[out[j].Category]
		}
		return strings.ToLower(out[i].Item) < strings.ToLower(out[j].Item)
	})
	return out
}
