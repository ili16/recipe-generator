-- +goose Up
-- +goose StatementBegin
-- Full canonical snapshot per accepted edit. Enables AI diff review and rollback.
CREATE TABLE recipe_versions
(
    id          BIGSERIAL PRIMARY KEY,
    recipe_id   INT         NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    version     INT         NOT NULL,
    data        JSONB       NOT NULL,
    change_kind TEXT        NOT NULL DEFAULT 'manual'
        CHECK (change_kind IN ('extraction', 'manual', 'ai_edit', 'import')),
    change_note TEXT,
    created_by  INT         REFERENCES users (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (recipe_id, version)
);

CREATE TABLE recipe_notes
(
    id            BIGSERIAL PRIMARY KEY,
    recipe_id     INT         NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    user_id       INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    note_text     TEXT        NOT NULL,
    rating        SMALLINT CHECK (rating BETWEEN 1 AND 5),
    variant_label TEXT,
    cooked_at     TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_recipe_notes_recipe ON recipe_notes (recipe_id, created_at DESC);
CREATE INDEX idx_recipe_notes_user ON recipe_notes (user_id);

-- Append-only activity log.
CREATE TABLE recipe_timeline_events
(
    id          BIGSERIAL PRIMARY KEY,
    recipe_id   INT         NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    user_id     INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    event_type  TEXT        NOT NULL
        CHECK (event_type IN ('created', 'extracted', 'cooked', 'edited', 'ai_edit_accepted',
                              'ai_edit_rejected', 'note_added', 'rated', 'published')),
    version     INT,
    metadata    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_recipe_timeline_recipe ON recipe_timeline_events (recipe_id, occurred_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE recipe_timeline_events;
DROP TABLE recipe_notes;
DROP TABLE recipe_versions;
-- +goose StatementEnd
