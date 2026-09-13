package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
)

// TestSaveConversationDraft covers the "Save to my recipes" button's path: it saves the
// draft the agent parked, and — the part that is easy to get wrong — it marks the draft
// saved in the shared session, so tapping twice, or tapping and then telling the model
// "save it", yields one recipe rather than two.
// Runs against a real DB — skipped unless DB_URL is set.
func TestSaveConversationDraft(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	storage.InitDBPool()
	ctx := context.Background()

	var userID int
	if err := storage.Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-draft-save') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { storage.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	// The key the handler computes, built the same way, so this exercises the real
	// session lookup rather than a map the test owns.
	r := httptest.NewRequest("POST", "/api/v1/chat/drafts/save", nil)
	key := sessionKey(r, userID, "conv-draft-save")
	state := loadSessionState(key)
	state.drafts = map[string]model.RecipeDocument{"draft_1": testDraft}
	state.draftInfo = map[string]draftInfo{}
	state.nextDraft = 1

	id, err := saveConversationDraft(ctx, key, userID, "draft_1", nil)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Second tap — a fresh session snapshot, exactly as a second request would build one.
	again, err := saveConversationDraft(ctx, key, userID, "draft_1", nil)
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if again != id {
		t.Fatalf("saving twice made two recipes: %d then %d", id, again)
	}

	var count int
	if err := storage.Pool.QueryRow(ctx, `SELECT count(*) FROM recipes WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count recipes: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d recipes, want 1", count)
	}

	// A draft the session no longer holds is its own answer, not a server error: the
	// client shows the "ask the assistant" fallback instead of a dead button.
	if _, err := saveConversationDraft(ctx, key, userID, "draft_99", nil); !errors.Is(err, errDraftExpired) {
		t.Fatalf("unknown draft_ref: got %v, want errDraftExpired", err)
	}
}
