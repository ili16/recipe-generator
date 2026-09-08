-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN skill_level TEXT CHECK (skill_level IN ('beginner', 'intermediate', 'advanced')),
    ADD COLUMN dietary_prefs TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN disliked_ingredients TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN cooking_cadence TEXT CHECK (cooking_cadence IN ('daily', 'every_couple_days', 'meal_prep_batching'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
    DROP COLUMN skill_level,
    DROP COLUMN dietary_prefs,
    DROP COLUMN disliked_ingredients,
    DROP COLUMN cooking_cadence;
-- +goose StatementEnd
