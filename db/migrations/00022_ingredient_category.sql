-- +goose Up
-- +goose StatementBegin
-- Aisle category, emitted by the generation call alongside the rest of the document
-- (BACKLOG 6.1). Nullable: pre-6.1 rows have none and a missing one groups as "other"
-- rather than blocking the grocery list. Not a CHECK constraint — the JSON schema is
-- the enum, and a model that invents a sixth value should land in "other", not 500.
ALTER TABLE recipe_ingredients
    ADD COLUMN category TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE recipe_ingredients
    DROP COLUMN category;
-- +goose StatementEnd
