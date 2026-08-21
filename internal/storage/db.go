package storage

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	Pool *pgxpool.Pool
)

func AddRecipe(ctx context.Context, userID int, name, content, category string) (model.Recipe, error) {
	const query = `
		INSERT INTO recipes (user_id, title, content, category)
		VALUES ($1, $2, $3, $4)
		RETURNING id, title, content, category`

	slog.Debug("db: insert recipe", "userID", userID, "title", name)
	t := time.Now()
	var recipe model.Recipe
	err := Pool.QueryRow(ctx, query, userID, name, content, category).
		Scan(&recipe.ID, &recipe.Recipename, &recipe.Recipe, &recipe.Category)
	if err != nil {
		slog.Error("Inserting recipe failed", "error", err)
		return model.Recipe{}, err
	}

	slog.Info("Added recipe to database", "userID", userID, "recipeID", recipe.ID)
	slog.Debug("db: insert recipe done", "recipeID", recipe.ID, "dur_ms", time.Since(t).Milliseconds())
	return recipe, nil
}

func UpdateRecipe(ctx context.Context, userID, recipeID int, name, content, category string) (model.Recipe, error) {
	const query = `
		UPDATE recipes
		SET title      = $1,
		    content    = $2,
		    category   = COALESCE(NULLIF($3, ''), category),
		    updated_at = NOW()
		WHERE id = $4 AND user_id = $5
		RETURNING id, title, content, category`

	slog.Debug("db: update recipe", "userID", userID, "recipeID", recipeID)
	t := time.Now()
	var recipe model.Recipe
	err := Pool.QueryRow(ctx, query, name, content, category, recipeID, userID).
		Scan(&recipe.ID, &recipe.Recipename, &recipe.Recipe, &recipe.Category)
	if err != nil {
		return model.Recipe{}, err
	}
	slog.Debug("db: update recipe done", "recipeID", recipe.ID, "dur_ms", time.Since(t).Milliseconds())
	return recipe, nil
}

func DeleteRecipe(ctx context.Context, userID, recipeID int) error {
	slog.Debug("db: delete recipe", "userID", userID, "recipeID", recipeID)
	t := time.Now()
	_, err := Pool.Exec(ctx, "DELETE FROM recipes WHERE user_id = $1 AND id = $2", userID, recipeID)
	if err == nil {
		slog.Debug("db: delete recipe done", "recipeID", recipeID, "dur_ms", time.Since(t).Milliseconds())
	}
	return err
}

func InitDBPool() {
	var err error
	Pool, err = pgxpool.New(context.Background(), os.Getenv("DB_URL"))
	if err != nil {
		slog.Error("Unable to initialize DB pool connection", "error", err)
		os.Exit(1)
	}
	slog.Info("DB pool initialised")
}

func GetRecipes(userid int) ([]model.Recipe, error) {
	slog.Debug("db: query recipes", "userID", userid)
	t := time.Now()
	rows, err := Pool.Query(context.Background(), "SELECT id, title, content, category FROM recipes WHERE user_id = $1 ORDER BY updated_at DESC", userid)
	if err != nil {
		slog.Error("Failed to query recipes", "error", err)
		return nil, err
	}
	defer rows.Close()

	var recipes []model.Recipe
	for rows.Next() {
		var recipe model.Recipe
		err := rows.Scan(&recipe.ID, &recipe.Recipename, &recipe.Recipe, &recipe.Category)
		if err != nil {
			slog.Error("Failed to scan recipe", "error", err)
			return nil, err
		}
		recipes = append(recipes, recipe)
	}
	slog.Debug("db: query recipes done", "userID", userid, "count", len(recipes), "dur_ms", time.Since(t).Milliseconds())
	return recipes, nil
}
