package storage

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	Pool *pgxpool.Pool
)

// AddRecipeStructured persists a RecipeDocument in a single transaction:
// recipes header + recipe_ingredients + recipe_steps + recipe_versions snapshot.
func AddRecipeStructured(ctx context.Context, userID int, doc model.RecipeDocument, content string) (model.Recipe, error) {
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return model.Recipe{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var recipeID int
	err = tx.QueryRow(ctx, `
		INSERT INTO recipes
			(user_id, title, summary, category, language, servings, prep_minutes, cook_minutes,
			 difficulty, source_type, content, ai_model, ai_prompt_version, current_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'text',$10,'gpt-4o-mini','v2',1)
		RETURNING id`,
		userID, doc.Title, doc.Summary, doc.Category, doc.Language,
		doc.Servings, doc.PrepMinutes, doc.CookMinutes, doc.Difficulty, content,
	).Scan(&recipeID)
	if err != nil {
		return model.Recipe{}, err
	}

	// Insert ingredients and collect DB IDs keyed by 0-based array index.
	ingIDs := make([]int64, len(doc.Ingredients))
	for i, ing := range doc.Ingredients {
		var ingID int64
		err = tx.QueryRow(ctx, `
			INSERT INTO recipe_ingredients
				(recipe_id, sort_order, section, item, quantity, quantity_text, unit, optional)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			RETURNING id`,
			recipeID, i+1, ing.Section, ing.Item, ing.Quantity, ing.QuantityText, ing.Unit, ing.Optional,
		).Scan(&ingID)
		if err != nil {
			return model.Recipe{}, err
		}
		ingIDs[i] = ingID
	}

	for _, step := range doc.Steps {
		var stepID int64
		err = tx.QueryRow(ctx, `
			INSERT INTO recipe_steps (recipe_id, sort_order, step_text, timer_seconds, temperature_c)
			VALUES ($1,$2,$3,$4,$5)
			RETURNING id`,
			recipeID, step.SortOrder, step.StepText, step.TimerSeconds, step.TemperatureC,
		).Scan(&stepID)
		if err != nil {
			return model.Recipe{}, err
		}
		for _, idx := range step.IngredientIndices {
			if idx >= 0 && idx < len(ingIDs) {
				_, err = tx.Exec(ctx, `INSERT INTO step_ingredients (step_id, ingredient_id) VALUES ($1,$2)`, stepID, ingIDs[idx])
				if err != nil {
					return model.Recipe{}, err
				}
			}
		}
	}

	snapshot, _ := json.Marshal(doc)
	_, err = tx.Exec(ctx, `
		INSERT INTO recipe_versions (recipe_id, version, data, change_kind, created_by)
		VALUES ($1, 1, $2, 'extraction', $3)`,
		recipeID, snapshot, userID,
	)
	if err != nil {
		return model.Recipe{}, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO recipe_timeline_events (recipe_id, user_id, event_type, version)
		VALUES ($1, $2, 'extracted', 1)`,
		recipeID, userID,
	)
	if err != nil {
		return model.Recipe{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return model.Recipe{}, err
	}

	slog.Info("added structured recipe", "userID", userID, "recipeID", recipeID, "title", doc.Title)
	return model.Recipe{ID: recipeID, Recipename: doc.Title, Recipe: content, Category: doc.Category}, nil
}

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

func GetRecipeByID(ctx context.Context, userID, recipeID int) (model.Recipe, error) {
	var recipe model.Recipe
	var doc model.RecipeDocument

	err := Pool.QueryRow(ctx, `
		SELECT r.id, r.title, r.content, r.category,
		       r.summary, r.language, r.servings, r.prep_minutes, r.cook_minutes, r.difficulty
		FROM recipes r
		WHERE r.id = $1 AND r.user_id = $2 AND r.deleted_at IS NULL`,
		recipeID, userID,
	).Scan(
		&recipe.ID, &recipe.Recipename, &recipe.Recipe, &recipe.Category,
		&doc.Summary, &doc.Language, &doc.Servings, &doc.PrepMinutes, &doc.CookMinutes, &doc.Difficulty,
	)
	if err != nil {
		return model.Recipe{}, err
	}
	doc.Title = recipe.Recipename
	doc.Category = recipe.Category

	ingRows, err := Pool.Query(ctx, `
		SELECT section, item, quantity, quantity_text, unit, optional
		FROM recipe_ingredients WHERE recipe_id = $1 ORDER BY sort_order`, recipeID)
	if err == nil {
		defer ingRows.Close()
		for ingRows.Next() {
			var ing model.RecipeIngredient
			if scanErr := ingRows.Scan(&ing.Section, &ing.Item, &ing.Quantity, &ing.QuantityText, &ing.Unit, &ing.Optional); scanErr == nil {
				doc.Ingredients = append(doc.Ingredients, ing)
			}
		}
	}

	stepRows, err := Pool.Query(ctx, `
		SELECT sort_order, step_text, timer_seconds, temperature_c
		FROM recipe_steps WHERE recipe_id = $1 ORDER BY sort_order`, recipeID)
	if err == nil {
		defer stepRows.Close()
		for stepRows.Next() {
			var step model.RecipeStep
			if scanErr := stepRows.Scan(&step.SortOrder, &step.StepText, &step.TimerSeconds, &step.TemperatureC); scanErr == nil {
				doc.Steps = append(doc.Steps, step)
			}
		}
	}

	// Populate IngredientIndices from the step_ingredients junction table.
	// ingredient sort_order is 1-based so subtract 1 to get the 0-based array index.
	siRows, err := Pool.Query(ctx, `
		SELECT rs.sort_order, ri.sort_order - 1
		FROM step_ingredients si
		JOIN recipe_steps rs ON si.step_id = rs.id
		JOIN recipe_ingredients ri ON si.ingredient_id = ri.id
		WHERE rs.recipe_id = $1
		ORDER BY rs.sort_order, ri.sort_order`, recipeID)
	if err == nil {
		defer siRows.Close()
		indexMap := make(map[int][]int)
		for siRows.Next() {
			var stepSort, ingIdx int
			if scanErr := siRows.Scan(&stepSort, &ingIdx); scanErr == nil {
				indexMap[stepSort] = append(indexMap[stepSort], ingIdx)
			}
		}
		for i := range doc.Steps {
			if indices, ok := indexMap[doc.Steps[i].SortOrder]; ok {
				doc.Steps[i].IngredientIndices = indices
			}
		}
	}

	if len(doc.Ingredients) > 0 || len(doc.Steps) > 0 {
		recipe.Structured = &doc
	}
	return recipe, nil
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
