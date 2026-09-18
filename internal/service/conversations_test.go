package service

import (
	"encoding/json"
	"testing"

	"github.com/ili16/recipe-generator/internal/model"
)

// One stored turn is several messages; the transcript is one user bubble and one
// assistant bubble carrying the card. Getting this wrong shows a reopened thread as a
// wall of empty assistant bubbles, or loses the recipe (BACKLOG.md 10.4).
func TestThreadBubbles(t *testing.T) {
	card := json.RawMessage(`{"kind":"recipe","data":{"draft_ref":"draft_1"}}`)
	msgs := []model.ConversationMessage{
		{Role: "system", Content: "Earlier in this conversation (4 messages, summarised): pasta"},
		{Role: "user", Content: "a pasta please\n\n[attached photo] image_ref=image1"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "1", Name: "generate_recipe"}}},
		{Role: "tool", ToolCallID: "1", Content: `{"draft_ref":"draft_1"}`, Artifact: card},
		{Role: "assistant", Content: "Here it is."},
		{Role: "user", Content: "thanks"},
	}

	got := threadBubbles(msgs)
	if len(got) != 3 {
		t.Fatalf("expected user/assistant/user, got %d bubbles: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Text != "a pasta please" || got[0].AttachmentCount != 1 {
		t.Errorf("user bubble kept the image marker or lost the photo: %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Text != "Here it is." {
		t.Errorf("assistant turn did not collapse into one bubble: %+v", got[1])
	}
	if len(got[1].Artifacts) != 1 || string(got[1].Artifacts[0]) != string(card) {
		t.Errorf("the card did not land on the turn that made it: %+v", got[1].Artifacts)
	}
	if got[2].Role != "user" || got[2].AttachmentCount != 0 {
		t.Errorf("second user bubble wrong: %+v", got[2])
	}
}

// A thread reopened after the in-memory session is gone still has to be able to
// transform and save its drafts, so the documents come back off the stored cards.
func TestRehydrateDrafts(t *testing.T) {
	doc := model.RecipeDocument{Title: "Pasta"}
	data, err := json.Marshal(map[string]any{
		"kind": "recipe",
		"data": map[string]any{"draft_ref": "draft_2", "document": doc, "recipe_id": 7},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &chatSession{
		drafts:    map[string]model.RecipeDocument{},
		draftInfo: map[string]draftInfo{},
		messages:  []model.ConversationMessage{{Role: "tool", Artifact: data}},
	}

	rehydrateDrafts(s)

	if s.drafts["draft_2"].Title != "Pasta" {
		t.Fatalf("draft not restored: %+v", s.drafts)
	}
	if s.draftInfo["draft_2"].savedID != 7 {
		t.Errorf("a restored draft that was already saved must not save twice: %+v", s.draftInfo)
	}
	// The counter has to clear the restored refs or the next draft overwrites one.
	if s.nextDraft != 2 {
		t.Errorf("nextDraft = %d, want 2", s.nextDraft)
	}
}
