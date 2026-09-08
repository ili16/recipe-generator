-- +goose Up
-- +goose StatementBegin
ALTER TABLE meal_plan_items
    ADD COLUMN start_time TIME NOT NULL DEFAULT '18:00:00';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE meal_plan_items
    DROP COLUMN start_time;
-- +goose StatementEnd
