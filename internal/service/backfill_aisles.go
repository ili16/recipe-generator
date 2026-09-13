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
	"github.com/openai/openai-go"
)

// aisleBatchSize is how many ingredient names go into one classification call. Big
// enough that a normal library is one or two calls, small enough that a single bad
// response doesn't cost the whole run.
const aisleBatchSize = 100

const aisleSystemPrompt = `You sort grocery items into supermarket aisles.
For every item you are given, answer with the aisle it is bought from, one of:
produce, protein, dairy, pantry, other.
Rules: fresh fruit and vegetables and herbs are produce; meat, fish and eggs are
protein; milk, cheese, yoghurt, butter and cream are dairy; anything dry, tinned,
bottled, frozen or a spice is pantry; use other only when you genuinely cannot tell.
Answer for every item you were given, in the same order, and change nothing about the
item names.`

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
		// Keep only the pairs worth writing, so one unusable answer costs its own item
		// and not the batch.
		var names, cats []string
		for i, item := range chunk {
			if i >= len(aisles) {
				break
			}
			if a := aisles[i]; a != "other" && slices.Contains(model.GroceryCategories, a) {
				names = append(names, item)
				cats = append(cats, a)
			}
		}
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

// classifyAisles asks the assist model for one aisle per item, in order. The schema
// pins the answer to a plain string array so there is no name to match back up — the
// index is the join.
func classifyAisles(ctx context.Context, items []string) ([]string, error) {
	type answer struct {
		Aisles []string `json:"aisles"`
	}
	out, _, err := llm.Call[answer](ctx, llm.Spec{
		Model:    assistModel,
		CallType: "aisle_backfill",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(aisleSystemPrompt),
			openai.UserMessage(strings.Join(items, "\n")),
		},
		Schema: &llm.Schema{Name: "aisles", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"aisles": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string", "enum": model.GroceryCategories},
				},
			},
			"required":             []string{"aisles"},
			"additionalProperties": false,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("classify aisles: %w", err)
	}
	if len(out.Aisles) != len(items) {
		// A short answer is usable up to where it stops; a long one is misaligned past
		// the end of the batch and the extras are dropped by the caller's index guard.
		slog.WarnContext(ctx, "aisle backfill: answer length mismatch", "items", len(items), "aisles", len(out.Aisles))
	}
	return out.Aisles, nil
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
