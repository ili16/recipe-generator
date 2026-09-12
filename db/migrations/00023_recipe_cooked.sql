-- +goose Up
-- +goose StatementBegin
-- "Mark as cooked" (BACKLOG 6.3): the last time the owner actually cooked this recipe.
-- A single timestamp, not a cook-history table — the only consumer is the suggestion
-- cooldown, which asks "how long ago", never "how many times". A history table is what
-- 6.7 (per-cook photos and ratings) would need; it can own its own rows then.
ALTER TABLE recipes
    ADD COLUMN last_cooked_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE recipes
    DROP COLUMN last_cooked_at;
-- +goose StatementEnd
