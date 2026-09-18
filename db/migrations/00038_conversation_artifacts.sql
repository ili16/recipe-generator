-- +goose Up
-- +goose StatementBegin
-- The cards a turn produced (a recipe document, a week plan) were streamed to the client
-- and then forgotten: reopening a thread replayed its prose with the recipe missing
-- (BACKLOG.md 10.4). The artifact is stored on the tool message that produced it -- one
-- column, not a table: it has no life of its own and dies with the message.
ALTER TABLE conversation_messages ADD COLUMN artifact JSONB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE conversation_messages DROP COLUMN artifact;
-- +goose StatementEnd
