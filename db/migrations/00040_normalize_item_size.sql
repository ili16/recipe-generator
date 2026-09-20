-- +goose Up
-- +goose StatementBegin
-- BACKLOG 17.1. The size is not part of the ingredient's name. "medium onion", "large egg"
-- and "große Zwiebel" were each their own food to everything that joins on the normalised
-- name -- the pantry match, the grocery list, ingredient_grams, the cooked deduction --
-- so one onion was weighed three times and a pantry holding "onion" covered none of them.
--
-- What the size actually is, is a weight: model.ToGrams multiplies the per-piece row by
-- it. That arithmetic lives in Go; this function only has to agree on the key.
--
-- This is the SQL copy of model.NormalizeItem / model.SplitSize. The word list is
-- model.sizeFactors and TestMigrationSizeWordsMatchGo fails if the two drift apart.
-- Change one, change both.
CREATE OR REPLACE FUNCTION normalize_item(item TEXT) RETURNS TEXT AS $$
    SELECT regexp_replace(
               lower(btrim(split_part(split_part(item, ',', 1), '(', 1))),
               '^(small|medium|large|big'
               || '|kleine|kleiner|kleines|klein'
               || '|mittlere|mittlerer|mittleres|mittel'
               || '|große|großer|großes|groß'
               || '|grosse|grosser|grosses|gross'
               || '|petit|petite|moyen|moyenne|grand'
               || '|pequeño|pequeña|mediano|mediana|grande'
               || ')\s+(.+)$',
               '\2');
$$ LANGUAGE SQL IMMUTABLE STRICT;
-- +goose StatementEnd

-- +goose StatementBegin
-- Exactly 00037's rewrite, for exactly 00037's reason: names already stored normalised
-- were normalised by the old rule, and anything holding a size will never match again
-- unless it is rewritten. Several rows can collapse onto one name and both tables have a
-- unique key on it, so one row per key may be rewritten -- shortest name wins, which is
-- the bare food when it is already there.
--
-- A pantry loser is left alone rather than merged: adding two quantities the user never
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

-- A gram loser is dropped, not kept -- but unlike 00037 the two claims are NOT the same
-- claim: "large onion" -> 150 g was the weight of a large one, and after the rewrite the
-- row would read "one onion is 150 g" while ToGrams multiplies it by 1.35 again. Dropping
-- the sized rows outright and letting cmd/backfill-grams refill "onion" is the only
-- version that cannot leave a silently inflated weight behind.
DELETE FROM ingredient_grams a
WHERE a.name <> normalize_item(a.name)
  AND EXISTS (SELECT 1
              FROM ingredient_grams b
              WHERE b.unit = a.unit
                AND normalize_item(b.name) = normalize_item(a.name)
                AND (length(b.name), b.name) < (length(a.name), a.name));

-- Whatever survived is the only row for its key, and it is a sized weight. Drop the
-- sized ones rather than rewriting them, for the reason above; the backfill refills the
-- bare name from scratch.
DELETE FROM ingredient_grams WHERE name <> normalize_item(name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The sizes are gone from the normalised names; only 00037's function comes back.
CREATE OR REPLACE FUNCTION normalize_item(item TEXT) RETURNS TEXT AS $$
    SELECT lower(btrim(split_part(split_part(item, ',', 1), '(', 1)));
$$ LANGUAGE SQL IMMUTABLE STRICT;
-- +goose StatementEnd
