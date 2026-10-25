package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
)

// aisleBatchSize is how many ingredient names go into one classification call. Big
// enough that a normal library is one or two calls, small enough that a single bad
// response doesn't cost the whole run.
const aisleBatchSize = 100

// aisleChoices are model.GroceryCategories with the rules that used to be a system
// prompt, now attached to the option they describe.
var aisleChoices = []llm.Choice{
	{Value: "produce", Description: "Fresh fruit, vegetables and herbs."},
	{Value: "protein", Description: "Meat, fish and eggs."},
	{Value: "dairy", Description: "Milk, cheese, yoghurt, butter and cream."},
	{Value: "pantry", Description: "Anything dry, tinned, bottled, frozen, or a spice."},
	{Value: "other", Description: "Only when it genuinely cannot be told."},
}

// minAisleConfidence is below which an answer is left unwritten. NULL already renders as
// "other" and stays on the worklist, so a coin-flip costs nothing to skip and a wrong
// aisle costs a trip down the wrong one.
// ponytail: guessed, not calibrated; tune against a labelled sample if skips pile up.
const minAisleConfidence = 0.6

// BackfillIngredientAisles fills recipe_ingredients.category for ingredients saved
// before BACKLOG 6.1 taught the generation call to emit one. Names are classified once
// each, in batched assist-model calls, and written to every recipe that uses them.
//
// "other" answers are not written: NULL already groups as "other" on the list, so
// storing it would only cost a future, better-informed run the chance to retry. A run
// with nothing uncategorised makes no LLM call at all.
func BackfillIngredientAisles(ctx context.Context, dryRun bool) (classified int, rowsUpdated int64, err error) {
	items, err := storage.UncategorisedItems(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("read uncategorised items: %w", err)
	}
	if len(items) == 0 {
		slog.InfoContext(ctx, "aisle backfill: nothing to do")
		return 0, 0, nil
	}
	slog.InfoContext(ctx, "aisle backfill: start", "items", len(items), "dry_run", dryRun)

	for chunk := range slices.Chunk(items, aisleBatchSize) {
		aisles, err := classifyAisles(ctx, chunk)
		if err != nil {
			return classified, rowsUpdated, err
		}
		names, cats := pickPairs(chunk, aisles)
		if len(names) == 0 {
			continue
		}
		classified += len(names)
		if dryRun {
			slog.InfoContext(ctx, "aisle backfill: would write", "pairs", len(names), "sample", sampleOf(names, cats))
			continue
		}
		n, err := storage.SetItemCategories(ctx, names, cats)
		if err != nil {
			return classified, rowsUpdated, fmt.Errorf("write categories: %w", err)
		}
		rowsUpdated += n
	}
	slog.InfoContext(ctx, "aisle backfill: done", "classified", classified, "rows", rowsUpdated, "dry_run", dryRun)
	return classified, rowsUpdated, nil
}

// pickPairs keeps only the answers worth writing, so one unusable answer costs its own
// item and not the batch. An item the model dropped or reworded simply isn't in the map
// and stays NULL for a later run; "other" is not written because NULL already groups as
// "other" on the list, and storing it would cost a better-informed run the chance to retry.
func pickPairs(chunk []string, aisles map[string]string) (names, cats []string) {
	for _, item := range chunk {
		a, ok := aisles[item]
		if !ok {
			continue
		}
		if a != "other" && slices.Contains(model.GroceryCategories, a) {
			names = append(names, item)
			cats = append(cats, a)
		}
	}
	return names, cats
}

// classifyAisles asks the Decisions API one choice question per item and returns the
// confident answers keyed by item name. Each question is named by its index in the
// batch, a name we chose and the API echoes back verbatim, so the join that once
// mislabelled everything after an inserted answer (111 aisles for 100 items) has nothing
// left to drift: an answer cannot be reworded, invented, or outside the vocabulary.
func classifyAisles(ctx context.Context, items []string) (map[string]string, error) {
	questions := make([]llm.Question, len(items))
	for i, item := range items {
		questions[i] = llm.Question{
			Type:         "choice",
			Name:         fmt.Sprintf("i%d", i),
			Instructions: fmt.Sprintf("Which supermarket aisle is %q bought from?", item),
			Choices:      aisleChoices,
		}
	}
	answers, err := llm.Decide(ctx, "aisle_backfill", "Sort grocery items into supermarket aisles.", questions)
	if err != nil {
		return nil, fmt.Errorf("classify aisles: %w", err)
	}
	return aislesFrom(items, answers), nil
}

// aislesFrom keeps the confident choice answers. A refusal, a missing answer or a
// low-confidence one leaves its item out, so it stays NULL for a later run.
func aislesFrom(items []string, answers map[string]llm.Answer) map[string]string {
	byItem := make(map[string]string, len(items))
	for i, item := range items {
		a, ok := answers[fmt.Sprintf("i%d", i)]
		if ok && a.Type == "choice" && a.Confidence >= minAisleConfidence {
			byItem[item] = a.Choice
		}
	}
	return byItem
}

func sampleOf(names, cats []string) string {
	var b strings.Builder
	for i := 0; i < len(names) && i < 5; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s→%s", names[i], cats[i])
	}
	return b.String()
}
