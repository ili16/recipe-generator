-- +goose Up
-- +goose StatementBegin
CREATE TABLE user_cooking_profiles (
    user_id     INT         PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    data        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE user_cooking_profiles;
-- +goose StatementEnd
