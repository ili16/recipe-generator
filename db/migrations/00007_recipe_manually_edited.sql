-- +goose Up
-- +goose StatementBegin
ALTER TABLE recipes
    ADD COLUMN manually_edited BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE recipes
    DROP COLUMN manually_edited;
-- +goose StatementEnd
