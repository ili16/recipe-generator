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

// knownCanonicalsCap bounds the reuse list shown to the resolver (~1.5k tokens).
const knownCanonicalsCap = 400

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
Strip the grade, form and origin too, when what is left is the food a cook would name:
"granulated sugar" -> "sugar". "kosher salt" -> "salt". "extra-virgin olive oil" -> "olive oil".
"fresh madagascar vanilla bean" -> "vanilla bean". "large free-range egg" -> "egg".
Two names may only share a canonical if a cook would call them the same ingredient.

class: a short lower_snake_case name for the foods that could REPLACE this one in a
recipe. "spaghetti", "linguine" and "penne" are all "pasta_dry". "parmesan" and "pecorino"
are "hard_cheese". "butter" and "margarine" are "solid_fat".
This is not a category, and never the shelf the food sits on. "vegetable" is wrong,
because you cannot put an onion where a carrot was. "spice" is wrong for the same reason:
a vanilla bean is replaced by vanilla extract or vanilla paste, never by black pepper, so
its class is "vanilla" and not "spice". If nothing sensibly replaces the food -- salt,
water, egg -- answer null. Null is a better answer than a class that is merely a shelf.

You may be shown canonicals that already exist. If a name is the same food as one of them,
in any language, answer with that exact canonical rather than inventing a second spelling:
"trockenhefe" is "instant yeast" when "instant yeast" is listed. Only mint a new canonical
when none of them is the same food.

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
	// Best effort: without the list it is the old behaviour, so a failed read is not fatal.
	if known, err := storage.KnownCanonicals(ctx, knownCanonicalsCap); err == nil && len(known) > 0 {
		fmt.Fprintf(&b, "Existing canonicals: %s\n\nNames:\n", strings.Join(known, ", "))
	}
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
		byName[name] = model.Alias{Name: name, Canonical: canonical, Class: usableClass(e.Class)}
	}
	if len(byName) != len(names) {
		slog.WarnContext(ctx, "alias: answer did not cover the batch",
			"names", len(names), "answered", len(out.Aliases), "matched", len(byName))
	}
	return byName, nil
}

// categoryClasses are the answers the prompt forbids and the model gives anyway: a shelf
// in a supermarket rather than a food you could cook with. They are dropped on the way in
// because every reader of class treats it as "you could use this instead", and one
// "spice" row is enough to offer black pepper in place of a vanilla bean -- which is what
// it did. A food with no honest class has none; nothing is a worse answer than anything.
//
// Listed singular; a trailing "s" is trimmed before the lookup, so "herbs" is "herb".
// Deliberately only the bare taxonomy words: "hard_cheese" and "liquid_fat" are real
// classes and stay.
var categoryClasses = map[string]bool{
	"spice": true, "ground_spice": true, "whole_spice": true, "seasoning": true,
	"herb": true, "dried_herb": true, "fresh_herb": true, "aromatic": true,
	"vegetable": true, "fruit": true, "produce": true, "dairy": true,
	"meat": true, "protein": true, "seafood": true, "grain": true, "legume": true,
	"nut": true, "sauce": true, "condiment": true, "baking": true, "flavouring": true,
	"flavoring": true, "extract": true, "topping": true, "garnish": true,
	"liquid": true, "food": true, "ingredient": true, "pantry": true, "other": true,
	"misc": true,
	// "no class" spelled as a word. The schema has a real JSON null and the prompt asks
	// for it, but a model that answers in prose writes this instead -- and a class of
	// "null" matched every other food that said "null", which is how a pantry holding
	// salt came to be offered as a substitute for vegetable stock.
	"null": true, "none": true, "nil": true, "n/a": true, "na": true, "unknown": true,
}

