-- +goose Up
-- +goose StatementBegin
-- Staples: the things a kitchen always has -- salt, pepper, water, oil. They are ordinary
-- pantry rows, so 7.3's pantry match counts them for free, which is the whole point: a
-- recipe you "almost have everything for" should not be dragged below the line by salt.
-- What changes is that they are never shopped for and never deducted when you cook.
--
-- A flag on the row, not a category: a staple still lives on a shelf, and the unique
-- index (name, unit) is its identity -- being a staple is not.
ALTER TABLE pantry_items
    ADD COLUMN staple BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose StatementBegin
-- A planned meal that has been eaten. Deliberately not recipes.last_cooked_at (00023),
-- which says "this recipe was cooked at some point" and drives the suggestion cooldown.
-- This says "the pot planned for Monday is gone", which is what the grocery list needs
-- in order to stop shopping for it. Both are stamped together; neither can answer the
-- other's question.
ALTER TABLE meal_plan_items
    ADD COLUMN cooked_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
-- The one answer to "how much does one of these weigh".
--
-- model.NormalizeUnit knows exactly two conversions (kg->g, l->ml), so "2 onions" and
-- "150 g onion" are different unit families and nothing can ever be compared, summed or
-- subtracted across them. Everything downstream needs this: the grocery list emits two
-- onion lines, and deducting a cooked recipe from the pantry is impossible outright.
--
-- grams is per ONE of (name, unit). unit = '' means grams per piece: ('onion','',150).
-- 'g' is never stored -- it is the identity and model.ToGrams short-circuits it; 'kg'
-- and 'l' are never stored either, because NormalizeUnit has already folded them before
-- the lookup happens.
--
-- name = '*' is the wildcard: a conversion that does not depend on the food, like a
-- teaspoon. An item-specific row always beats it, which is how ('flour','cup',120) can
-- disagree with ('*','cup',240) without either being wrong.
--
-- source records who decided, so a bad model guess can be found and a hand-seeded row is
-- never silently overwritten by one. BACKLOG 11.3 wants exactly this table keyed the
-- same way; it extends it with fdc_id rather than starting a second, divergent notion of
-- what a gram is.
CREATE TABLE ingredient_grams
(
    name       VARCHAR(200)  NOT NULL,
    unit       TEXT          NOT NULL,
    grams      NUMERIC(10, 3) NOT NULL CHECK (grams > 0),
    source     TEXT          NOT NULL DEFAULT 'llm',
    created_at TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    PRIMARY KEY (name, unit)
);

-- The unit conversions that are true regardless of the food. Volume measures are the
-- exception to that and are seeded at water's density: a cup of flour is not 240 g, which
-- is exactly what an item-specific row is for. Seeding them anyway beats leaving a
-- recipe written in cups unconvertible, and 'seed' marks them as the coarse answer.
INSERT INTO ingredient_grams (name, unit, grams, source)
VALUES ('*', 'ml', 1, 'seed'),
       ('*', 'tsp', 5, 'seed'),
       ('*', 'teaspoon', 5, 'seed'),
       ('*', 'tbsp', 15, 'seed'),
       ('*', 'tablespoon', 15, 'seed'),
       ('*', 'cup', 240, 'seed'),
       ('*', 'cups', 240, 'seed'),
       ('*', 'fl oz', 29.57, 'seed'),
       ('*', 'oz', 28.35, 'seed'),
       ('*', 'ounce', 28.35, 'seed'),
       ('*', 'lb', 453.6, 'seed'),
       ('*', 'pound', 453.6, 'seed'),
       ('*', 'pint', 473.2, 'seed'),
       ('*', 'quart', 946.4, 'seed'),
       ('*', 'mg', 0.001, 'seed'),
       ('*', 'dl', 100, 'seed'),
       ('*', 'cl', 10, 'seed'),
       ('*', 'el', 15, 'seed'),
       ('*', 'tl', 5, 'seed'),
       ('*', 'prise', 0.5, 'seed'),
       ('*', 'pinch', 0.5, 'seed');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE ingredient_grams;
ALTER TABLE meal_plan_items DROP COLUMN cooked_at;
ALTER TABLE pantry_items DROP COLUMN staple;
-- +goose StatementEnd
