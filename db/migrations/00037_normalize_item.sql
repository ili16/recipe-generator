-- +goose Up
-- +goose StatementBegin
-- BACKLOG 16.4. The preparation is not part of the ingredient's name. Recipes write the
-- cut into the item -- "onion, finely chopped", "garlic (minced)" -- and everything that
-- joins on that name (the pantry match, the grocery list, ingredient_grams, the cooked
-- deduction) was treating three onions as three foods. 16.2's backfill made it visible:
-- it resolved all three separately, at 150 g each.
--
-- This is the SQL copy of model.NormalizeItem. Change one, change both.
CREATE OR REPLACE FUNCTION normalize_item(item TEXT) RETURNS TEXT AS $$
    SELECT lower(btrim(split_part(split_part(item, ',', 1), '(', 1)));
$$ LANGUAGE SQL IMMUTABLE STRICT;
-- +goose StatementEnd

-- +goose StatementBegin
-- Names already stored normalised were normalised by the old rule, so anything holding a
-- cut has to be rewritten or it will never match again. Several rows can collapse onto one
-- name, and both tables have a unique key on it, so exactly one row per key may be
-- rewritten: the shortest name wins, which is the bare food when it is already there.
--
-- A pantry loser is left alone rather than merged -- adding two quantities the user never
-- asked to combine is worse than leaving a second line they can see and merge themselves.
UPDATE pantry_items p
SET name = normalize_item(p.name)
WHERE p.name <> normalize_item(p.name)
  AND NOT EXISTS (SELECT 1
                  FROM pantry_items q
                  WHERE COALESCE(q.household_id::text, 'u' || q.user_id)
                      = COALESCE(p.household_id::text, 'u' || p.user_id)
                    AND COALESCE(q.unit, '') = COALESCE(p.unit, '')
                    AND normalize_item(q.name) = normalize_item(p.name)
                    AND (length(q.name), q.name) < (length(p.name), p.name));

-- A gram loser is dropped, not kept: "onion, chopped" -> 150 g and "onion" -> 150 g are
-- the same claim about the same food, and the table is a cache the backfill refills.
DELETE FROM ingredient_grams a
WHERE EXISTS (SELECT 1
              FROM ingredient_grams b
              WHERE b.unit = a.unit
                AND normalize_item(b.name) = normalize_item(a.name)
                AND (length(b.name), b.name) < (length(a.name), a.name));

UPDATE ingredient_grams SET name = normalize_item(name) WHERE name <> normalize_item(name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The cuts are gone from the normalised names; only the function comes back out.
DROP FUNCTION IF EXISTS normalize_item(TEXT);
-- +goose StatementEnd
