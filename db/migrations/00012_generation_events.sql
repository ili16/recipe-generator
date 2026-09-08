-- +goose Up
-- +goose StatementBegin
CREATE TABLE generation_events (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source_type TEXT        NOT NULL CHECK (source_type IN ('text', 'url', 'image')),
    prompt      TEXT,
    had_refine  BOOLEAN     NOT NULL DEFAULT FALSE,
    outcome     TEXT        NOT NULL DEFAULT 'pending'
        CHECK (outcome IN ('pending', 'saved', 'refined', 'declined', 'abandoned')),
    recipe_id   INT REFERENCES recipes (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX idx_generation_events_pending ON generation_events (created_at) WHERE outcome = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE generation_events;
-- +goose StatementEnd
