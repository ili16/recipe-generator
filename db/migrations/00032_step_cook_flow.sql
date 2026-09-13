-- +goose Up
-- +goose StatementBegin
-- A recipe knew its steps but not its *cook flow*: which steps are mise en place, which
-- are the heat being on, which are just waiting -- and, worse, nothing could say that two
-- steps happen at the same time. Boiling water while you chop was serialised into two
-- screens, so cooking mode taught people to cook slowly.
--
-- Both columns are nullable and both stay NULL on every existing recipe: no backfill,
-- because nothing in the stored text says which phase a step is, and a guess would write a
-- fact the model never asserted. A step list with no phase and no group renders exactly as
-- it did before -- one step per screen, no phase label, no "at the same time".
ALTER TABLE recipe_steps
    ADD COLUMN phase text,
    ADD COLUMN parallel_group integer;

-- Only the three the generation schema can emit. A row may still be NULL ("not stated").
ALTER TABLE recipe_steps
    ADD CONSTRAINT recipe_steps_phase_check
    CHECK (phase IS NULL OR phase IN ('prep', 'cook', 'wait'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE recipe_steps
    DROP CONSTRAINT IF EXISTS recipe_steps_phase_check;
ALTER TABLE recipe_steps
    DROP COLUMN IF EXISTS phase,
    DROP COLUMN IF EXISTS parallel_group;
-- +goose StatementEnd
