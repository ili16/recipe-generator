package storage

import (
	"context"
	"math"
	"sort"
	"strconv"

	"github.com/ili16/recipe-generator/internal/model"
)

// Traversal over the per-user graph (BACKLOG 18.2). The graph is a few thousand edges at
// most (one kitchen's library), so it is read once through GraphEdges and walked in memory —
// the same scope guarantee, no recursive CTE to keep in step with the view.
// ponytail: O(edges) per query; a CTE or cache if a scope ever holds 100k+ edges.

// SimilarRecipe is one row of a "recipes like this one" answer.
type SimilarRecipe struct {
	ID    int     `json:"id"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
}

// maxNeighbourDepth caps neighbours(): past 3 hops everything is adjacent to everything.
const maxNeighbourDepth = 3

const (
	// edgeFloor: how close to a recipe's best neighbour a pair must score to be worth drawing,
	// and how rare a shared food must be to be worth naming. Relative rather than absolute, so
	// a library where everything is alike still links up.
	edgeFloor = 0.4
	// maxSharedItems: a tooltip, not an ingredient list.
	maxSharedItems = 6
)

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for k := range a {
		if b[k] {
			shared++
		}
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
}

// ingredientIDF weights each canonical ingredient by how rare it is in this kitchen. Salt is in
// almost every recipe, so two dishes sharing it is no evidence that they are alike; saffron is.
// A food more than half the kitchen uses weighs exactly nothing — sharing only staples must
// score 0 and draw no edge at all, which a merely small weight would not achieve when every
// candidate is equally staple-bound (crème brûlée against a savoury library). The df > 2 guard
// keeps a three-recipe library, where everything is in "half" of it, from erasing itself.
func ingredientIDF(uses map[string]map[string]bool) map[string]float64 {
	df := map[string]int{}
	for _, set := range uses {
		for item := range set {
			df[item]++
		}
	}
	w := make(map[string]float64, len(df))
	for item, n := range df {
		if n > 2 && 2*n > len(uses) {
			continue
		}
		w[item] = math.Log(float64(len(uses)+1) / (float64(n) + 0.5))
	}
	return w
}

// weightedJaccard is jaccard with each member worth its weight instead of 1.
func weightedJaccard(a, b map[string]bool, w map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var shared, union float64
	for k := range a {
		union += w[k]
		if b[k] {
			shared += w[k]
		}
	}
	for k := range b {
		if !a[k] {
			union += w[k]
		}
	}
	if union == 0 {
		return 0
	}
	return shared / union
}

// rankSimilar scores every other recipe against id: rarity-weighted Jaccard over canonical
// ingredients (so a shared staple counts for next to nothing), half
// as much for Jaccard over their classes (so butter-vs-margarine still counts), plus small
// bonuses for the same cuisine and shared techniques. Zero-score recipes are dropped.
func rankSimilar(edges []GraphEdge, id string) []SimilarRecipe {
	class := map[string]string{}
	for _, e := range edges {
		if e.Rel == "is_a" {
			class[e.Src] = e.Dst
		}
	}
	ing, cls, feat := map[string]map[string]bool{}, map[string]map[string]bool{}, map[string]map[string]bool{}
	add := func(m map[string]map[string]bool, r, v string) {
		if m[r] == nil {
			m[r] = map[string]bool{}
		}
		m[r][v] = true
	}
	for _, e := range edges {
		if e.SrcType != "recipe" {
			continue
		}
		switch {
		case e.Rel == "uses":
			add(ing, e.Src, e.Dst)
			if c, ok := class[e.Dst]; ok {
				add(cls, e.Src, c)
			}
		case e.DstType == "cuisine" || e.DstType == "technique":
			add(feat, e.Src, e.DstType+":"+e.Dst)
		}
	}
	w := ingredientIDF(ing)
	var out []SimilarRecipe
	for other := range ing {
		if other == id {
			continue
		}
		s := weightedJaccard(ing[id], ing[other], w) + 0.5*jaccard(cls[id], cls[other]) + 0.25*jaccard(feat[id], feat[other])
		if s > 0 {
			n, _ := strconv.Atoi(other)
			out = append(out, SimilarRecipe{ID: n, Score: s})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RecipeLink is one recipe↔recipe edge of the overlap map (BACKLOG 18.3): two recipes that
// share food, how alike they are, and which foods they share.
type RecipeLink struct {
	A      int      `json:"a"`
	B      int      `json:"b"`
	Score  float64  `json:"score"`
	Shared []string `json:"shared"`
}

// overlapLinks keeps each recipe's `top` closest neighbours and drops the rest. The full
// pairwise matrix is a hairball — which is exactly what the first two map attempts looked
// like — so sparseness is the feature, not an optimisation.
func overlapLinks(edges []GraphEdge, top int) []RecipeLink {
	uses := map[string]map[string]bool{}
	var ids []string
	for _, e := range edges {
		if e.SrcType != "recipe" || e.Rel != "uses" {
			continue
		}
		if uses[e.Src] == nil {
			uses[e.Src] = map[string]bool{}
			ids = append(ids, e.Src)
		}
		uses[e.Src][e.Dst] = true
	}
	sort.Strings(ids)
	w := ingredientIDF(uses)
	seen := map[[2]int]bool{}
	out := []RecipeLink{}
	for _, id := range ids {
		a, _ := strconv.Atoi(id)
		var best float64
		for i, s := range rankSimilar(edges, id) {
			if i == 0 {
				best = s.Score
			}
			// Sorted descending, so the first neighbour that is not in the same league as
			// this recipe's closest one ends the list. A dish with no real relative draws
			// one line, not `top` meaningless ones.
			if top > 0 && i >= top || s.Score < edgeFloor*best {
				break
			}
			pair := [2]int{a, s.ID}
			if pair[0] > pair[1] {
				pair[0], pair[1] = pair[1], pair[0]
			}
			if seen[pair] {
				continue
			}
			seen[pair] = true
			out = append(out, RecipeLink{A: pair[0], B: pair[1], Score: s.Score, Shared: sharedItems(uses[id], uses[strconv.Itoa(s.ID)], w)})
		}
	}
	return out
}

// sharedItems names what two recipes have in common, rarest first and staples left out — "egg,
// salt, sugar" is what made the map read as nonsense even where the edge itself was sound. The
// closest food always survives the trim, so a link is never listed as sharing nothing when it
// does; an edge that really is held up by cuisine alone returns empty, and the map says so.
func sharedItems(a, b map[string]bool, w map[string]float64) []string {
	out := []string{}
	for k := range a {
		if b[k] && w[k] > 0 {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if w[out[i]] != w[out[j]] {
			return w[out[i]] > w[out[j]]
		}
		return out[i] < out[j]
	})
	for len(out) > 1 && (len(out) > maxSharedItems || w[out[len(out)-1]] < edgeFloor*w[out[0]]) {
		out = out[:len(out)-1]
	}
	return out
}

// RecipeOverlap answers "which of my recipes are made of the same things" for the map.
func RecipeOverlap(ctx context.Context, userID, top int) ([]RecipeLink, error) {
	edges, err := GraphEdges(ctx, userID, 0)
	if err != nil {
		return nil, err
	}
	return overlapLinks(edges, top), nil
}

// relatedIngredients: foods in the scope that share a class with canonical, plus every
// spelling that translates to it. Order is alphabetical.
func relatedIngredients(edges []GraphEdge, canonical string) []string {
	var classes []string
	for _, e := range edges {
		if e.Rel == "is_a" && e.Src == canonical {
			classes = append(classes, e.Dst)
		}
	}
	set := map[string]bool{}
	for _, e := range edges {
		switch {
		case e.Rel == "is_a" && e.Src != canonical && contains(classes, e.Dst):
			set[e.Src] = true
		case e.Rel == "translates" && e.Dst == canonical && e.Src != canonical:
			set[e.Src] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// walk returns the edges within depth hops of the start node, treating edges as undirected.
func walk(edges []GraphEdge, nodeType, node string, depth int) []GraphEdge {
	if depth < 1 {
		depth = 1
	}
	if depth > maxNeighbourDepth {
		depth = maxNeighbourDepth
	}
	key := func(t, n string) string { return t + "\x00" + n }
	seen := map[string]bool{key(nodeType, node): true}
	frontier := map[string]bool{key(nodeType, node): true}
	taken := map[int]bool{}
	var out []GraphEdge
	for d := 0; d < depth && len(frontier) > 0; d++ {
		next := map[string]bool{}
		for i, e := range edges {
			s, t := key(e.SrcType, e.Src), key(e.DstType, e.Dst)
			if taken[i] || (!frontier[s] && !frontier[t]) {
				continue
			}
			taken[i] = true
			out = append(out, e)
			for _, k := range []string{s, t} {
				if !seen[k] {
					seen[k] = true
					next[k] = true
				}
			}
		}
		frontier = next
	}
	return out
}

func recipeTitles(ctx context.Context, userID int, ids []int) (map[int]string, error) {
	titles := map[int]string{}
	if len(ids) == 0 {
		return titles, nil
	}
	rows, err := Pool.Query(ctx, `SELECT r.id, r.title FROM recipes r WHERE r.id = ANY($2) AND `+inScope("r", "$1"), userID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var t string
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		titles[id] = t
	}
	return titles, rows.Err()
}

// SimilarRecipes answers "recipes like this one" for a saved recipe, best first.
func SimilarRecipes(ctx context.Context, userID, recipeID, limit int) ([]SimilarRecipe, error) {
	edges, err := GraphEdges(ctx, userID, 0)
	if err != nil {
		return nil, err
	}
	out := rankSimilar(edges, strconv.Itoa(recipeID))
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	ids := make([]int, len(out))
	for i, r := range out {
		ids[i] = r.ID
	}
	titles, err := recipeTitles(ctx, userID, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Title = titles[out[i].ID]
	}
	return out, nil
}

// resolveCanonical folds any spelling, in any language, to its canonical via the global
// alias table, so "Zwiebel" and "onion" start from the same node.
func resolveCanonical(ctx context.Context, name string) (string, error) {
	n := model.NormalizeItem(name)
	aliases, err := AliasTable(ctx, []string{n})
	if err != nil {
		return "", err
	}
	if a, ok := aliases[n]; ok && a.Canonical != "" {
		return a.Canonical, nil
	}
	return n, nil
}

// SimilarIngredients returns the canonical the name resolves to and the related foods.
func SimilarIngredients(ctx context.Context, userID int, name string) (string, []string, error) {
	canonical, err := resolveCanonical(ctx, name)
	if err != nil {
		return "", nil, err
	}
	edges, err := GraphEdges(ctx, userID, 0)
	if err != nil {
		return "", nil, err
	}
	return canonical, relatedIngredients(edges, canonical), nil
}

// Neighbours returns the edges within depth hops (1–3) of a node. An ingredient name is
// resolved to its canonical first; other node types are taken as given (recipe ids, slugs).
func Neighbours(ctx context.Context, userID int, nodeType, node string, depth int) ([]GraphEdge, error) {
	if nodeType == "ingredient" {
		c, err := resolveCanonical(ctx, node)
		if err != nil {
			return nil, err
		}
		node = c
	}
	edges, err := GraphEdges(ctx, userID, 0)
	if err != nil {
		return nil, err
	}
	return walk(edges, nodeType, node, depth), nil
}
