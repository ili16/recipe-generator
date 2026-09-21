-- +goose Up
-- +goose StatementBegin
-- BACKLOG 18.1. The per-user graph is DERIVED, not stored: recipe_ingredients, recipe_tags and
-- ingredient_alias already hold nearly every edge, so a second copy would only drift. What was
-- missing is cuisine and technique, which the recipe-writing call now fills in the same
-- response (one call, not two).
ALTER TABLE recipes ADD COLUMN cuisine TEXT;
ALTER TABLE recipes ADD COLUMN techniques TEXT[] NOT NULL DEFAULT '{}';

-- One row per edge, already carrying the scope columns so a reader filters with inScope()
-- like every other table. Soft-deleted recipes contribute nothing; a hard delete cascades
-- through the tables underneath -- either way "delete the recipe, lose its edges" holds with
-- no cleanup code. is_a / translates are alias FACTS (global), included only for foods the
-- scope's own recipes use so one kitchen never sees another's vocabulary.
CREATE VIEW graph_edge AS
SELECT r.user_id, r.household_id, 'recipe' AS src_type, r.id::text AS src,
       'uses' AS rel, 'ingredient' AS dst_type, canonical_item(ri.item)::text AS dst,
       count(*)::int AS weight
FROM recipes r JOIN recipe_ingredients ri ON ri.recipe_id = r.id
WHERE r.deleted_at IS NULL
GROUP BY r.user_id, r.household_id, r.id, canonical_item(ri.item)
UNION ALL
SELECT r.user_id, r.household_id, 'recipe', r.id::text, 'belongs_to',
       CASE WHEN t.tag_group = 'meal_type' THEN 'meal' ELSE 'tag' END, t.slug, 1
FROM recipes r JOIN recipe_tags rt ON rt.recipe_id = r.id JOIN tags t ON t.id = rt.tag_id
WHERE r.deleted_at IS NULL
UNION ALL
SELECT r.user_id, r.household_id, 'recipe', r.id::text, 'belongs_to', 'cuisine', r.cuisine, 1
FROM recipes r WHERE r.deleted_at IS NULL AND r.cuisine IS NOT NULL
UNION ALL
SELECT r.user_id, r.household_id, 'recipe', r.id::text, 'cooked_with', 'technique', tq, 1
FROM recipes r, unnest(r.techniques) AS tq WHERE r.deleted_at IS NULL
UNION ALL
SELECT DISTINCT r.user_id, r.household_id, 'ingredient', a.canonical, 'is_a', 'class', a.class, 1
FROM recipes r JOIN recipe_ingredients ri ON ri.recipe_id = r.id
JOIN ingredient_alias a ON a.name = normalize_item(ri.item)
WHERE r.deleted_at IS NULL AND a.class IS NOT NULL
UNION ALL
SELECT DISTINCT r.user_id, r.household_id, 'ingredient', a.name, 'translates', 'ingredient', a.canonical, 1
FROM recipes r JOIN recipe_ingredients ri ON ri.recipe_id = r.id
JOIN ingredient_alias a ON a.name = normalize_item(ri.item)
WHERE r.deleted_at IS NULL AND a.name <> a.canonical;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW graph_edge;
ALTER TABLE recipes DROP COLUMN techniques;
ALTER TABLE recipes DROP COLUMN cuisine;
-- +goose StatementEnd
