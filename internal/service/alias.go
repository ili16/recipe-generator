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

// aliasBatchSize is how many names go into one call — same reasoning as gramsBatchSize: a
// whole library is one or two calls, and a bad response costs a batch rather than the run.
const aliasBatchSize = 100

// The two answers this prompt gives are deliberately different kinds of claim, and the
// wording works hard to keep them apart. canonical is an identity and gets merged into
// shopping lists and subtracted from fridges; class is only ever a suggestion. A model
// that treats class as a taxonomy ("vegetable") would make 17.4 offer an onion in place
// of a carrot, so the instruction is about what you could actually cook with.
const aliasSystemPrompt = `You normalise the names of foods.
For every name you are given, answer with two things.

canonical: the ordinary English name of the SAME food.
"parmigiano reggiano" -> "parmesan". "aubergine" -> "eggplant". "zwiebel" -> "onion".
"spring onion" -> "spring onion". If the name is already the ordinary English name, repeat
it unchanged. Always singular, never a brand, never a preparation, never a size.
Two names may only share a canonical if a cook would call them the same ingredient.

class: a short lower_snake_case name for the foods that could REPLACE this one in a
recipe. "spaghetti", "linguine" and "penne" are all "pasta_dry". "parmesan" and "pecorino"
are "hard_cheese". "butter" and "margarine" are "solid_fat".
This is not a category. "vegetable" is wrong, because you cannot put an onion where a
carrot was. If nothing sensibly replaces the food — salt, water, egg — answer null.

Answer for every name you were given, echoing each name back exactly as it was given.`

// EnsureAliases resolves the names ingredient_alias cannot yet answer and writes them, so
// that later reads are pure DB lookups. A name already resolved costs nothing and makes
// no call.
//
// Failure is not fatal to the caller by design, exactly as with EnsureGrams: an
// unresolved name resolves to itself (model.CanonicalItem, canonical_item()), which is
// precisely the behaviour every caller had before this table existed.
func EnsureAliases(ctx context.Context, names []string) error {
	missing, err := unresolvedAliases(ctx, names)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}
	slog.InfoContext(ctx, "alias: resolving", "names", len(missing))

	for chunk := range slices.Chunk(missing, aliasBatchSize) {
		answers, err := classifyAliases(ctx, chunk)
		if err != nil {
			return err
		}
		rows := make([]model.Alias, 0, len(chunk))
		for _, n := range chunk {
			// Only a name the model echoed back exactly, with a usable canonical, is
			// written. Anything else stays missing so a later run can retry it —
			// writing a guess would freeze it, since UpsertAliases never overwrites.
			a, ok := answers[n]
			if !ok || a.Canonical == "" {
				continue
			}
			rows = append(rows, a)
		}
		if err := storage.UpsertAliases(ctx, rows); err != nil {
			return fmt.Errorf("write aliases: %w", err)
		}
	}
	return nil
}

// unresolvedAliases narrows names to the ones the table cannot answer yet, deduplicated
// and normalised the way the key is normalised.
func unresolvedAliases(ctx context.Context, names []string) ([]string, error) {
	wanted := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, n := range names {
		n = model.NormalizeItem(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		wanted = append(wanted, n)
	}
	if len(wanted) == 0 {
		return nil, nil
	}

	table, err := storage.AliasTable(ctx, wanted)
	if err != nil {
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	missing := wanted[:0:0]
	for _, n := range wanted {
		if _, ok := table[n]; !ok {
			missing = append(missing, n)
		}
	}
	return missing, nil
}

// classifyAliases asks the assist model for each name's canonical and class, keyed back by
// the name itself. The name is echoed back and used as the join rather than the array
// index — the same lesson as classifyAisles and weighPairs: an answer array that drifts by
// one silently mislabels everything after it, and here that would merge two foods that are
// not the same food.
func classifyAliases(ctx context.Context, names []string) (map[string]model.Alias, error) {
	type entry struct {
		Name      string  `json:"name"`
		Canonical string  `json:"canonical"`
		Class     *string `json:"class"`
	}
	type answer struct {
		Aliases []entry `json:"aliases"`
	}

	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s\n", n)
	}

	out, _, err := llm.Call[answer](ctx, llm.Spec{
		Model:    assistModel,
		CallType: "alias_lookup",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(aliasSystemPrompt),
			openai.UserMessage(b.String()),
		},
		Schema: &llm.Schema{Name: "aliases", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"aliases": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":      map[string]any{"type": "string"},
							"canonical": map[string]any{"type": "string"},
							"class":     llm.Nullable("string"),
						},
						"required":             []string{"name", "canonical", "class"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"aliases"},
			"additionalProperties": false,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("classify aliases: %w", err)
	}

	byName := make(map[string]model.Alias, len(out.Aliases))
	for _, e := range out.Aliases {
		name := model.NormalizeItem(e.Name)
		canonical := model.NormalizeItem(e.Canonical)
		if name == "" || canonical == "" {
			continue
		}
		// A class is a family name, not a food, so it is only lowercased and trimmed.
		// An empty one is nil, not "": nothing substitutes for salt.
		var class *string
		if e.Class != nil {
			if c := strings.ToLower(strings.TrimSpace(*e.Class)); c != "" {
				class = &c
			}
		}
		byName[name] = model.Alias{Name: name, Canonical: canonical, Class: class}
	}
	if len(byName) != len(names) {
		slog.WarnContext(ctx, "alias: answer did not cover the batch",
			"names", len(names), "answered", len(out.Aliases), "matched", len(byName))
	}
	return byName, nil
}

// LearnAliases resolves a saved recipe's ingredient names in the background, so the next
// grocery list can put its "parmigiano reggiano" on the same line as another recipe's
// "parmesan", and so a German recipe and an English one stop shopping twice for an onion.
//
// Detached from the request for the same reason LearnGrams is: it is an LLM call on the
// tail of a save the user is waiting on, and nothing breaks if it loses. An unresolved
// name resolves to itself, and cmd/backfill-aliases picks up whatever this dropped.
func LearnAliases(ingredients []model.RecipeIngredient) {
	names := AliasNamesOf(ingredients)
	if len(names) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), aliasLearnTimeout)
		defer cancel()
		if err := EnsureAliases(ctx, names); err != nil {
			slog.WarnContext(ctx, "alias: background learn failed", "error", err, "names", len(names))
		}
	}()
}

// aliasLearnTimeout bounds the detached call — it outlives the request, so nothing else
// would ever stop it.
const aliasLearnTimeout = 2 * time.Minute

// AliasNamesOf renders the names a recipe's ingredient list needs resolved. Unlike
// GramsPairsOf this does not skip an unmeasured ingredient: "salt to taste" still has a
// name, and the pantry match keys on the name whether or not there is a quantity.
func AliasNamesOf(ingredients []model.RecipeIngredient) []string {
	names := make([]string, 0, len(ingredients))
	for _, ing := range ingredients {
		names = append(names, model.NormalizeItem(ing.Item))
	}
	return names
}
