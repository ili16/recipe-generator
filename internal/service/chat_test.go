package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ili16/recipe-generator/internal/model"
	"github.com/openai/openai-go"
)

// TestThreadToParamsShape checks the stored thread converts to the message shape the API
// requires: the static system prompt first, an assistant message carrying its tool calls,
// and each tool result tied back to the call id that asked for it. Getting any of these
// wrong is a 400 from OpenAI on every turn that used a tool.
func TestThreadToParamsShape(t *testing.T) {
	params := threadToParams([]model.ConversationMessage{
		{Role: "user", Content: "give me a pasta recipe"},
		{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "call_1", Name: "generate_recipe", Args: `{"description":"pasta"}`}}},
		{Role: "tool", Content: `{"draft_ref":"draft_1"}`, ToolCallID: "call_1"},
		{Role: "assistant", Content: "Here you go."},
	}, "", "")
	if len(params) != 5 {
		t.Fatalf("expected system + 4 messages, got %d", len(params))
	}
	if params[0].OfSystem == nil {
		t.Error("first message is not the system prompt")
	}
	if params[1].OfUser == nil {
		t.Error("second message is not the user's")
	}
	assistant := params[2].OfAssistant
	if assistant == nil || len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls lost: %+v", params[2])
	}
	if assistant.ToolCalls[0].ID != "call_1" || assistant.ToolCalls[0].Function.Name != "generate_recipe" {
		t.Errorf("tool call mangled: %+v", assistant.ToolCalls[0])
	}
	if params[3].OfTool == nil || params[3].OfTool.ToolCallID != "call_1" {
		t.Errorf("tool result not tied to its call: %+v", params[3])
	}
}

// TestTrimThreadDropsOrphanedToolResults guards the one way history truncation can break
// a turn: the API rejects a tool message whose requesting assistant message is no longer
// in the list, so the cut has to take the orphans with it.
func TestTrimThreadDropsOrphanedToolResults(t *testing.T) {
	long := make([]model.ConversationMessage, 0, maxThreadMessages+4)
	long = append(long,
		model.ConversationMessage{Role: "user", Content: "first"},
		model.ConversationMessage{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "call_1"}}},
	)
	// Enough tool results that the cut lands squarely inside them.
	for range maxThreadMessages + 2 {
		long = append(long, model.ConversationMessage{Role: "tool", Content: "{}", ToolCallID: "call_1"})
	}
	long = append(long, model.ConversationMessage{Role: "user", Content: "last"})

	trimmed := trimThread(long)
	if len(trimmed) > maxThreadMessages {
		t.Errorf("thread not capped: %d messages", len(trimmed))
	}
	if len(trimmed) == 0 || trimmed[0].Role == "tool" {
		t.Fatalf("thread still starts with an orphaned tool result: %+v", trimmed)
	}
	if trimmed[len(trimmed)-1].Content != "last" {
		t.Error("the most recent message was trimmed away")
	}
}

// TestDecodeInlineImage covers the /chat upload channel's trust boundary: the bytes it
// returns are handed to the model as an image part, so anything that isn't an image has
// to be refused here rather than one layer down (BACKLOG.md 3.6 step 1).
func TestDecodeInlineImage(t *testing.T) {
	// A 1x1 GIF — the smallest thing http.DetectContentType calls an image.
	gif := "R0lGODlhAQABAAAAACH5BAEKAAEALAAAAAABAAEAAAICTAEAOw=="

	for _, tc := range []struct {
		name  string
		in    string
		wantN int // 0 means "must be rejected"
	}{
		{"data url", "data:image/gif;base64," + gif, 37},
		{"bare base64", gif, 37},
		{"not base64", "data:image/gif;base64,????", 0},
		{"not an image", base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\nrm -rf /")), 0},
		{"malformed data url", "data:image/gif;base64" + gif, 0},
		{"empty", "", 0},
	} {
		got, err := DecodeInlineImage(tc.in)
		switch {
		case tc.wantN == 0 && err == nil:
			t.Errorf("%s: accepted %d bytes, want a rejection", tc.name, len(got))
		case tc.wantN != 0 && err != nil:
			t.Errorf("%s: rejected a valid image: %v", tc.name, err)
		case tc.wantN != 0 && len(got) != tc.wantN:
			t.Errorf("%s: decoded %d bytes, want %d", tc.name, len(got), tc.wantN)
		}
	}
}

// TestTrimThreadKeepsShortThreadsIntact — the cap must not touch an ordinary thread.
func TestTrimThreadKeepsShortThreadsIntact(t *testing.T) {
	msgs := []model.ConversationMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}
	if got := trimThread(msgs); len(got) != 2 {
		t.Errorf("expected 2 messages, got %d", len(got))
	}
}

