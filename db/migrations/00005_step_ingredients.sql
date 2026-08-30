-- +goose Up
-- +goose StatementBegin
CREATE TABLE step_ingredients
(
    step_id       BIGINT NOT NULL REFERENCES recipe_steps (id) ON DELETE CASCADE,
    ingredient_id BIGINT NOT NULL REFERENCES recipe_ingredients (id) ON DELETE CASCADE,
    PRIMARY KEY (step_id, ingredient_id)
);

CREATE INDEX idx_step_ingredients_step ON step_ingredients (step_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE step_ingredients;
-- +goose StatementEnd
