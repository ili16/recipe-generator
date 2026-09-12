-- +goose Up
-- +goose StatementBegin
CREATE TABLE collections (
    id         SERIAL PRIMARY KEY,
    user_id    INT          NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       VARCHAR(120) NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, name)
);

-- A recipe may sit in several collections; dropping a collection drops only the
-- membership rows, never the recipes (BACKLOG 8.1).
CREATE TABLE recipe_collections (
    collection_id INT         NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    recipe_id     INT         NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    added_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (collection_id, recipe_id)
);

CREATE INDEX idx_recipe_collections_recipe ON recipe_collections (recipe_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE recipe_collections;
DROP TABLE collections;
-- +goose StatementEnd
