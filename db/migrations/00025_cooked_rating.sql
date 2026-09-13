-- +goose Up
-- +goose StatementBegin
-- Post-cook rating (BACKLOG 6.7): how the last cook actually went, 1–5. One column on
-- the recipe, same shape as 00023's last_cooked_at — the consumer is the planner prompt,
-- which asks "is this worth repeating", never "how did each individual cook go". A
-- per-cook history table is what photos would need; 6.11 can own its own rows then.
ALTER TABLE recipes
    ADD COLUMN cooked_rating SMALLINT CHECK (cooked_rating BETWEEN 1 AND 5);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE recipes
    DROP COLUMN cooked_rating;
-- +goose StatementEnd
