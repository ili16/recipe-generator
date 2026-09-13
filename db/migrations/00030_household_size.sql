-- +goose Up
-- +goose StatementBegin
-- BACKLOG.md 9.12. servings on a planned day was being asked to mean two things at once:
-- how many people eat here (a property of the household, stable for months) and how many
-- portions this pot makes (a property of this recipe on this day, often deliberately
-- larger -- which is what creates a leftover). Only the second existed, so the app could
-- not notice a recipe yielding 2 planned for a household of 5.
-- NULL means "never said", which is not the same as 1: no household size, no mismatch
-- warning, because there is nothing to compare against.
ALTER TABLE users ADD COLUMN household_size INT CHECK (household_size BETWEEN 1 AND 20);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN household_size;
-- +goose StatementEnd
