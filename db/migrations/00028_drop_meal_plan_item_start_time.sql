-- +goose Up
-- +goose StatementBegin
-- BACKLOG.md 9.5. start_time was added by 00013 for a per-day time editor that 4.1 and
-- 4.4 later deleted, so every row has held the 18:00:00 default ever since -- while
-- TodayBanner still scheduled a real push notification at it. An unconfigurable push at
-- a time nobody chose is the bug; the column is what made it look configurable.
-- meal_slot (00019) is the real ordering key within a day.
-- If a time is ever wanted again it comes back as one global "usual dinner time" in
-- Profile, not as a per-item column.
ALTER TABLE meal_plan_items DROP COLUMN start_time;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE meal_plan_items ADD COLUMN start_time TIME NOT NULL DEFAULT '18:00:00';
-- +goose StatementEnd
