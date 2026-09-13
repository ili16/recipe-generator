-- +goose Up
-- +goose StatementBegin
-- BACKLOG.md 9.11. A leftover was never stored: the client inferred one from "yesterday
-- held this same recipe_id" (planDays.ts), which is wrong whenever reality is -- cook the
-- same dish twice on purpose and the second day is labelled leftovers with no way to say
-- otherwise. Non-null source_item_id means: this is the meal cooked on that item's day,
-- eaten again here. ON DELETE CASCADE is the semantics -- clearing the cook clears its
-- leftovers, because you did not cook it.
ALTER TABLE meal_plan_items
    ADD COLUMN source_item_id INT REFERENCES meal_plan_items(id) ON DELETE CASCADE;

CREATE INDEX idx_meal_plan_items_source ON meal_plan_items(source_item_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill: apply the client's heuristic once, so weeks planned before this keep the
-- labels they already show. A run of consecutive dates holding the same recipe for the
-- same user is one cook (the earliest date, and within it the earliest meal slot) plus
-- leftovers pointing at it.
WITH items AS (
    SELECT mpi.id, mp.user_id, mpi.recipe_id, mpi.planned_on,
           CASE mpi.meal_slot WHEN 'breakfast' THEN 0 WHEN 'lunch' THEN 1 WHEN 'dinner' THEN 2 ELSE 3 END AS slot_ord
    FROM meal_plan_items mpi
    JOIN meal_plans mp ON mp.id = mpi.meal_plan_id
),
islands AS (
    SELECT user_id, recipe_id, planned_on,
           planned_on - (ROW_NUMBER() OVER (PARTITION BY user_id, recipe_id ORDER BY planned_on))::int AS run_key
    FROM (SELECT DISTINCT user_id, recipe_id, planned_on FROM items) d
),
ranked AS (
    SELECT i.id,
           FIRST_VALUE(i.id) OVER (
               PARTITION BY i.user_id, i.recipe_id, isl.run_key
               ORDER BY i.planned_on, i.slot_ord, i.id
           ) AS cook_id
    FROM items i
    JOIN islands isl ON isl.user_id = i.user_id AND isl.recipe_id = i.recipe_id AND isl.planned_on = i.planned_on
)
UPDATE meal_plan_items m
SET source_item_id = r.cook_id
FROM ranked r
WHERE m.id = r.id AND r.cook_id <> m.id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_meal_plan_items_source;
ALTER TABLE meal_plan_items DROP COLUMN source_item_id;
-- +goose StatementEnd
