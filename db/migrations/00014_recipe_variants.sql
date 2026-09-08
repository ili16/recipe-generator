-- +goose Up
-- +goose StatementBegin
ALTER TABLE recipes
    ADD COLUMN variant_of_recipe_id INT REFERENCES recipes (id) ON DELETE SET NULL;

CREATE INDEX idx_recipes_variant_of ON recipes (variant_of_recipe_id)
    WHERE variant_of_recipe_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_recipes_variant_of;
ALTER TABLE recipes
    DROP COLUMN variant_of_recipe_id;
-- +goose StatementEnd
