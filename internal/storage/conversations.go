package storage

import (
	"context"
	"errors"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/jackc/pgx/v5"
)

// ErrConversationNotFound is returned when a conversation id doesn't exist or belongs to
// another user — deliberately the same error for both, so a caller can't probe for the
// existence of someone else's thread.
var ErrConversationNotFound = errors.New("conversation not found")

// CreateConversation opens a new thread for userID and returns its id.
func CreateConversation(ctx context.Context, userID int, title string) (string, error) {
	var id string
	err := Pool.QueryRow(ctx,
		`INSERT INTO conversations (user_id, title) VALUES ($1, $2) RETURNING id`,
		userID, title).Scan(&id)
	return id, err
}

// ConversationMessages returns every message in a conversation in thread order, after
// checking the conversation belongs to userID.
func ConversationMessages(ctx context.Context, userID int, conversationID string) ([]model.ConversationMessage, error) {
	var owner int
	err := Pool.QueryRow(ctx, `SELECT user_id FROM conversations WHERE id = $1`, conversationID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != userID) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, err
	}

	rows, err := Pool.Query(ctx, `
		SELECT role, content, tool_calls, tool_call_id
		FROM conversation_messages
		WHERE conversation_id = $1
		ORDER BY seq`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []model.ConversationMessage
	for rows.Next() {
		var m model.ConversationMessage
		var toolCallID *string
		if err := rows.Scan(&m.Role, &m.Content, &m.ToolCalls, &toolCallID); err != nil {
			return nil, err
		}
		if toolCallID != nil {
			m.ToolCallID = *toolCallID
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// AppendConversationMessages writes msgs to the end of a conversation in one transaction,
// numbering them from the current max seq. Ownership is enforced by the same
// conversation-belongs-to-user check as reads; an unowned id appends nothing.
func AppendConversationMessages(ctx context.Context, userID int, conversationID string, msgs []model.ConversationMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var owner int
	err = tx.QueryRow(ctx, `SELECT user_id FROM conversations WHERE id = $1 FOR UPDATE`, conversationID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != userID) {
		return ErrConversationNotFound
	}
	if err != nil {
		return err
	}

	var next int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM conversation_messages WHERE conversation_id = $1`,
		conversationID).Scan(&next); err != nil {
		return err
	}

	for i, m := range msgs {
		var toolCallID *string
		if m.ToolCallID != "" {
			toolCallID = &m.ToolCallID
		}
		var toolCalls any
		if len(m.ToolCalls) > 0 {
			toolCalls = m.ToolCalls
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_messages (conversation_id, seq, role, content, tool_calls, tool_call_id)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			conversationID, next+i, m.Role, m.Content, toolCalls, toolCallID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET updated_at = NOW() WHERE id = $1`, conversationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
