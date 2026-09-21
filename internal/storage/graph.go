package storage

import (
	"context"
	"strconv"
)

// GraphEdge is one edge of the recipe graph (BACKLOG 18.1): typed nodes, a relation, a weight.
type GraphEdge struct {
	SrcType string `json:"src_type"`
	Src     string `json:"src"`
	Rel     string `json:"rel"`
	DstType string `json:"dst_type"`
	Dst     string `json:"dst"`
	Weight  int    `json:"weight"`
}

// GraphEdges reads the graph_edge view (migration 00049) through the same scope as every
// other table, so a household member sees the household's graph. recipeID > 0 narrows it
// to that recipe's own edges; 0 returns the whole scope.
func GraphEdges(ctx context.Context, userID, recipeID int) ([]GraphEdge, error) {
	q := `SELECT e.src_type, e.src, e.rel, e.dst_type, e.dst, e.weight FROM graph_edge e WHERE ` +
		inScope("e", "$1")
	args := []any{userID}
	if recipeID > 0 {
		q += ` AND e.src_type = 'recipe' AND e.src = $2`
		args = append(args, strconv.Itoa(recipeID))
	}
	rows, err := Pool.Query(ctx, q+` ORDER BY e.src_type, e.src, e.rel, e.dst`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GraphEdge
	for rows.Next() {
		var e GraphEdge
		if err := rows.Scan(&e.SrcType, &e.Src, &e.Rel, &e.DstType, &e.Dst, &e.Weight); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
