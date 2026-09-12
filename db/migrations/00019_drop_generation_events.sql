-- +goose Up
-- +goose StatementBegin
-- BACKLOG 3.10 + 3.14: the table had no producer left. Rows were only ever created by
-- POST /generate and /generate/stream, which lost their last client in 3.5 and are
-- deleted in 3.9; /chat's generate_recipe tool never wrote one. 'declined' lost its
-- trigger with the review screen (3.6) and 'refined' lost MarkRefined's only caller
-- with /refine-recipe (3.13), so the funnel described a flow the app no longer has.
-- Generation and save volume still come from telemetry (RecordGenerate/RecordSave).
DROP TABLE IF EXISTS generation_events;
-- +goose StatementEnd

-- +goose Down
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
