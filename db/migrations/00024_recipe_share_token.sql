-- +goose Up
-- +goose StatementBegin
-- Read-only share links (BACKLOG 8.4). NULL = not shared, which is every existing row:
-- sharing is opt-in per recipe and revoking is `SET share_token = NULL`, so the old link
-- stops resolving and a new share issues a fresh, unrelated token.
ALTER TABLE recipes
    ADD COLUMN share_token TEXT UNIQUE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE recipes
    DROP COLUMN share_token;
-- +goose StatementEnd
