-- +goose Up
-- +goose StatementBegin
-- Per-serving macros, all nullable: the model cannot always estimate them, and a
-- confidently wrong calorie count is worse than a blank (BACKLOG 6.6). These are
-- model estimates, never measured values.
ALTER TABLE recipes
    ADD COLUMN calories  INT,
    ADD COLUMN protein_g INT,
    ADD COLUMN carbs_g   INT,
    ADD COLUMN fat_g     INT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE recipes
    DROP COLUMN calories,
    DROP COLUMN protein_g,
    DROP COLUMN carbs_g,
    DROP COLUMN fat_g;
-- +goose StatementEnd
