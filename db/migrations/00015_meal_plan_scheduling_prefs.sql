-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN meal_plan_skip_days TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN meal_plan_batch_days INT NOT NULL DEFAULT 1 CHECK (meal_plan_batch_days IN (1, 2, 3));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
    DROP COLUMN meal_plan_skip_days,
    DROP COLUMN meal_plan_batch_days;
-- +goose StatementEnd
