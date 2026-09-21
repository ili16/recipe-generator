-- +goose Up
-- +goose StatementBegin
-- BACKLOG 18.0 audit (`backfill-aliases -audit`, beta, 2026-09-21): one food filed under
-- several canonicals, so a pantry of one never covered a recipe wanting another. Left alone on
-- purpose: cabbage/red cabbage, onion/red onion, bacon/pancetta, the dairies, the noodles --
-- same class, different food, which is what a class is for.
CREATE TEMP TABLE merge (old TEXT, new TEXT, class TEXT) ON COMMIT DROP;
INSERT INTO merge VALUES
    ('beef bouillon', 'beef stock', 'beef_stock'),
    ('beef_stock', 'beef stock', 'beef_stock'),
    ('crushed tomatoes', 'chopped tomatoes', 'canned_tomatoes'),
    ('yeast', 'instant yeast', 'yeast'),
    ('red bell pepper', 'bell pepper', 'bell_pepper'),
    ('red peppers', 'bell pepper', 'bell_pepper');

-- Every name that resolved to the old canonical now resolves to the new one...
UPDATE ingredient_alias a SET canonical = m.new, class = m.class, source = 'manual'
FROM merge m WHERE a.canonical = m.old;
-- ...and the old canonical, which may never have had a row of its own, resolves there too.
INSERT INTO ingredient_alias (name, canonical, class, source)
SELECT old, new, class, 'manual' FROM merge
ON CONFLICT (name) DO UPDATE SET canonical = EXCLUDED.canonical, class = EXCLUDED.class, source = 'manual';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