// usableClass is the one gate between what the model called a substitution family and
// what the app will offer a cook. Empty, absent or a category answer all mean the same
// thing: this food has no stand-in. Nil and not "" -- the column's NULL is what every
// reader tests.
func usableClass(class *string) *string {
	if class == nil {
		return nil
	}
	c := strings.ToLower(strings.TrimSpace(*class))
	if c == "" || categoryClasses[c] || categoryClasses[strings.TrimSuffix(c, "s")] {
		return nil
	}
	return &c
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

// LearnPantryItems is the same pair of background lookups a recipe save triggers, for the
// other way a food gets into the app: somebody typing it into their kitchen. Without it a
// hand-typed "Zwiebel" is a food the app has never heard of — it matches no recipe asking
// for an onion, weighs nothing, and offers no swap — until the next deploy's backfill.
//
// Both halves are the recipe's, deliberately: a pantry line and an ingredient line are the
// same claim about the same food, and the moment they resolve differently the pantry match
// is comparing two different keys. That is also why this takes the item rather than the
// name — grams needs the unit.
func LearnPantryItems(items ...model.PantryItem) {
	ingredients := make([]model.RecipeIngredient, 0, len(items))
	for _, item := range items {
		ingredients = append(ingredients, model.RecipeIngredient{Item: item.Name, Quantity: item.Quantity, Unit: item.Unit})
	}
	LearnAliases(ingredients)
	LearnGrams(ingredients)
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

// auditClassPrompt reviews a family instead of a food, because that is the shape the
// mistake takes. Asked "what class is black pepper?", a model answering "spice" is not
// obviously wrong; shown "spice = {vanilla bean, black pepper, nutmeg}" it can see that no
// cook would pour pepper into a custard.
//
// It is asked to answer only about what does NOT belong, so a healthy library is a nearly
// empty answer and the cost of a run scales with the number of mistakes rather than the
// size of the table.
const auditClassPrompt = `You check groups of foods that a cooking app treats as INTERCHANGEABLE.

Each group is a class name and its members. The app offers any member in place of any
other, so a group is only correct if a cook would accept every swap inside it.

Report only the members that do NOT belong. For each, give the class it should have
instead -- a small family of things that really could replace it -- or null if nothing
sensibly replaces that food at all.

Judge the swap a cook would actually make, not the aisle the food sits in.
"spice", "vegetable", "dairy", "sauce" and the like are always wrong as a class: they are
shelves, and you cannot put an onion where a carrot was. A vanilla bean belongs with
vanilla extract and vanilla paste, never with black pepper.
A group that is already correct produces no entries at all. Say nothing about it.

Every name you report must be a name that was given to you, spelled exactly as it was.`

// ClassFix is one food the audit wants moved out of its family, and where to. Class is
// nil when the answer is that nothing substitutes for it.
type ClassFix struct {
	Name  string
	Was   string
	Class *string
}

// AuditClasses re-reads the substitution families already in the table and reports the
// members that do not belong (cmd/backfill-aliases -audit). It writes nothing: the caller
// decides, because a second pass from the same family of model catches disagreement, not
// truth -- it will find pepper sitting next to vanilla, and it will not know that
// Weizenmehl is flour (BACKLOG 17.6).
func AuditClasses(ctx context.Context) ([]ClassFix, error) {
	groups, err := storage.ClassGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("read classes: %w", err)
	}
	if len(groups) == 0 {
		return nil, nil
	}

	classes := make([]string, 0, len(groups))
	for c := range groups {
		classes = append(classes, c)
	}
	slices.Sort(classes)

	var fixes []ClassFix
	for chunk := range slices.Chunk(classes, auditBatchSize) {
		answers, err := reviewClasses(ctx, groups, chunk)
		if err != nil {
			return nil, err
		}
		fixes = append(fixes, answers...)
	}
	return fixes, nil
}

// auditBatchSize is in classes, not foods — a family is a line or two, so the whole
// library is one or two calls, the same trade as aliasBatchSize.
const auditBatchSize = 40

// reviewClasses asks about one batch of families and keeps only the answers that name a
// food actually in one of them. The model is writing names back rather than picking an
// index, so an invented or misspelled name is the failure to guard: it would otherwise
// become an UPDATE against a row nobody audited, or a silent no-op that still got counted.
func reviewClasses(ctx context.Context, groups map[string][]string, classes []string) ([]ClassFix, error) {
	type entry struct {
		Name  string  `json:"name"`
		Class *string `json:"class"`
	}
	type answer struct {
		Wrong []entry `json:"wrong"`
	}

	// member -> the class it is being audited in, which is also the whitelist.
	audited := map[string]string{}
	var b strings.Builder
	for _, c := range classes {
		fmt.Fprintf(&b, "%s: %s\n", c, strings.Join(groups[c], ", "))
		for _, m := range groups[c] {
			audited[m] = c
		}
	}

	out, _, err := llm.Call[answer](ctx, llm.Spec{
		Model:    assistModel,
		CallType: "alias_class_audit",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(auditClassPrompt),
			openai.UserMessage(b.String()),
		},
		Schema: &llm.Schema{Name: "class_audit", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"wrong": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":  map[string]any{"type": "string"},
							"class": llm.Nullable("string"),
						},
						"required":             []string{"name", "class"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"wrong"},
			"additionalProperties": false,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("audit classes: %w", err)
	}
	wrong := make(map[string]*string, len(out.Wrong))
	for _, e := range out.Wrong {
		wrong[e.Name] = e.Class
	}
	return applicableFixes(audited, wrong), nil
}

// applicableFixes keeps the answers that name a food the batch actually asked about, and
// runs the replacement class through the same gate a fresh resolve goes through — an
// audit that swapped one shelf name for another would be worse than no audit at all.
// A fix that changes nothing is dropped here rather than costing an UPDATE.
//
// Two answers about the same food cancel each other out. Names are normalised before the
// lookup, so "Vanilla Bean, ground" and "vanilla bean" are one row, and if the model said
// different things about them it has not made up its mind — the same reasoning as
// EnsureAliases refusing to write an answer it cannot match: leave the row alone and let a
// later run try again, rather than pick one at random and call it a correction.
func applicableFixes(audited map[string]string, wrong map[string]*string) []ClassFix {
	names := make([]string, 0, len(wrong))
	for n := range wrong {
		names = append(names, n)
	}
	slices.Sort(names)

	byName := make(map[string]ClassFix, len(names))
	conflicted := map[string]bool{}
	for _, raw := range names {
		name := model.NormalizeItem(raw)
		was, ok := audited[name]
		if !ok {
			slog.Warn("alias audit: answer named a food that was not in the batch", "name", raw)
			continue
		}
		class := usableClass(wrong[raw])
		if class != nil && *class == was {
			continue
		}
		if prev, seen := byName[name]; seen && !sameClass(prev.Class, class) {
			slog.Warn("alias audit: two different answers for one food, leaving it alone", "name", name)
			conflicted[name] = true
			continue
		}
		byName[name] = ClassFix{Name: name, Was: was, Class: class}
	}

	fixes := make([]ClassFix, 0, len(byName))
	for name, f := range byName {
		if !conflicted[name] {
			fixes = append(fixes, f)
		}
	}
	slices.SortFunc(fixes, func(a, b ClassFix) int { return strings.Compare(a.Name, b.Name) })
	return fixes
}

func sameClass(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

const auditCanonicalPrompt = `You check a cooking app's ingredient names for one food split across two spellings.

Each line is a substitution class and the distinct canonical names inside it. Report every
set of canonicals that are the SAME food -- what a cook would buy as one item -- including
the same food in two languages ("trockenhefe" and "instant yeast"). Spaghetti and penne are
different foods. Say nothing about groups that are fine.

Give each set as canonical names spelled exactly as given.`

// CanonicalSplit is a set of canonicals the audit believes are one food.
type CanonicalSplit struct {
	Class      string
	Canonicals []string
}

// AuditCanonicals reports foods split across canonicals within a class (BACKLOG 18.0). It
// writes nothing: canonical is an identity, so merging is a migration, not an audit fix.
func AuditCanonicals(ctx context.Context) ([]CanonicalSplit, error) {
	groups, err := storage.ClassCanonicals(ctx)
	if err != nil {
		return nil, fmt.Errorf("read classes: %w", err)
	}
	classes := make([]string, 0, len(groups))
	for c := range groups {
		classes = append(classes, c)
	}
	slices.Sort(classes)

	type set struct {
		Class      string   `json:"class"`
		Canonicals []string `json:"canonicals"`
	}
	type answer struct {
		Same []set `json:"same"`
	}
	var splits []CanonicalSplit
	for chunk := range slices.Chunk(classes, auditBatchSize) {
		var b strings.Builder
		known := map[string]map[string]bool{}
		for _, c := range chunk {
			fmt.Fprintf(&b, "%s: %s\n", c, strings.Join(groups[c], ", "))
			known[c] = map[string]bool{}
			for _, m := range groups[c] {
				known[c][m] = true
			}
		}
		out, _, err := llm.Call[answer](ctx, llm.Spec{
			Model:    assistModel,
			CallType: "alias_canonical_audit",
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage(auditCanonicalPrompt),
				openai.UserMessage(b.String()),
			},
			Schema: &llm.Schema{Name: "canonical_audit", Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"same": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"class":      map[string]any{"type": "string"},
								"canonicals": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							},
							"required":             []string{"class", "canonicals"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"same"},
				"additionalProperties": false,
			}},
		})
		if err != nil {
			return nil, fmt.Errorf("audit canonicals: %w", err)
		}
		for _, s := range out.Same {
			// The model writes names back; keep only sets of real canonicals from that class.
			var ok []string
			for _, c := range s.Canonicals {
				if known[s.Class][c] {
					ok = append(ok, c)
				}
			}
			if len(ok) > 1 {
				splits = append(splits, CanonicalSplit{Class: s.Class, Canonicals: ok})
			}
		}
	}
	return splits, nil
}
