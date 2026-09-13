-- +goose Up
-- +goose StatementBegin
-- The meal_type group could say what a dish *is* (main course, side, dessert) but never
-- when it is eaten: there was no lunch and no dinner, so "which meal is this" -- the first
-- thing a person scans a recipe list for -- was unanswerable. Two slugs in the taxonomy
-- that already owns this, not the recipes.category column 00006 deliberately dropped.
--
-- No backfill: nothing in the data says whether an existing 'main-courses' recipe is lunch
-- or dinner, and guessing would write a fact the user never said. Old recipes keep showing
-- their existing meal-type tag.
INSERT INTO tags (slug, label, tag_group)
VALUES ('lunch', 'Lunch', 'meal_type'),
       ('dinner', 'Dinner', 'meal_type');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- recipe_tags rows referencing these go with them (ON DELETE CASCADE), so a recipe tagged
-- 'dinner' comes back untagged for meal type rather than mis-tagged.
DELETE FROM tags WHERE slug IN ('lunch', 'dinner');
-- +goose StatementEnd
