-- +goose Up
-- +goose StatementBegin
-- Reference vocabularies used to validate AI extraction output.
CREATE TABLE techniques
(
    id          SERIAL PRIMARY KEY,
    slug        TEXT        NOT NULL UNIQUE,
    label       TEXT        NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE flavor_profiles
(
    id          SERIAL PRIMARY KEY,
    slug        TEXT        NOT NULL UNIQUE,
    label       TEXT        NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE recipe_techniques
(
    recipe_id    INT           NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    technique_id INT           NOT NULL REFERENCES techniques (id) ON DELETE CASCADE,
    confidence   NUMERIC(4, 3),
    PRIMARY KEY (recipe_id, technique_id)
);

CREATE TABLE recipe_flavor_profiles
(
    recipe_id         INT           NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    flavor_profile_id INT           NOT NULL REFERENCES flavor_profiles (id) ON DELETE CASCADE,
    confidence        NUMERIC(4, 3),
    PRIMARY KEY (recipe_id, flavor_profile_id)
);

CREATE TABLE creator_follows
(
    id           BIGSERIAL PRIMARY KEY,
    user_id      INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    label        TEXT,
    feed_url     TEXT        NOT NULL,
    site_url     TEXT,
    last_checked TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, feed_url)
);

CREATE TABLE feed_candidates
(
    id           BIGSERIAL PRIMARY KEY,
    follow_id    BIGINT      NOT NULL REFERENCES creator_follows (id) ON DELETE CASCADE,
    user_id      INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title        TEXT,
    url          TEXT        NOT NULL,
    published_at TIMESTAMPTZ,
    state        TEXT        NOT NULL DEFAULT 'new'
        CHECK (state IN ('new', 'dismissed', 'imported')),
    recipe_id    INT         REFERENCES recipes (id) ON DELETE SET NULL,
    metadata     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, url)
);

CREATE INDEX idx_feed_candidates_user_state ON feed_candidates (user_id, state, published_at DESC);

CREATE TABLE meal_plans
(
    id         BIGSERIAL PRIMARY KEY,
    user_id    INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      TEXT        NOT NULL,
    starts_on  DATE        NOT NULL,
    ends_on    DATE        NOT NULL,
    notes      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (ends_on >= starts_on)
);

CREATE INDEX idx_meal_plans_user ON meal_plans (user_id, starts_on DESC);

CREATE TABLE meal_plan_items
(
    id           BIGSERIAL PRIMARY KEY,
    meal_plan_id BIGINT      NOT NULL REFERENCES meal_plans (id) ON DELETE CASCADE,
    recipe_id    INT         NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    planned_on   DATE        NOT NULL,
    meal_slot    TEXT        NOT NULL DEFAULT 'dinner'
        CHECK (meal_slot IN ('breakfast', 'lunch', 'dinner', 'snack')),
    servings     INT,
    notes        TEXT,
    sort_order   INT         NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_meal_plan_items_plan ON meal_plan_items (meal_plan_id, planned_on, sort_order);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE meal_plan_items;
DROP TABLE meal_plans;
DROP TABLE feed_candidates;
DROP TABLE creator_follows;
DROP TABLE recipe_flavor_profiles;
DROP TABLE recipe_techniques;
DROP TABLE flavor_profiles;
DROP TABLE techniques;
-- +goose StatementEnd
