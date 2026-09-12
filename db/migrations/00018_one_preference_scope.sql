-- +goose Up
-- +goose StatementBegin
-- BACKLOG 4.4: preferences live in Profile only. The per-week override was invisible in
-- the UI, had no way to be cleared, and silently re-keyed itself when week_start_day
-- changed. cooking_cadence never reached any prompt and lost its client half in 4.1.
ALTER TABLE meal_plans
    DROP COLUMN no_food_days_override,
    DROP COLUMN no_cook_days_override,
    DROP COLUMN batch_days_override;

ALTER TABLE users
    DROP COLUMN cooking_cadence;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN cooking_cadence TEXT CHECK (cooking_cadence IN ('daily', 'every_couple_days', 'meal_prep_batching'));

ALTER TABLE meal_plans
    ADD COLUMN no_food_days_override TEXT[],
    ADD COLUMN no_cook_days_override TEXT[],
    ADD COLUMN batch_days_override INT CHECK (batch_days_override IS NULL OR batch_days_override IN (1, 2, 3));
-- +goose StatementEnd
