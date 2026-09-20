package service

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/model"
)

// TestLongThreadSurvivesCompaction is BACKLOG.md 9.4: compactCut's arithmetic is unit-tested
// in TestCompaction, but the summariser prompt itself had never run. This drives a 45-message
// thread past the cap and asks a question whose answer appears only in message 3 — the span
// that compaction replaces with prose. It fails if thread_summary.txt drops the constraint.
//
// Costs two real model calls; gated on OPENAI_KEY like the DB tests are on DB_URL.
func TestLongThreadSurvivesCompaction(t *testing.T) {
	if os.Getenv("OPENAI_KEY") == "" {
		t.Skip("OPENAI_KEY not set, skipping live summariser test")
	}

	// The fact under test, stated once and never repeated: an equipment constraint, which is
	// exactly what a cook expects the assistant to still know forty messages later.
	msgs := []model.ConversationMessage{
		{Role: "user", Content: "hi, I want to cook more at home"},
		{Role: "assistant", Content: "Happy to help. What does your kitchen look like?"},
		{Role: "user", Content: "I only have an induction hob and one big pan, a 28cm carbon-steel skillet. No oven at all."},
		{Role: "assistant", Content: "Noted — induction, a 28cm carbon-steel skillet, no oven."},
	}
	// Filler that never mentions the constraint again, long enough to push the cut past it.
	for i := len(msgs); i < 45; i += 2 {
		n := strconv.Itoa(i)
		msgs = append(msgs,
			model.ConversationMessage{Role: "user", Content: "what about a quick weeknight dinner, idea " + n + "?"},
			model.ConversationMessage{Role: "assistant", Content: "Idea " + n + ": a simple pasta with garlic, chilli and parsley, about 20 minutes."},
		)
	}

	ctx := context.Background()
	compacted, summary := compactThread(ctx, msgs)
	if summary == nil {
		t.Fatal("a 45-message thread was not compacted")
	}
	t.Logf("summary: %s", summary.Content)
	if len(compacted) >= len(msgs) {
		t.Fatalf("compacted thread is not shorter: %d vs %d", len(compacted), len(msgs))
	}

	// The acceptance: with messages 1-25 gone, the thread must still answer off the summary.
	ask := append(compacted, model.ConversationMessage{
		Role:    "user",
		Content: "Remind me what pan and hob I told you I have — just the equipment, one line.",
	})
	answer, _, err := llm.Call[string](ctx, llm.Spec{
		Model:    assistModel,
		CallType: "thread_summary_check",
		Messages: threadToParams(ask, "", ""),
	})
	if err != nil {
		t.Fatalf("follow-up call failed: %v", err)
	}
	t.Logf("answer: %s", answer)
	lower := strings.ToLower(answer)
	for _, want := range []string{"carbon", "induction"} {
		if !strings.Contains(lower, want) {
			t.Errorf("the answer lost %q — the summariser prompt dropped the constraint from message 3: %s", want, answer)
		}
	}
	if strings.Contains(lower, "oven") && !strings.Contains(lower, "no oven") && !strings.Contains(lower, "without an oven") {
		t.Errorf("the answer invented an oven: %s", answer)
	}
}
