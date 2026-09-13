-- +goose Up
-- +goose StatementBegin
-- BACKLOG 15.1. Until now the app assumed one cook per account: every row is scoped to a
-- single user_id, and household_size (00030) is a household property masquerading as a
-- personal integer -- you can say four people eat here, but not *which* four.
--
-- A household makes the kitchen the unit instead of the person. It is deliberately NOT
-- the "Collaboration / creator features" the backlog declines: there is no discovery
-- surface, no public read path and no social graph. A closed, mutual-consent group.
--
-- The mechanism is one idea used everywhere, so there is no second notion of "shared":
-- a nullable household_id on every table that can be shared. NULL means private to
-- user_id, which is every row that exists today, and every widened read predicate
-- collapses to today's behaviour when the caller's own household_id is NULL. The change
-- is inert until somebody creates a household.
--
-- Collections deliberately do NOT get one. A collection is your own grouping over the
-- library you can see; sharing the library does not mean sharing how you file it.
CREATE TABLE households
(
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    -- The id is stable identity; the join code is the credential, and is rotatable on
    -- purpose -- a member who leaves must not keep a permanent key back in. Same
    -- reasoning as recipes.share_token (00024), different lifetime.
    join_code  TEXT        NOT NULL UNIQUE,
    created_by INT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ON DELETE SET NULL everywhere, never CASCADE: dropping a household must orphan rows
-- back to the owners they never stopped carrying, not destroy a member's recipes.
ALTER TABLE users ADD COLUMN household_id UUID REFERENCES households (id) ON DELETE SET NULL;
ALTER TABLE recipes ADD COLUMN household_id UUID REFERENCES households (id) ON DELETE SET NULL;
ALTER TABLE pantry_items ADD COLUMN household_id UUID REFERENCES households (id) ON DELETE SET NULL;
ALTER TABLE meal_plans ADD COLUMN household_id UUID REFERENCES households (id) ON DELETE SET NULL;

CREATE INDEX idx_users_household ON users (household_id);
CREATE INDEX idx_recipes_household ON recipes (household_id) WHERE household_id IS NOT NULL;
CREATE INDEX idx_pantry_household ON pantry_items (household_id) WHERE household_id IS NOT NULL;
CREATE INDEX idx_meal_plans_household ON meal_plans (household_id) WHERE household_id IS NOT NULL;

-- The pantry's uniqueness is what makes "one fridge, one flour line" true (00026), and it
-- is currently per-user -- which in a shared household would be two flour lines for one
-- bag. Re-key it to the scope: the household when there is one, the user otherwise.
DROP INDEX idx_pantry_items_user_name_unit;
CREATE UNIQUE INDEX idx_pantry_items_scope_name_unit
    ON pantry_items (COALESCE(household_id::text, 'u' || user_id), name, COALESCE(unit, ''));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_pantry_items_scope_name_unit;
CREATE UNIQUE INDEX idx_pantry_items_user_name_unit
    ON pantry_items (user_id, name, COALESCE(unit, ''));

ALTER TABLE meal_plans DROP COLUMN household_id;
ALTER TABLE pantry_items DROP COLUMN household_id;
ALTER TABLE recipes DROP COLUMN household_id;
ALTER TABLE users DROP COLUMN household_id;

DROP TABLE households;
-- +goose StatementEnd