// TestEmitterTransports — an `Accept: application/json` caller (React Native, whose fetch
// has no readable body) must get the same events as an SSE caller, buffered into one body.
func TestEmitterTransports(t *testing.T) {
	for _, tc := range []struct {
		accept string
		want   string
	}{
		{"application/json", `{"events":[{"payload":{"text":"hi"},"type":"token"},{"payload":{"conversation_id":"c1"},"type":"done"}]}`},
		{"text/event-stream", "event: token\ndata: {\"text\":\"hi\"}\n\nevent: done\ndata: {\"conversation_id\":\"c1\"}\n\n"},
	} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/chat", nil)
		r.Header.Set("Accept", tc.accept)
		send, finish, err := newEmitter(rec, r)
		if err != nil {
			t.Fatalf("%s: %v", tc.accept, err)
		}
		send("token", map[string]string{"text": "hi"})
		send("done", map[string]string{"conversation_id": "c1"})
		finish()
		if got := strings.TrimSpace(rec.Body.String()); got != strings.TrimSpace(tc.want) {
			t.Errorf("%s:\n got %q\nwant %q", tc.accept, got, tc.want)
		}
	}
}

// TestThreadToParamsLanguage checks the caller's device locale reaches the model as part
// of the system prompt and nowhere else. It is the only locale signal left since
// /generate's `language` form field went away with the route (BACKLOG.md 3.9), so if this
// stops being appended, a short prompt like "cacio e pepe" silently loses its fallback.
func TestThreadToParamsLanguage(t *testing.T) {
	msgs := []model.ConversationMessage{{Role: "user", Content: "cacio e pepe"}}

	plain := threadToParams(msgs, "", "")
	if got := plain[0].OfSystem.Content.OfString.Value; strings.Contains(got, "device language") {
		t.Error("no language sent, but the system prompt mentions one")
	}

	withLang := threadToParams(msgs, "de", "")
	sys := withLang[0].OfSystem.Content.OfString.Value
	if !strings.Contains(sys, `device language is "de"`) {
		t.Errorf("system prompt does not carry the locale: %q", sys)
	}
	if user := withLang[1].OfUser.Content.OfString.Value; user != "cacio e pepe" {
		t.Errorf("locale leaked into the user message: %q", user)
	}
}

// TestCompaction checks the two things thread compaction must not get wrong: the cut
// never leaves a tool result without the assistant message that requested it, and a
// compacted thread's summary survives both the next read and the within-turn trim —
// losing it is the silent forgetting compaction exists to remove (BACKLOG.md 2.6).
func TestCompaction(t *testing.T) {
	// An assistant message followed by a long run of its tool results, so the cut lands
	// squarely inside them.
	msgs := []model.ConversationMessage{{Role: "user", Content: "first"}}
	for range maxThreadMessages {
		msgs = append(msgs, model.ConversationMessage{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "call_1"}}})
	}
	for range compactKeep + 2 {
		msgs = append(msgs, model.ConversationMessage{Role: "tool", Content: "{}", ToolCallID: "call_1"})
	}
	msgs = append(msgs, model.ConversationMessage{Role: "user", Content: "last"})

	cut := compactCut(msgs)
	if cut == 0 {
		t.Fatal("an over-long thread was not compacted")
	}
	if msgs[cut].Role == "tool" {
		t.Errorf("cut at %d leaves an orphaned tool result", cut)
	}
	if compactCut(msgs[:maxThreadMessages]) != 0 {
		t.Error("a thread within the cap was compacted anyway")
	}

	// The stored thread keeps the compacted span; a later read must resume at the summary.
	summary := model.ConversationMessage{Role: "system", Content: "Earlier: a pasta recipe."}
	stored := append(append([]model.ConversationMessage{}, msgs...), summary)
	stored = append(stored, model.ConversationMessage{Role: "user", Content: "and the sauce?"})
	resumed := sinceCompaction(stored)
	if len(resumed) != 2 || resumed[0].Content != summary.Content {
		t.Fatalf("read did not resume at the summary: %d messages", len(resumed))
	}

	// The backstop cut keeps the summary at the head and the newest message at the tail.
	long := append([]model.ConversationMessage{summary}, msgs...)
	trimmed := trimThread(long)
	if len(trimmed) > maxThreadMessages {
		t.Errorf("thread not capped: %d messages", len(trimmed))
	}
	if trimmed[0].Content != summary.Content {
		t.Errorf("summary cut from the head: %+v", trimmed[0])
	}
	if trimmed[len(trimmed)-1].Content != "last" {
		t.Errorf("the most recent message was trimmed away: %+v", trimmed[len(trimmed)-1])
	}
	if renderThread(msgs[:cut]) == "" {
		t.Error("the summariser was handed an empty thread")
	}
}

