-- +goose Up
-- +goose StatementBegin
-- BACKLOG 17.2. Two different facts about a food, with deliberately different blast radii.
-- Keeping them in one table is fine; conflating them is not.
--
--   canonical -- the SAME food under another name. "parmigiano reggiano" is parmesan,
--                "aubergine" is eggplant, "zwiebel" is onion. Safe to merge everywhere:
--                the pantry match, the grocery list, the gram conversion. This is also
--                the cross-language join the app never had -- before this, a library
--                holding one German and one English recipe shopped for onions twice.
--
--   class     -- a food you could SUBSTITUTE, not the same food. spaghetti, linguine and
--                penne are all pasta_dry. Read only by the substitution surfaces (17.4).
--                Never merged into a grocery line: you do not buy "pasta", you buy a box.
--
-- The direction of canonical is fixed by the prompt (the common English name), so that two
-- rows resolved a year apart agree. A row is never overwritten once written -- same
-- contract as ingredient_grams, and for the same reason.
CREATE TABLE ingredient_alias
(
    name       VARCHAR(200) PRIMARY KEY, -- always the output of normalize_item()
    canonical  VARCHAR(200) NOT NULL CHECK (btrim(canonical) <> ''),
    class      TEXT,
    source     TEXT         NOT NULL DEFAULT 'llm',
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ingredient_alias_class ON ingredient_alias (class) WHERE class IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- pg_trgm ships in the stock postgres image, so this costs an extension and nothing else.
-- It backs one thing only: a similarity fallback for a name no exact row answers, used
-- BELOW the exact join and never above it (see storage.NearestCanonical). Inflection and
-- typos -- "zwiebeln" for "zwiebel", "tomatoe" -- are not worth enumerating by hand.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX idx_ingredient_alias_canonical_trgm ON ingredient_alias USING gin (canonical gin_trgm_ops);
-- +goose StatementEnd

-- +goose StatementBegin
-- canonical_item() is normalize_item() plus the alias hop, and it is what the joins use.
-- It is STABLE rather than IMMUTABLE because it reads a table -- fine in a WHERE or a
-- JOIN, which is all it is for, and deliberately not usable in an index so that nothing
-- can come to depend on an alias row never changing.
--
-- An unresolved name resolves to itself, so this is a no-op until the table fills. That
-- is what makes it safe to swap in at every join site in one go.
CREATE OR REPLACE FUNCTION canonical_item(item TEXT) RETURNS TEXT AS $$
    SELECT COALESCE(
        (SELECT a.canonical FROM ingredient_alias a WHERE a.name = normalize_item(item)),
        normalize_item(item));
$$ LANGUAGE SQL STABLE STRICT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS canonical_item(TEXT);
DROP TABLE IF EXISTS ingredient_alias;
-- pg_trgm is left installed: dropping an extension another migration or a hand-built
-- index may since have leaned on is a worse failure than leaving it there.
-- +goose StatementEnd
