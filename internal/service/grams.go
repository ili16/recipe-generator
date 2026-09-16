package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/openai/openai-go"
)

// gramsBatchSize is how many conversions go into one call — same reasoning as
// aisleBatchSize: a whole library is one or two calls, and a bad response costs a batch
// rather than the run.
const gramsBatchSize = 100

const gramsSystemPrompt = `You convert cooking measures to grams.
For every item and unit you are given, answer with the weight in grams of ONE of that
unit of that item.
An empty unit means one whole piece of the item: ("onion", "") is one medium onion,
about 150 grams. ("egg", "") is one egg, about 50 grams.
A named unit is that measure of that specific item: ("flour", "cup") is about 120 grams,
("sugar", "cup") is about 200 grams, ("olive oil", "tbsp") is about 14 grams.
Use ordinary supermarket sizes and standard metric kitchen measures. Answer with a
single number of grams, always greater than zero.
Answer for every pair you were given, in the same order, and change nothing about the
item names or the units.`

// EnsureGrams resolves the (item, unit) pairs that ingredient_grams cannot yet answer
// and writes them, so that later reads are pure DB lookups. Pairs already resolvable —
// by their own row, by a wildcard, or by being grams — cost nothing and make no call.
//
// Failure is not fatal to the caller by design: a missing conversion degrades the
// grocery list to today's behaviour (one line per unit family) and makes a pantry
// deduction skip that ingredient rather than guess at it. cmd/backfill-grams retries
// whatever a live call could not get.
func EnsureGrams(ctx context.Context, pairs []model.ItemUnit) error {
	missing, err := unresolvedGrams(ctx, pairs)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	slog.InfoContext(ctx, "grams: resolving", "pairs", len(missing))

	for chunk := range slices.Chunk(missing, gramsBatchSize) {
		answers, err := weighPairs(ctx, chunk)
		if err != nil {
			return err
		}
		rows := make([]model.GramsRow, 0, len(chunk))
		for _, p := range chunk {
			// Only a pair the model echoed back exactly, with a usable number, is
			// written. Anything else stays missing so a later run can retry it —
			// writing a guess would freeze it, since UpsertGrams never overwrites.
			if g, ok := answers[p]; ok && g > 0 {
				rows = append(rows, model.GramsRow{ItemUnit: p, Grams: g, Source: "llm"})
			}
		}
		if err := storage.UpsertGrams(ctx, rows); err != nil {
			return fmt.Errorf("write grams: %w", err)
		}
	}
	return nil
}

// unresolvedGrams narrows pairs to the ones nothing can answer yet, deduplicated. A pair
// whose unit is grams is never unresolved — it is the identity.
func unresolvedGrams(ctx context.Context, pairs []model.ItemUnit) ([]model.ItemUnit, error) {
	wanted := make([]model.ItemUnit, 0, len(pairs))
	seen := map[model.ItemUnit]bool{}
	for _, p := range pairs {
		p.Item = model.NormalizeItem(p.Item)
		if p.Item == "" || p.Item == model.GramsWildcard || p.Unit == "g" || seen[p] {
			continue
		}
		seen[p] = true
		wanted = append(wanted, p)
	}
	if len(wanted) == 0 {
		return nil, nil
	}

	table, err := storage.GramsTable(ctx, wanted)
	if err != nil {
		return nil, fmt.Errorf("read grams: %w", err)
	}
	missing := wanted[:0:0]
	for _, p := range wanted {
		unit := p.Unit
		if _, ok := model.ToGrams(table, p.Item, &unit, 1); !ok {
			missing = append(missing, p)
		}
	}
	return missing, nil
}

// weighPairs asks the assist model for the grams of one of each pair, keyed back by the
// pair itself. The item and unit are echoed back and used as the join rather than the
// array index — the same lesson as classifyAisles (backfill_aisles.go): an answer array
// that drifts by one silently mislabels everything after it, and here that would mean
// subtracting the wrong amount from somebody's fridge.
func weighPairs(ctx context.Context, pairs []model.ItemUnit) (map[model.ItemUnit]float64, error) {
	type weight struct {
		Item  string  `json:"item"`
		Unit  string  `json:"unit"`
		Grams float64 `json:"grams"`
	}
	type answer struct {
		Weights []weight `json:"weights"`
	}

	var b strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&b, "%s\t%s\n", p.Item, p.Unit)
	}

	out, _, err := llm.Call[answer](ctx, llm.Spec{
		Model:    assistModel,
		CallType: "grams_lookup",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(gramsSystemPrompt),
			openai.UserMessage(b.String()),
		},
		Schema: &llm.Schema{Name: "weights", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"weights": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"item":  map[string]any{"type": "string"},
							"unit":  map[string]any{"type": "string"},
							"grams": map[string]any{"type": "number"},
						},
						"required":             []string{"item", "unit", "grams"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"weights"},
			"additionalProperties": false,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("weigh pairs: %w", err)
	}

	byPair := make(map[model.ItemUnit]float64, len(out.Weights))
	for _, w := range out.Weights {
		byPair[model.ItemUnit{Item: model.NormalizeItem(w.Item), Unit: strings.ToLower(strings.TrimSpace(w.Unit))}] = w.Grams
	}
	if len(byPair) != len(pairs) {
		slog.WarnContext(ctx, "grams: answer did not cover the batch",
			"pairs", len(pairs), "answered", len(out.Weights), "matched", len(byPair))
	}
	return byPair, nil
}

// LearnGrams resolves a saved recipe's conversions in the background, so the next
// grocery list can sum its "2 onions" with another recipe's "150 g onion".
//
// Detached from the request on purpose: it is an LLM call on the tail of a save the user
// is waiting on, and nothing breaks if it loses. A missing conversion degrades the
// grocery list to one line per unit family — exactly today's behaviour — and
// cmd/backfill-grams picks up whatever this dropped. That is also why the error is
// logged rather than returned: there is no caller left to hand it to.
func LearnGrams(ingredients []model.RecipeIngredient) {
	pairs := GramsPairsOf(ingredients)
	if len(pairs) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), gramsLearnTimeout)
		defer cancel()
		if err := EnsureGrams(ctx, pairs); err != nil {
			slog.WarnContext(ctx, "grams: background learn failed", "error", err, "pairs", len(pairs))
		}
	}()
}

// gramsLearnTimeout bounds the detached call — it outlives the request, so nothing else
// would ever stop it.
const gramsLearnTimeout = 2 * time.Minute

// GramsPairsOf renders the conversions a recipe's ingredient list needs, ready for
// EnsureGrams. Units are normalised the way everything else normalises them, so a
// recipe written in kg asks for nothing new.
func GramsPairsOf(ingredients []model.RecipeIngredient) []model.ItemUnit {
	pairs := make([]model.ItemUnit, 0, len(ingredients))
	for _, ing := range ingredients {
		if ing.Quantity == nil {
			continue // "to taste" is never converted, so it needs no conversion
		}
		family, _ := model.NormalizeUnit(ing.Unit)
		pairs = append(pairs, model.ItemUnit{Item: model.NormalizeItem(ing.Item), Unit: family})
	}
	return pairs
}
