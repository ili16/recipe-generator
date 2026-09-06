-- +goose Up
-- +goose StatementBegin
ALTER TABLE users ADD COLUMN monthly_budget_usd_override NUMERIC(10,4);

CREATE TABLE llm_usage_events (
    id                 BIGSERIAL PRIMARY KEY,
    user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    call_type          TEXT NOT NULL,
    model              TEXT NOT NULL,
    prompt_tokens      INTEGER NOT NULL DEFAULT 0,
    completion_tokens  INTEGER NOT NULL DEFAULT 0,
    cost_usd           NUMERIC(10,6) NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_llm_usage_events_user_period ON llm_usage_events (user_id, created_at);
-- ponytail: no retention job yet, no volume to justify it. Add a scheduled
-- DELETE FROM llm_usage_events WHERE created_at < now() - interval '13 months'
-- once real volume exists.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE llm_usage_events;
ALTER TABLE users DROP COLUMN monthly_budget_usd_override;
-- +goose StatementEnd
