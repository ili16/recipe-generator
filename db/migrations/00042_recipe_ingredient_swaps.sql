-- +goose Up
-- +goose StatementBegin
-- BACKLOG 17.4c. A swap is the cook's, not the recipe's: it belongs to one user and one
-- recipe, and it never touches recipes.content or recipe_ingredients. That is what keeps
-- a household member's "I use linguine" out of the owner's recipe, and leaves 15.8's
-- copy-on-edit question untouched.
--
-- sort_order is recipe_ingredients.sort_order — the line, not the food, so swapping one
-- of two lines that both say "flour" swaps only the one the cook pointed at. There is no
-- FK to recipe_ingredients (it has no unique key on (recipe_id, sort_order) to point at);
-- a swap whose line has since been edited away simply matches nothing on read, which is
-- the right failure: the recipe wins, the stale swap disappears.
--
-- replacement is stored as written by the cook. It is normalised at every join the same
-- way an ingredient is, by canonical_item().
CREATE TABLE recipe_ingredient_swaps
(
    user_id     INTEGER      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    recipe_id   INTEGER      NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    sort_order  INTEGER      NOT NULL,
    replacement VARCHAR(200) NOT NULL CHECK (btrim(replacement) <> ''),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, recipe_id, sort_order)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS recipe_ingredient_swaps;
-- +goose StatementEnd
