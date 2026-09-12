package storage

import (
	"context"
	"errors"
	"strings"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// ErrEmptyCollectionName is a 400, not a 500: the caller sent a blank name.
var ErrEmptyCollectionName = errors.New("collection name is empty")

// Every function here scopes by user_id, so another user's collection id is
// indistinguishable from a missing one (BACKLOG 8.1): they all return pgx.ErrNoRows,
// which the handlers turn into 404.

// ListCollections returns userID's collections with their member recipe ids. Only
// live recipes are listed — a soft-deleted one keeps its membership row (so restoring
// it puts it back in its collections) but is invisible until then.
func ListCollections(ctx context.Context, userID int) ([]model.Collection, error) {
	rows, err := Pool.Query(ctx, `
		SELECT c.id, c.name,
		       COALESCE(ARRAY_AGG(rc.recipe_id ORDER BY rc.added_at DESC)
		                FILTER (WHERE r.id IS NOT NULL), '{}')
		FROM collections c
		LEFT JOIN recipe_collections rc ON rc.collection_id = c.id
		LEFT JOIN recipes r ON r.id = rc.recipe_id AND r.deleted_at IS NULL
		WHERE c.user_id = $1
		GROUP BY c.id, c.name
		ORDER BY c.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	collections := []model.Collection{}
	for rows.Next() {
		var c model.Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.RecipeIDs); err != nil {
			return nil, err
		}
		collections = append(collections, c)
	}
	return collections, rows.Err()
}

// CreateCollection creates a named collection, or returns the existing one of that name
// — re-creating "Sunday" is the same collection, not a duplicate and not an error.
func CreateCollection(ctx context.Context, userID int, name string) (model.Collection, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.Collection{}, ErrEmptyCollectionName
	}
	var c model.Collection
	err := Pool.QueryRow(ctx, `
		INSERT INTO collections (user_id, name) VALUES ($1, $2)
		ON CONFLICT (user_id, name) DO UPDATE SET updated_at = NOW()
		RETURNING id, name`, userID, name).Scan(&c.ID, &c.Name)
	if err != nil {
		return model.Collection{}, err
	}
	c.RecipeIDs = []int{}
	return c, nil
}

// DeleteCollection drops the collection and its membership rows. The recipes are
// untouched — recipe_collections cascades, recipes does not.
func DeleteCollection(ctx context.Context, userID, collectionID int) error {
	tag, err := Pool.Exec(ctx, `DELETE FROM collections WHERE id = $1 AND user_id = $2`, collectionID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// SetCollectionMembership adds or removes one recipe. Both the collection and the recipe
// must belong to userID; either failing is pgx.ErrNoRows. Adding twice is a no-op.
func SetCollectionMembership(ctx context.Context, userID, collectionID, recipeID int, member bool) error {
	if !member {
		tag, err := Pool.Exec(ctx, `
			DELETE FROM recipe_collections
			WHERE collection_id = $1 AND recipe_id = $2
			  AND EXISTS (SELECT 1 FROM collections WHERE id = $1 AND user_id = $3)`,
			collectionID, recipeID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	}

	tag, err := Pool.Exec(ctx, `
		INSERT INTO recipe_collections (collection_id, recipe_id)
		SELECT $1, $2
		WHERE EXISTS (SELECT 1 FROM collections WHERE id = $1 AND user_id = $3)
		  AND EXISTS (SELECT 1 FROM recipes WHERE id = $2 AND user_id = $3 AND deleted_at IS NULL)
		ON CONFLICT DO NOTHING`,
		collectionID, recipeID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either it's already a member (fine) or one of the ids isn't the caller's.
		var ok bool
		if err := Pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM recipe_collections rc
			               JOIN collections c ON c.id = rc.collection_id
			               WHERE rc.collection_id = $1 AND rc.recipe_id = $2 AND c.user_id = $3)`,
			collectionID, recipeID, userID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return pgx.ErrNoRows
		}
	}
	return nil
}
