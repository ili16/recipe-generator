-- +goose Up
-- +goose StatementBegin
-- Thread compaction (BACKLOG.md 2.6) writes a rolling summary into the thread as a
-- 'system' message, standing in for the span it replaces. It is a stored message like
-- any other -- that is the point: the truncation becomes visible in the transcript
-- instead of happening silently on every read.
ALTER TABLE conversation_messages DROP CONSTRAINT conversation_messages_role_check;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_role_check
    CHECK (role IN ('user', 'assistant', 'tool', 'system'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM conversation_messages WHERE role = 'system';
ALTER TABLE conversation_messages DROP CONSTRAINT conversation_messages_role_check;
ALTER TABLE conversation_messages ADD CONSTRAINT conversation_messages_role_check
    CHECK (role IN ('user', 'assistant', 'tool'));
-- +goose StatementEnd
