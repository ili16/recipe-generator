-- +goose Up
-- +goose StatementBegin
CREATE TABLE recipe_votes (
    recipe_id  INT         NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    user_id    INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    vote       SMALLINT    NOT NULL CHECK (vote IN (-1, 1)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (recipe_id, user_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE recipe_votes;
-- +goose StatementEnd
