-- +goose Up
-- +goose StatementBegin
-- BACKLOG 15.9. Which lines are ticked off has lived in per-device AsyncStorage since 6.1,
-- keyed (user, week). That was defensible when a kitchen was one cook. In a household it is
-- the single most obvious thing two people do -- split the shop -- and their ticks never
-- meet, so one of them buys the milk twice.
--
-- This is the first genuinely shared *mutable* state in the app, so it gets the same shape
-- as every other shared thing rather than a second notion of sharing: a nullable
-- household_id, and a uniqueness key on COALESCE(household_id::text, 'u' || user_id) --
-- character for character the scope key 00035 gave pantry_items.
--
-- That index is not bookkeeping, it is the "exactly once" half of the acceptance. A tick
-- writes the line into the pantry (7.2); without a single row per line per scope, two
-- members ticking the milk within a second of each other each own a pantry write, and the
-- first to untick deletes a row the other still believes in. With it, the second tick
-- conflicts, reuses the first's pantry_item_id, and the pantry is written once.
--
-- week_start and not the plan id: the list is derived from whatever the plan says right
-- now (6.1), plans get replaced, and a tick is about a shopping trip for a week. line_key
-- is the client's item|unit identity -- the same string the ScrollView keys on -- because
-- the list reshuffles and an index would tick the wrong thing.
CREATE TABLE grocery_ticks
(
    id             BIGSERIAL PRIMARY KEY,
    user_id        INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- SET NULL, never CASCADE, for 00035's reason: disbanding a household orphans rows
    -- back to the member who made them. A tick then simply becomes that member's own.
    household_id   UUID REFERENCES households (id) ON DELETE SET NULL,
    week_start     DATE        NOT NULL,
    line_key       TEXT        NOT NULL,
    -- The pantry row this tick created, so unticking knows what to take back out. NULL is
    -- a tick whose pantry write failed: still ticked, nothing behind it -- exactly the
    -- state every tick had before 7.2.
    pantry_item_id INT REFERENCES pantry_items (id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_grocery_ticks_scope_week_line
    ON grocery_ticks (COALESCE(household_id::text, 'u' || user_id), week_start, line_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE grocery_ticks;
-- +goose StatementEnd
