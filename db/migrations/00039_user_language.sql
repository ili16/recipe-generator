-- +goose Up
-- +goose StatementBegin
-- The UI language lived only in the device's AsyncStorage, so a second device started in
-- English again (BACKLOG.md 14.1). NULL is not "English": it means the user never chose,
-- and the client follows the device locale.
ALTER TABLE users ADD COLUMN language TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN language;
-- +goose StatementEnd
