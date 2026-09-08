-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    RENAME COLUMN meal_plan_skip_days TO meal_plan_no_food_days;

ALTER TABLE users
    ADD COLUMN meal_plan_no_cook_days TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN week_start_day TEXT NOT NULL DEFAULT 'monday'
        CHECK (week_start_day IN ('monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday'));

ALTER TABLE meal_plans
    ADD COLUMN no_food_days_override TEXT[],
    ADD COLUMN no_cook_days_override TEXT[],
    ADD COLUMN batch_days_override INT CHECK (batch_days_override IS NULL OR batch_days_override IN (1, 2, 3));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE meal_plans
    DROP COLUMN no_food_days_override,
    DROP COLUMN no_cook_days_override,
    DROP COLUMN batch_days_override;

ALTER TABLE users
    DROP COLUMN meal_plan_no_cook_days,
    DROP COLUMN week_start_day;

ALTER TABLE users
    RENAME COLUMN meal_plan_no_food_days TO meal_plan_skip_days;
-- +goose StatementEnd
