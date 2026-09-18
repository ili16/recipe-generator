package storage

import (
	"context"
	"errors"
	"time"

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
		SELECT role, content, tool_calls, tool_call_id, artifact
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
		if err := rows.Scan(&m.Role, &m.Content, &m.ToolCalls, &toolCallID, &m.Artifact); err != nil {
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
		var artifact any
		if len(m.Artifact) > 0 {
			artifact = m.Artifact
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO conversation_messages (conversation_id, seq, role, content, tool_calls, tool_call_id, artifact)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			conversationID, next+i, m.Role, m.Content, toolCalls, toolCallID, artifact); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET updated_at = NOW() WHERE id = $1`, conversationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ConversationSummary is one row of the thread list (BACKLOG.md 10.4): enough to pick a
// conversation out of a list, and nothing more — the messages are a second request.
type ConversationSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Preview   string    `json:"preview"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListConversations returns a user's threads, most recently used first. Threads with no
// readable message are left out: a turn that failed before the model answered leaves a
// row behind, and an untitled empty thread in the list is a dead link.
func ListConversations(ctx context.Context, userID int, limit int) ([]ConversationSummary, error) {
	rows, err := Pool.Query(ctx, `
		SELECT c.id, c.title, c.updated_at, last.content
		FROM conversations c
		JOIN LATERAL (
			SELECT content FROM conversation_messages m
			WHERE m.conversation_id = c.id AND m.role IN ('user', 'assistant') AND m.content <> ''
			ORDER BY m.seq DESC LIMIT 1
		) last ON TRUE
		WHERE c.user_id = $1
		ORDER BY c.updated_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ConversationSummary{}
	for rows.Next() {
		var c ConversationSummary
		if err := rows.Scan(&c.ID, &c.Title, &c.UpdatedAt, &c.Preview); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetConversationTitle names a thread. Scoped to the owner like every other read here,
// so a guessed id renames nothing — and says so, since a user renaming a thread by hand
// (BACKLOG.md 10.7) needs to hear that it did not happen.
func SetConversationTitle(ctx context.Context, userID int, conversationID, title string) error {
	tag, err := Pool.Exec(ctx,
		`UPDATE conversations SET title = $3 WHERE id = $1 AND user_id = $2`,
		conversationID, userID, title)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrConversationNotFound
	}
	return err
}

// DeleteConversation removes a thread and, by the messages table's ON DELETE CASCADE,
// its transcript. Same owner scoping and same not-found error as everything else here.
func DeleteConversation(ctx context.Context, userID int, conversationID string) error {
	tag, err := Pool.Exec(ctx,
		`DELETE FROM conversations WHERE id = $1 AND user_id = $2`, conversationID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrConversationNotFound
	}
	return err
}

// ConversationTitle returns a thread's title, or ErrConversationNotFound if it is not
// this user's.
func ConversationTitle(ctx context.Context, userID int, conversationID string) (string, error) {
	var title string
	var owner int
	err := Pool.QueryRow(ctx, `SELECT user_id, title FROM conversations WHERE id = $1`, conversationID).Scan(&owner, &title)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != userID) {
		return "", ErrConversationNotFound
	}
	return title, err
}
