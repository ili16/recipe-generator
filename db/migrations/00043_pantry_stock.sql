-- +goose Up
-- +goose StatementBegin
-- BACKLOG 16.6. A staple is a standing fact, and 16.1 made it load-bearing: it counts for
-- the pantry match and it is dropped from the grocery list. So a staple that ran out three
-- weeks ago makes recipes look cookable and keeps salt off the shopping list, silently,
-- forever. Two columns are what it takes to ask.
--
-- running_low is a boolean and not a three-state enum on purpose: "out" and "low" produce
-- the same action -- buy it -- and the difference is not worth a control the user has to
-- think about. It is what BuildGroceryList reads to stop dropping a staple.
--
-- confirmed_at is stamped on upsert and on every tap. A staple untouched for 28 days is
-- what the Pantry screen's stock check asks about; it keeps counting for the recipe match
-- while it waits, because the card asks a question, it does not retract a fact the user
-- never withdrew.
ALTER TABLE pantry_items
    ADD COLUMN running_low  BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN confirmed_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE pantry_items DROP COLUMN running_low, DROP COLUMN confirmed_at;
-- +goose StatementEnd
