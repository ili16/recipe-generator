package storage

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	Pool *pgxpool.Pool
)

// queryExecer is satisfied by both *pgxpool.Pool and pgx.Tx, so tag inserts work the same
// way whether or not they're part of a larger transaction.
type queryExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// insertRecipeTags links recipeID to every tag whose slug is in tags. Unknown slugs are
// silently skipped (the SELECT simply won't match them).
func insertRecipeTags(ctx context.Context, exec queryExecer, recipeID int, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	_, err := exec.Exec(ctx, `
		INSERT INTO recipe_tags (recipe_id, tag_id)
		SELECT $1, id FROM tags WHERE slug = ANY($2)`,
		recipeID, tags,
	)
	return err
}

// getRecipeTags returns the tag slugs attached to a recipe, alphabetically.
func getRecipeTags(ctx context.Context, recipeID int) ([]string, error) {
	rows, err := Pool.Query(ctx, `
		SELECT t.slug FROM recipe_tags rt JOIN tags t ON t.id = rt.tag_id
		WHERE rt.recipe_id = $1 ORDER BY t.slug`, recipeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		tags = append(tags, slug)
	}
	return tags, rows.Err()
}

// insertIngredientsAndSteps inserts doc's ingredients and steps (plus the step_ingredients
// junction rows) for recipeID, within tx. Assumes recipe_ingredients/recipe_steps for
// recipeID are already empty (a fresh insert or a caller that deleted the old rows first).
func insertIngredientsAndSteps(ctx context.Context, tx pgx.Tx, recipeID int, doc model.RecipeDocument) error {
	// Insert ingredients and collect DB IDs keyed by 0-based array index.
	ingIDs := make([]int64, len(doc.Ingredients))
	for i, ing := range doc.Ingredients {
		var ingID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO recipe_ingredients
				(recipe_id, sort_order, section, item, quantity, quantity_text, unit, optional)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			RETURNING id`,
			recipeID, i+1, ing.Section, ing.Item, ing.Quantity, ing.QuantityText, ing.Unit, ing.Optional,
		).Scan(&ingID)
		if err != nil {
			return err
		}
		ingIDs[i] = ingID
	}

	for _, step := range doc.Steps {
		var stepID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO recipe_steps (recipe_id, sort_order, step_text, timer_seconds, temperature_c)
			VALUES ($1,$2,$3,$4,$5)
			RETURNING id`,
			recipeID, step.SortOrder, step.StepText, step.TimerSeconds, step.TemperatureC,
		).Scan(&stepID)
		if err != nil {
			return err
		}
		for _, idx := range step.IngredientIndices {
			if idx >= 0 && idx < len(ingIDs) {
				if _, err = tx.Exec(ctx, `INSERT INTO step_ingredients (step_id, ingredient_id) VALUES ($1,$2)`, stepID, ingIDs[idx]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// AddRecipeStructured persists a RecipeDocument in a single transaction: recipes header
// + recipe_ingredients + recipe_steps, plus the full edit trail.
//
// origin, if non-nil, is the user's original generation ask; it's recorded as an
// immutable sources row. history is the ordered list of accepted edits, with history[0]
// representing the initial generation (ChangePrompt == "") and each subsequent entry one
// chat-driven edit — each becomes a recipe_versions snapshot (change_kind 'extraction'
// for the first, 'ai_edit' for the rest, change_note holding the instruction) plus a
// matching recipe_timeline_events row, so "was this recipe saved unedited?" is just
// current_version == 1. The saved document is history's last entry; an empty history is
// treated as a single unedited entry seeded with doc.
func AddRecipeStructured(ctx context.Context, userID int, doc model.RecipeDocument, content string, origin *model.GenerationOrigin, history []model.EditTurn, aiModel, promptVersion string, variantOfRecipeID *int) (model.Recipe, error) {
	if len(history) == 0 {
		history = []model.EditTurn{{Structured: doc}}
	}

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return model.Recipe{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	sourceType := "text"
	if origin != nil && origin.SourceType != "" {
		sourceType = origin.SourceType
	}

	var recipeID int
	err = tx.QueryRow(ctx, `
		INSERT INTO recipes
			(user_id, title, summary, language, servings, total_minutes, prep_minutes, cook_minutes,
			 difficulty, source_type, content, ai_model, ai_prompt_version, current_version,
			 variant_of_recipe_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id`,
		userID, doc.Title, doc.Summary, doc.Language,
		doc.Servings, doc.TotalMinutes, doc.PrepMinutes, doc.CookMinutes, doc.Difficulty, sourceType, content,
		aiModel, promptVersion, len(history), variantOfRecipeID,
	).Scan(&recipeID)
	if err != nil {
		return model.Recipe{}, err
	}

	if err = insertRecipeTags(ctx, tx, recipeID, doc.Tags); err != nil {
		return model.Recipe{}, err
	}

	if err = insertIngredientsAndSteps(ctx, tx, recipeID, doc); err != nil {
		return model.Recipe{}, err
	}

	for i, turn := range history {
		version := i + 1
		changeKind, eventType := "ai_edit", "ai_edit_accepted"
		if i == 0 {
			changeKind, eventType = "extraction", "extracted"
		}
		snapshot, marshalErr := json.Marshal(turn.Structured)
		if marshalErr != nil {
			return model.Recipe{}, marshalErr
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO recipe_versions (recipe_id, version, data, change_kind, change_note, created_by)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)`,
			recipeID, version, snapshot, changeKind, turn.ChangePrompt, userID,
		)
		if err != nil {
			return model.Recipe{}, err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO recipe_timeline_events (recipe_id, user_id, event_type, version)
			VALUES ($1, $2, $3, $4)`,
			recipeID, userID, eventType, version,
		)
		if err != nil {
			return model.Recipe{}, err
		}
	}

	if origin != nil {
		_, err = tx.Exec(ctx, `
			INSERT INTO sources (recipe_id, user_id, source_type, source_url, raw_text)
			VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))`,
			recipeID, userID, sourceType, origin.SourceURL, origin.Prompt,
		)
		if err != nil {
			return model.Recipe{}, err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return model.Recipe{}, err
	}

	slog.Info("added structured recipe", "userID", userID, "recipeID", recipeID, "title", doc.Title)
	return model.Recipe{ID: recipeID, Recipename: doc.Title, Recipe: content, Tags: doc.Tags}, nil
}

// UpdateRecipeStructured overwrites an existing recipe's header, ingredients, steps, and
// tags with doc, replacing the ingredient/step/tag rows wholesale (delete-then-reinsert,
// same shape as AddRecipeStructured's insert). manuallyEdited is the caller's intent: true
// for a hand-edited save, false when persisting an AI refine result, which clears any
// prior manual-edit flag since the AI content now supersedes it. Every call also appends
// one recipe_versions snapshot (change_kind "manual" or "ai_edit") plus a matching
// recipe_timeline_events row, bumping current_version, so the edit is visible in the
// recipe's history. changeNote is the AI instruction behind an ai_edit; pass "" for a
// manual edit with no note.
func UpdateRecipeStructured(ctx context.Context, userID, recipeID int, doc model.RecipeDocument, content string, manuallyEdited bool, changeNote string) (model.Recipe, error) {
	slog.Debug("db: update recipe", "userID", userID, "recipeID", recipeID)
	t := time.Now()

	tx, err := Pool.Begin(ctx)
	if err != nil {
		return model.Recipe{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var id, version int
	err = tx.QueryRow(ctx, `
		UPDATE recipes
		SET title = $1, summary = $2, language = $3, servings = $4, total_minutes = $5,
		    prep_minutes = $6, cook_minutes = $7, difficulty = $8, content = $9, manually_edited = $10,
		    current_version = current_version + 1, updated_at = NOW()
		WHERE id = $11 AND user_id = $12 AND deleted_at IS NULL
		RETURNING id, current_version`,
		doc.Title, doc.Summary, doc.Language, doc.Servings, doc.TotalMinutes, doc.PrepMinutes,
		doc.CookMinutes, doc.Difficulty, content, manuallyEdited, recipeID, userID,
	).Scan(&id, &version)
	if err != nil {
		return model.Recipe{}, err
	}

	if _, err = tx.Exec(ctx, `DELETE FROM recipe_ingredients WHERE recipe_id = $1`, recipeID); err != nil {
		return model.Recipe{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM recipe_steps WHERE recipe_id = $1`, recipeID); err != nil {
		return model.Recipe{}, err
	}
	if err = insertIngredientsAndSteps(ctx, tx, recipeID, doc); err != nil {
		return model.Recipe{}, err
	}

	if _, err = tx.Exec(ctx, `DELETE FROM recipe_tags WHERE recipe_id = $1`, recipeID); err != nil {
		return model.Recipe{}, err
	}
	if err = insertRecipeTags(ctx, tx, recipeID, doc.Tags); err != nil {
		return model.Recipe{}, err
	}

	changeKind, eventType := "manual", "edited"
	if !manuallyEdited {
		changeKind, eventType = "ai_edit", "ai_edit_accepted"
	}
	snapshot, err := json.Marshal(doc)
	if err != nil {
		return model.Recipe{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO recipe_versions (recipe_id, version, data, change_kind, change_note, created_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)`,
		recipeID, version, snapshot, changeKind, changeNote, userID,
	); err != nil {
		return model.Recipe{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO recipe_timeline_events (recipe_id, user_id, event_type, version)
		VALUES ($1, $2, $3, $4)`,
		recipeID, userID, eventType, version,
	); err != nil {
		return model.Recipe{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return model.Recipe{}, err
	}

	slog.Debug("db: update recipe done", "recipeID", recipeID, "dur_ms", time.Since(t).Milliseconds())
	return model.Recipe{
		ID: recipeID, Recipename: doc.Title, Recipe: content, Tags: doc.Tags,
		Structured: &doc, ManuallyEdited: manuallyEdited,
	}, nil
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
	config, err := pgxpool.ParseConfig(os.Getenv("DB_URL"))
	if err != nil {
		slog.Error("Unable to parse DB_URL", "error", err)
		os.Exit(1)
	}
	config.ConnConfig.Tracer = otelpgx.NewTracer()

	Pool, err = pgxpool.NewWithConfig(context.Background(), config)
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
		SELECT r.id, r.title, r.content, r.manually_edited,
		       r.summary, r.language, r.servings, r.total_minutes, r.prep_minutes, r.cook_minutes, r.difficulty,
		       rv.vote
		FROM recipes r
		LEFT JOIN recipe_votes rv ON rv.recipe_id = r.id AND rv.user_id = $2
		WHERE r.id = $1 AND r.user_id = $2 AND r.deleted_at IS NULL`,
		recipeID, userID,
	).Scan(
		&recipe.ID, &recipe.Recipename, &recipe.Recipe, &recipe.ManuallyEdited,
		&doc.Summary, &doc.Language, &doc.Servings, &doc.TotalMinutes, &doc.PrepMinutes, &doc.CookMinutes, &doc.Difficulty,
		&recipe.MyVote,
	)
	if err != nil {
		return model.Recipe{}, err
	}
	doc.Title = recipe.Recipename

	recipe.Tags, err = getRecipeTags(ctx, recipeID)
	if err != nil {
		return model.Recipe{}, err
	}
	doc.Tags = recipe.Tags

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

// GetRecipeVersions returns a recipe's edit trail (recipe_versions), newest first. Returns
// pgx.ErrNoRows if recipeID doesn't exist or isn't owned by userID; an owned recipe with no
// structured saves yet (markdown-only AddRecipe path) returns an empty, non-nil slice.
func GetRecipeVersions(ctx context.Context, userID, recipeID int) ([]model.RecipeVersion, error) {
	var exists bool
	if err := Pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM recipes WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL)`,
		recipeID, userID,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, pgx.ErrNoRows
	}

	rows, err := Pool.Query(ctx, `
		SELECT version, change_kind, COALESCE(change_note, ''), created_at, data
		FROM recipe_versions
		WHERE recipe_id = $1
		ORDER BY version DESC`, recipeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	versions := []model.RecipeVersion{}
	for rows.Next() {
		var v model.RecipeVersion
		var raw []byte
		if err := rows.Scan(&v.Version, &v.ChangeKind, &v.ChangeNote, &v.CreatedAt, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &v.Data); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func GetRecipes(userid int) ([]model.Recipe, error) {
	slog.Debug("db: query recipes", "userID", userid)
	t := time.Now()
	rows, err := Pool.Query(context.Background(), `
		SELECT r.id, r.title, r.content, r.manually_edited, rv.vote, r.variant_of_recipe_id,
		       r.source_type, (SELECT s.source_url FROM sources s WHERE s.recipe_id = r.id ORDER BY s.id LIMIT 1),
		       r.created_at
		FROM recipes r
		LEFT JOIN recipe_votes rv ON rv.recipe_id = r.id AND rv.user_id = $1
		WHERE r.user_id = $1 ORDER BY r.updated_at DESC`, userid)
	if err != nil {
		slog.Error("Failed to query recipes", "error", err)
		return nil, err
	}

	var recipes []model.Recipe
	for rows.Next() {
		var recipe model.Recipe
		err := rows.Scan(&recipe.ID, &recipe.Recipename, &recipe.Recipe, &recipe.ManuallyEdited, &recipe.MyVote, &recipe.VariantOfRecipeID,
			&recipe.SourceType, &recipe.SourceURL, &recipe.CreatedAt)
		if err != nil {
			rows.Close()
			slog.Error("Failed to scan recipe", "error", err)
			return nil, err
		}
		recipes = append(recipes, recipe)
	}
	rows.Close()

	tagRows, err := Pool.Query(context.Background(), `
		SELECT rt.recipe_id, t.slug
		FROM recipe_tags rt
		JOIN tags t ON t.id = rt.tag_id
		WHERE rt.recipe_id = ANY(SELECT id FROM recipes WHERE user_id = $1)`, userid)
	if err != nil {
		slog.Error("Failed to query recipe tags", "error", err)
		return nil, err
	}
	defer tagRows.Close()
	tagsByRecipe := make(map[int][]string)
	for tagRows.Next() {
		var recipeID int
		var slug string
		if err := tagRows.Scan(&recipeID, &slug); err != nil {
			slog.Error("Failed to scan recipe tag", "error", err)
			return nil, err
		}
		tagsByRecipe[recipeID] = append(tagsByRecipe[recipeID], slug)
	}
	for i := range recipes {
		recipes[i].Tags = tagsByRecipe[recipes[i].ID]
	}

	slog.Debug("db: query recipes done", "userID", userid, "count", len(recipes), "dur_ms", time.Since(t).Milliseconds())
	return recipes, nil
}