// An abandoned turn and a slow one both surface as a context error, and the funnel
// (BACKLOG.md 9.6) is only worth reading if it tells them apart.
func TestTurnOutcome(t *testing.T) {
	ok := context.Background()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()

	cases := []struct {
		name string
		ctx  context.Context
		code string
		want string
	}{
		{"clean turn", ok, "", "ok"},
		{"error code passes through", ok, "budget_exceeded", "budget_exceeded"},
		{"client hung up", canceled, "timeout", "abandoned"},
		{"client hung up after a clean turn", canceled, "", "abandoned"},
		{"deadline is a real timeout, not an abandon", expired, "timeout", "timeout"},
	}
	for _, c := range cases {
		if got := turnOutcome(c.ctx, c.code); got != c.want {
			t.Errorf("%s: turnOutcome(_, %q) = %q, want %q", c.name, c.code, got, c.want)
		}
	}
}

// Stop (BACKLOG.md 10.1) and the turn deadline are the same cancellation to the loop, and
// they mean opposite things to the user — one is "I changed my mind", the other "we were
// too slow".
func TestHaltCode(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := haltCode(canceled); got != "stopped" {
		t.Errorf("cancelled: haltCode = %q, want %q", got, "stopped")
	}

	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if got := haltCode(expired); got != "timeout" {
		t.Errorf("deadline: haltCode = %q, want %q", got, "timeout")
	}
}

// The approval gate (BACKLOG.md 10.2). Three properties, and the third is the one that
// breaks the thread if it is wrong: every tool call the parked reply asked for must come
// back with exactly one tool message, or the next model call is rejected outright.
func TestApprovalGate(t *testing.T) {
	call := func(id, name, args string) openai.ChatCompletionMessageToolCall {
		c := openai.ChatCompletionMessageToolCall{ID: id}
		c.Function.Name, c.Function.Arguments = name, args
		return c
	}

	t.Run("gates on scope, not on writing", func(t *testing.T) {
		cases := []struct {
			calls []openai.ChatCompletionMessageToolCall
			want  int
		}{
			{[]openai.ChatCompletionMessageToolCall{call("a", "generate_recipe", "{}")}, -1},
			// A single row the user named is its own confirmation — neither asks.
			{[]openai.ChatCompletionMessageToolCall{call("a", "save_recipe", "{}"), call("b", "set_plan_day", "{}")}, -1},
			{[]openai.ChatCompletionMessageToolCall{call("a", "apply_week_plan", "{}")}, 0},
			// The first one found, so the calls after it never run against a state the
			// user has not agreed to.
			{[]openai.ChatCompletionMessageToolCall{call("a", "get_recipe", "{}"), call("b", "clear_plan_day", "{}"), call("c", "set_preferences", "{}")}, 1},
		}
		for _, tc := range cases {
			if got := firstMutating(tc.calls); got != tc.want {
				t.Errorf("firstMutating(%v) = %d, want %d", tc.calls, got, tc.want)
			}
		}
	})

	t.Run("approval is the confirmation", func(t *testing.T) {
		got := approvedArgs(call("a", "apply_week_plan", `{"days":[{"date":"2026-09-20"}]}`))
		var args map[string]any
		if err := json.Unmarshal([]byte(got), &args); err != nil {
			t.Fatalf("approvedArgs produced unparseable JSON: %v", err)
		}
		if args["confirm"] != true {
			t.Errorf("approvedArgs did not set confirm: %s", got)
		}
		if args["days"] == nil {
			t.Errorf("approvedArgs dropped the arguments: %s", got)
		}
		// Every other tool is passed through untouched — confirm means nothing to them.
		if got := approvedArgs(call("a", "clear_plan_day", `{"date":"x"}`)); got != `{"date":"x"}` {
			t.Errorf("approvedArgs rewrote a non-plan call: %s", got)
		}
	})

	// Declining is the case that runs no tool at all, so it is the one where a missing
	// tool message would go unnoticed until the *next* turn failed.
	t.Run("declining answers every call and writes nothing", func(t *testing.T) {
		calls := []openai.ChatCompletionMessageToolCall{
			call("c1", "clear_plan_day", `{"date":"2026-09-20"}`),
			call("c2", "get_recipe", `{"id":1}`),
		}
		session := &chatSession{}
		pending := &pendingApproval{id: "p1", assistant: model.ConversationMessage{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "c1"}, {ID: "c2"}}}, calls: calls, index: 0}
		resumeApproval(context.Background(), func(string, any) {}, session, pending, false)

		if len(session.messages) != 3 {
			t.Fatalf("got %d messages, want the assistant reply plus one tool result per call", len(session.messages))
		}
		for i, want := range []string{"c1", "c2"} {
			if msg := session.messages[i+1]; msg.Role != "tool" || msg.ToolCallID != want {
				t.Errorf("message %d = %+v, want a tool result for %s", i+1, msg, want)
			}
		}
		if !strings.Contains(session.messages[1].Content, "did not approve") {
			t.Errorf("declined call: %s", session.messages[1].Content)
		}
		// The call *after* the gated one is reported un-run rather than executed: the
		// model wrote it expecting the gated write to have happened.
		if !strings.Contains(session.messages[2].Content, "not run") {
			t.Errorf("trailing call: %s", session.messages[2].Content)
		}
	})
}
