-- +goose Up
-- +goose StatementBegin
-- BACKLOG 13.2. A week gets the same read-only link a recipe has had since 8.4: one
-- nullable token on the row that already identifies the week, no table and no second
-- notion of what a share is. NULL means not shared, revoking sets it back to NULL, and
-- re-sharing is idempotent so a link already sent keeps working.
ALTER TABLE meal_plans ADD COLUMN share_token TEXT UNIQUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE meal_plans DROP COLUMN share_token;
-- +goose StatementEnd
