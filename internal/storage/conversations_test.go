package storage

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// TestConversationRoundTrip is a smoke test against a real DB (skipped unless DB_URL is
// set) for the agent-loop thread storage added in migration 00017: a thread with an
// assistant tool-call message and its matching tool result survives a write/read cycle
// intact, seq keeps the order, and another user cannot read or append to the thread.
func TestConversationRoundTrip(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID, otherUserID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-conv-owner') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-conv-other') RETURNING id`).Scan(&otherUserID); err != nil {
		t.Fatalf("seed other user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, otherUserID) })

	convID, err := CreateConversation(ctx, userID, "Pasta night")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	turn := []model.ConversationMessage{
		{Role: "user", Content: "give me a pasta recipe"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "call_1", Name: "generate_recipe", Args: `{"description":"pasta"}`}}},
		{Role: "tool", Content: `{"draft_ref":"draft_1"}`, ToolCallID: "call_1"},
		{Role: "assistant", Content: "Here's a pasta."},
	}
	if err := AppendConversationMessages(ctx, userID, convID, turn); err != nil {
		t.Fatalf("AppendConversationMessages: %v", err)
	}
	// A second turn must continue the seq, not restart it.
	if err := AppendConversationMessages(ctx, userID, convID, []model.ConversationMessage{{Role: "user", Content: "make it vegan"}}); err != nil {
		t.Fatalf("AppendConversationMessages (second turn): %v", err)
	}

	got, err := ConversationMessages(ctx, userID, convID)
	if err != nil {
		t.Fatalf("ConversationMessages: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(got))
	}
	if got[0].Content != "give me a pasta recipe" || got[4].Content != "make it vegan" {
		t.Errorf("thread out of order: %+v", got)
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].Name != "generate_recipe" || got[1].ToolCalls[0].Args != `{"description":"pasta"}` {
		t.Errorf("tool calls did not round-trip: %+v", got[1])
	}
	if got[2].ToolCallID != "call_1" {
		t.Errorf("expected tool_call_id call_1, got %q", got[2].ToolCallID)
	}

	// Another user must not be able to read or extend the thread — and must get the
	// same error as for a thread that doesn't exist at all.
	if _, err := ConversationMessages(ctx, otherUserID, convID); !errors.Is(err, ErrConversationNotFound) {
		t.Errorf("expected ErrConversationNotFound reading another user's thread, got %v", err)
	}
	if err := AppendConversationMessages(ctx, otherUserID, convID, []model.ConversationMessage{{Role: "user", Content: "hijack"}}); !errors.Is(err, ErrConversationNotFound) {
		t.Errorf("expected ErrConversationNotFound appending to another user's thread, got %v", err)
	}
	if after, _ := ConversationMessages(ctx, userID, convID); len(after) != 5 {
		t.Errorf("a rejected append still wrote something: %d messages", len(after))
	}
}
