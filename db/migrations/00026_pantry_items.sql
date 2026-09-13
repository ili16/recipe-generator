-- +goose Up
-- +goose StatementBegin
-- What the user has in the house (BACKLOG 7.1). name is stored already normalised
-- (lowercased, trimmed) and unit already canonicalised to its summing family (kg→g,
-- l→ml) by model.NormalizeItem/NormalizeUnit — the same normalisation the grocery list
-- consolidates on, so 7.3's pantry match is a plain join rather than a second,
-- divergent notion of what "flour" is.
--
-- One row per (user, name, unit family): re-adding an item you already have updates the
-- quantity rather than growing a second line. Category is TEXT, not an enum — same
-- reasoning as recipe_ingredients.category: an unknown value should render as "other",
-- not 500.
CREATE TABLE pantry_items (
    id         SERIAL PRIMARY KEY,
    user_id    INT          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       VARCHAR(200) NOT NULL,
    quantity   NUMERIC(10, 3),
    unit       TEXT,
    category   TEXT         NOT NULL DEFAULT 'spices_dry',
    expires_on DATE,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- COALESCE, not the bare column: a NULL unit ("2 eggs") must still collide with itself,
-- and NULLs are distinct in a plain unique index.
CREATE UNIQUE INDEX idx_pantry_items_user_name_unit
    ON pantry_items (user_id, name, COALESCE(unit, ''));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE pantry_items;
-- +goose StatementEnd
