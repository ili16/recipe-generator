-- +goose Up
-- +goose StatementBegin
-- Server-held chat threads for POST /api/v1/chat (specs/AGENT_CHAT.md §2.1). The client
-- sends one message per turn instead of re-uploading the whole transcript, so history
-- policy (truncation, summarisation) becomes a server concern with no client release.
-- Anonymous callers get an in-memory thread instead of a row here; nothing to attach to.
CREATE TABLE conversations
(
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    INT         NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_conversations_user ON conversations (user_id, updated_at DESC);

CREATE TABLE conversation_messages
(
    id              BIGSERIAL   PRIMARY KEY,
    conversation_id UUID        NOT NULL REFERENCES conversations (id) ON DELETE CASCADE,
    -- seq orders a thread deterministically: created_at alone can tie, since an
    -- assistant message and the tool results it triggered are written in one batch.
    seq             INT         NOT NULL,
    role            TEXT        NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    content         TEXT        NOT NULL DEFAULT '',
    -- tool_calls holds the assistant's requested calls verbatim ([{id, name, args}]);
    -- tool_call_id ties a tool result back to the call that asked for it.
    tool_calls      JSONB,
    tool_call_id    TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (conversation_id, seq)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE conversation_messages;
DROP TABLE conversations;
-- +goose StatementEnd
