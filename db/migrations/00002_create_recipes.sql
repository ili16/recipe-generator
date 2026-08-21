-- +goose Up
-- +goose StatementBegin
CREATE TABLE recipes
(
    id                    SERIAL PRIMARY KEY,
    public_id             UUID           NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    user_id               INT            NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title                 TEXT           NOT NULL,
    summary               TEXT,
    category              TEXT           NOT NULL DEFAULT 'Sonstiges',
    language              TEXT           NOT NULL DEFAULT 'en',
    servings              INT,
    prep_minutes          INT,
    cook_minutes          INT,
    total_minutes         INT,
    difficulty            TEXT,
    source_type           TEXT           NOT NULL DEFAULT 'text'
        CHECK (source_type IN ('text', 'url', 'image', 'voice', 'manual', 'import')),
    source_url            TEXT,
    -- Rendered markdown draft; authoritative content lives in recipe_ingredients/recipe_steps.
    content               TEXT,
    ai_model              TEXT,
    ai_prompt_version     TEXT,
    extraction_confidence NUMERIC(4, 3),
    current_version       INT            NOT NULL DEFAULT 1,
    attributes            JSONB          NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    deleted_at            TIMESTAMPTZ
);

CREATE INDEX idx_recipes_user_updated ON recipes (user_id, updated_at DESC);
CREATE INDEX idx_recipes_user_category ON recipes (user_id, category);
CREATE INDEX idx_recipes_attributes ON recipes USING GIN (attributes);

-- `section` groups ingredients into blocks such as "Dough" / "Sauce".
CREATE TABLE recipe_ingredients
(
    id            BIGSERIAL PRIMARY KEY,
    recipe_id     INT            NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    section       TEXT,
    sort_order    INT            NOT NULL,
    item          TEXT           NOT NULL,
    -- quantity drives on-device scaling; quantity_text keeps unparseable input like "a pinch".
    quantity      NUMERIC(10, 3),
    quantity_text TEXT,
    unit          TEXT,
    notes         TEXT,
    optional      BOOLEAN        NOT NULL DEFAULT FALSE,
    attributes    JSONB          NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    UNIQUE (recipe_id, sort_order)
);

CREATE TABLE recipe_steps
(
    id            BIGSERIAL PRIMARY KEY,
    recipe_id     INT         NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    section       TEXT,
    sort_order    INT         NOT NULL,
    step_text     TEXT        NOT NULL,
    timer_seconds INT,
    temperature_c INT,
    attributes    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (recipe_id, sort_order)
);

-- Immutable record of what the user handed us before AI touched it.
CREATE TABLE sources
(
    id             BIGSERIAL PRIMARY KEY,
    recipe_id      INT         REFERENCES recipes (id) ON DELETE CASCADE,
    user_id        INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source_type    TEXT        NOT NULL
        CHECK (source_type IN ('text', 'url', 'image', 'voice', 'manual', 'import')),
    source_url     TEXT,
    raw_text       TEXT,
    extracted_text TEXT,
    media_mime     TEXT,
    media_bytes    BIGINT,
    metadata       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sources_recipe_id ON sources (recipe_id);
CREATE INDEX idx_sources_user_id ON sources (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE sources;
DROP TABLE recipe_steps;
DROP TABLE recipe_ingredients;
DROP TABLE recipes;
-- +goose StatementEnd
