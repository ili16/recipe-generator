package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/billing"
	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/ratelimit"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/ili16/recipe-generator/internal/utils"
	"github.com/openai/openai-go"
)

// The agent loop behind POST /api/v1/chat (specs/AGENT_CHAT.md §2): send the thread plus
// the tool catalog, run whatever tools the model asks for, append the results, repeat
// until it answers in prose. Bounded three ways — a hard iteration cap, a wall-clock
// deadline derived from the request context, and a budget re-check between iterations.

const (
	// maxTurnIterations caps how many model calls one user turn may make. Generate →
	// look at the result → answer is three; anything past this is a loop, not a plan.
	maxTurnIterations = 6

	// turnDeadline bounds a whole turn in wall-clock time. Generation alone can take
	// tens of seconds, and a turn may run several of them.
	turnDeadline = 4 * time.Minute

	// maxThreadMessages caps how much history is replayed to the model. The point of
	// server-held threads is that this policy is ours to change without a client
	// release; verbatim-last-N is the version worth having until threads get long
	// enough to need summarisation (specs/AGENT_CHAT.md §2.1).
	maxThreadMessages = 40

	maxChatMessageChars = 2000

	// maxChatBodyBytes bounds the request body. It is generous because an image
	// attachment arrives inline as base64 (~4/3 of the file), and modest because the
	// whole thing is held in memory for the turn.
	maxChatBodyBytes = 12 << 20

	// maxChatImages caps how many photos one turn may carry — the model reads them one
	// at a time via generate_recipe, and a burst of them is a bill, not a recipe.
	maxChatImages = 3

	// anonThreadTTL is how long an anonymous thread — and every session's recipe drafts
	// — survive without use. Drafts are unsaved by definition; nothing is lost by
	// letting a cold one go.
	anonThreadTTL = 2 * time.Hour
)

// chatSession is one conversation as the loop sees it: who is asking, the thread so far,
// and the unsaved drafts produced in it. userID is 0 for an anonymous caller.
type chatSession struct {
	id        string
	userID    int
	messages  []model.ConversationMessage
	drafts    map[string]model.RecipeDocument
	draftInfo map[string]draftInfo
	nextDraft int
	// images are the photos attached to *this* turn, keyed by the image_ref named in
	// the user message. Turn-scoped on purpose: once the model has read one it holds a
	// draft, and a later turn works from the draft_ref, not the pixels.
	images map[string][]byte
	// language is the caller's device locale (ISO 639-1), turn-scoped like images and
	// never stored on the thread: it describes the client, not the conversation, and a
	// thread resumed from another device should follow that device. Empty when the
	// client sends none.
	language string
}

// draftInfo is what a draft carries besides its document: the saved recipe it is a
// variant of, if any, and the recipe id it was saved as, if it already has been. The
// second is what makes saving — and so applying a plan — idempotent: asking twice
// returns the same recipe instead of creating a duplicate (BACKLOG.md 4.3).
type draftInfo struct {
	variantOf int
	savedID   int
	// origin is what the user handed over to produce this draft (a URL, a photo, a
	// description). Recorded on the recipe at save time so the library can say where
	// it came from (BACKLOG.md 3.7).
	origin *model.GenerationOrigin
}

// sessionState is the in-memory side of a conversation. Drafts live here for every
// caller (they are ephemeral by nature — an unsaved document, valid only while the
// conversation is warm); for anonymous callers the messages live here too, since there
// is no user to hang a conversations row off.
//
// ponytail: single-process map with a sweep, exactly like internal/ratelimit — if this
// backend is ever run on more than one instance, both need a shared store together.
type sessionState struct {
	messages  []model.ConversationMessage
	drafts    map[string]model.RecipeDocument
	draftInfo map[string]draftInfo
	nextDraft int
	seen      time.Time
}

var sessions = struct {
	sync.Mutex
	byKey map[string]*sessionState
}{byKey: map[string]*sessionState{}}

// sessionKey namespaces in-memory state per caller, so an anonymous conversation id
// guessed from another IP resolves to a different (empty) thread. Anonymous callers are
// keyed by IP exactly as the rate limiter keys them.
func sessionKey(r *http.Request, userID int, conversationID string) string {
	if userID != 0 {
		return "user:" + strings.TrimSpace(conversationID)
	}
	return "ip:" + ratelimit.ClientIP(r) + "|" + strings.TrimSpace(conversationID)
}

func loadSessionState(key string) *sessionState {
	sessions.Lock()
	defer sessions.Unlock()
	st, ok := sessions.byKey[key]
	if !ok {
		st = &sessionState{drafts: map[string]model.RecipeDocument{}, draftInfo: map[string]draftInfo{}}
		sessions.byKey[key] = st
	}
	st.seen = time.Now()
	return st
}

// snapshotDrafts copies the stored drafts so the turn mutates its own map. Two turns on
// one conversation at once is rare but must not be a concurrent map write — the copy
// makes it last-writer-wins instead of a panic.
func snapshotDrafts(st *sessionState) (map[string]model.RecipeDocument, map[string]draftInfo) {
	sessions.Lock()
	defer sessions.Unlock()
	out := make(map[string]model.RecipeDocument, len(st.drafts))
	for k, v := range st.drafts {
		out[k] = v
	}
	info := make(map[string]draftInfo, len(st.draftInfo))
	for k, v := range st.draftInfo {
		info[k] = v
	}
	return out, info
}

// sweepSessions evicts state untouched for longer than anonThreadTTL.
func sweepSessions() {
	for range time.Tick(anonThreadTTL) {
		cutoff := time.Now().Add(-anonThreadTTL)
		sessions.Lock()
		for k, st := range sessions.byKey {
			if st.seen.Before(cutoff) {
				delete(sessions.byKey, k)
			}
		}
		sessions.Unlock()
	}
}

func init() { go sweepSessions() }

// newConversationID mints an id for an anonymous thread, which has no DB row to get one
// from. Same shape as the UUIDs Postgres generates for signed-in threads.
func newConversationID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// HandleChat is the one conversational endpoint: one user message in, a stream of
// tokens, tool events and artifacts out. Wired behind the same
// authenticator.Optional(rl.Guard(billing.Guard(…))) stack as every other LLM route.
func HandleChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ConversationID string `json:"conversation_id"`
		Message        string `json:"message"`
		// Language is the caller's device locale (ISO 639-1). Optional; it only breaks
		// the tie when the message itself carries no clear language signal — a short
		// foreign dish name like "cacio e pepe" should not hijack the output language.
		Language string `json:"language"`
		Attachments    []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
			// Data carries an image inline, either as a data: URL or bare base64.
			Data string `json:"data"`
		} `json:"attachments"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Message) > maxChatMessageChars {
		req.Message = req.Message[:maxChatMessageChars]
	}

	images := map[string][]byte{}
	for _, a := range req.Attachments {
		switch {
		// URL attachments are just context for the model, which decides whether to
		// extract a recipe from them or merely read them.
		case a.Type == "url" && a.URL != "":
			req.Message += "\n\n[attached link] " + a.URL
		case a.Type == "image" && a.Data != "":
			if len(images) >= maxChatImages {
				continue
			}
			data, err := decodeInlineImage(a.Data)
			if err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			ref := "image" + strconv.Itoa(len(images)+1)
			images[ref] = data
			req.Message += "\n\n[attached photo] image_ref=" + ref
		}
	}

	emit, finish, err := newEmitter(w, r)
	if err != nil {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	defer finish()

	// Deadline and cancellation both come off the request context, so a client that
	// hangs up stops the upstream OpenAI calls with it.
	ctx, cancel := context.WithTimeout(r.Context(), turnDeadline)
	defer cancel()

	session, state, err := openSession(ctx, r, req.ConversationID)
	if err != nil {
		emit("error", map[string]string{"code": "conversation_not_found"})
		return
	}
	session.images = images
	session.language = req.Language
	session.messages = append(session.messages, model.ConversationMessage{Role: "user", Content: req.Message})

	produced, code := runTurn(ctx, emit, session)

	// Persist whatever the turn produced even when it ended on an error — a half-done
	// turn the user can see must be a half-done turn the next turn can read.
	persistTurn(ctx, session, state, produced)

	if code != "" {
		emit("error", map[string]string{"code": code})
		return
	}
	emit("done", map[string]string{"conversation_id": session.id})
}

// emit sends one client event. The turn never knows which transport it is on.
type emit func(event string, payload any)

// newEmitter picks the transport. SSE is the default; a client that asks for
// `Accept: application/json` — React Native, whose fetch has no readable body
// (BACKLOG.md 3.8) — gets the same events buffered into one JSON array at the end,
// written by the returned finish func.
func newEmitter(w http.ResponseWriter, r *http.Request) (emit, func(), error) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		var events []map[string]any
		return func(event string, payload any) {
				events = append(events, map[string]any{"type": event, "payload": payload})
			}, func() {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"events": events})
			}, nil
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, nil, errors.New("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return func(event string, payload any) { utils.WriteSSE(w, flusher, event, payload) }, func() {}, nil
}

// decodeInlineImage turns an attachment's `data` into image bytes. It accepts a data:
// URL or bare base64, and rejects anything that doesn't sniff as an image — this is a
// trust boundary, and the bytes go straight to the model as an image part.
func decodeInlineImage(s string) ([]byte, error) {
	if strings.HasPrefix(s, "data:") {
		_, after, ok := strings.Cut(s, ",")
		if !ok {
			return nil, errors.New("malformed data url")
		}
		s = after
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("attachment is not valid base64")
	}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, errors.New("attachment is not an image")
	}
	return data, nil
}

// openSession resolves (or opens) the thread this turn belongs to. A signed-in caller
// gets a row in conversations; an anonymous one gets in-memory state keyed by IP.
func openSession(ctx context.Context, r *http.Request, conversationID string) (*chatSession, *sessionState, error) {
	userID := 0
	if u, ok := auth.FromContext(r.Context()); ok {
		userID = u.ID
	}

	session := &chatSession{id: strings.TrimSpace(conversationID), userID: userID}
	if userID != 0 {
		if session.id == "" {
			id, err := storage.CreateConversation(ctx, userID, "")
			if err != nil {
				return nil, nil, err
			}
			session.id = id
		} else {
			msgs, err := storage.ConversationMessages(ctx, userID, session.id)
			if err != nil {
				return nil, nil, err
			}
			session.messages = msgs
		}
	} else if session.id == "" {
		session.id = newConversationID()
	}

	state := loadSessionState(sessionKey(r, userID, session.id))
	session.drafts, session.draftInfo = snapshotDrafts(state)
	session.nextDraft = state.nextDraft
	if userID == 0 {
		session.messages = state.messages
	}
	return session, state, nil
}

// persistTurn appends the turn's new messages to wherever this thread lives.
func persistTurn(ctx context.Context, session *chatSession, state *sessionState, produced int) {
	sessions.Lock()
	state.drafts, state.draftInfo, state.nextDraft = session.drafts, session.draftInfo, session.nextDraft
	if session.userID == 0 {
		state.messages = session.messages
	}
	sessions.Unlock()
	if session.userID == 0 {
		return
	}
	// produced counts the messages the loop added on top of the user's; +1 for it.
	added := session.messages[len(session.messages)-(produced+1):]
	// Detached from the request context: a client that hung up mid-turn still paid for
	// the tokens, so the thread must still record them.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := storage.AppendConversationMessages(writeCtx, session.userID, session.id, added); err != nil {
		slog.Error("chat: persist thread failed", "error", err, "conversation_id", session.id)
	}
}

// runTurn is the loop itself. It returns how many messages it appended to the session
// and an error code for the client ("" when the turn ended normally) — a code, never
// err.Error(), so wrapped upstream text can't reach the wire (specs/AGENT_CHAT.md §4).
func runTurn(ctx context.Context, send emit, session *chatSession) (int, string) {
	before := len(session.messages)
	produced := func() int { return len(session.messages) - before }

	for i := range maxTurnIterations {
		// An agent turn is N model calls, so the budget has to be re-read between
		// iterations: a runaway loop must trip the cap mid-turn, not after the whole
		// turn has been paid for (BACKLOG.md 2.4). The tool calls inside the previous
		// iteration record their own cost, so re-reading spend counts those too.
		if i > 0 && overBudget(ctx, session.userID) {
			return produced(), "budget_exceeded"
		}

		msg, _, err := llm.CallMessage(ctx, llm.Spec{
			Model:    recipeModel,
			CallType: "chat",
			Messages: threadToParams(session.messages, session.language),
			Tools:    toolDefinitions(),
			OnDelta:  func(delta string) { send("token", map[string]string{"text": delta}) },
		})
		if err != nil {
			if ctx.Err() != nil {
				return produced(), "timeout"
			}
			slog.ErrorContext(ctx, "chat: model call failed", "error", err)
			return produced(), "model_error"
		}

		// An assistant message with neither prose nor tool calls is not a turn — and
		// storing one would make every later turn replay a message the API rejects.
		if msg.Content != "" || len(msg.ToolCalls) > 0 {
			session.messages = append(session.messages, fromOpenAIMessage(msg))
		}
		if len(msg.ToolCalls) == 0 {
			return produced(), ""
		}

		for _, call := range msg.ToolCalls {
			session.messages = append(session.messages, runToolCall(ctx, send, session, call))
		}
	}
	// Out of iterations with the model still asking for tools: surface it as a code
	// rather than silently truncating a turn the user thinks completed.
	return produced(), "iteration_limit"
}

// runToolCall executes one tool call, emits its client events, and returns the tool
// message to append to the thread. A tool failure is reported to the model as JSON so it
// can recover within the same turn.
func runToolCall(ctx context.Context, send emit, session *chatSession, call openai.ChatCompletionMessageToolCall) model.ConversationMessage {
	name := call.Function.Name
	send("tool_start", map[string]string{"name": name, "args": call.Function.Arguments})

	result, art, err := dispatch(ctx, session, name, call.Function.Arguments)
	if err != nil {
		slog.WarnContext(ctx, "chat: tool failed", "tool", name, "error", err)
		result = map[string]string{"error": err.Error()}
	}
	payload, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		payload = []byte(`{"error":"the tool returned something unreadable"}`)
	}

	send("tool_end", map[string]any{"name": name, "ok": err == nil})
	if art != nil {
		send("artifact", art)
	}
	return model.ConversationMessage{Role: "tool", Content: string(payload), ToolCallID: call.ID}
}

// overBudget reports whether the caller has reached their monthly cap. Anonymous callers
// have no cap (nothing is tracked for them — the rate limiter is their only guard), and
// a DB error fails open, matching billing.Guard.
func overBudget(ctx context.Context, userID int) bool {
	if userID == 0 {
		return false
	}
	spent, cap, err := billing.CurrentUsage(ctx, userID)
	if err != nil {
		return false
	}
	return spent >= cap
}

// fromOpenAIMessage converts an assistant reply into the stored thread shape.
func fromOpenAIMessage(msg openai.ChatCompletionMessage) model.ConversationMessage {
	out := model.ConversationMessage{Role: "assistant", Content: msg.Content}
	for _, c := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, model.ToolCall{ID: c.ID, Name: c.Function.Name, Args: c.Function.Arguments})
	}
	return out
}

// threadToParams renders the stored thread as OpenAI messages, system prompt first,
// capped to the most recent maxThreadMessages. language (the caller's device locale, if
// sent) is appended to the system prompt rather than the thread, so it follows the
// device making the request instead of being frozen into the conversation.
func threadToParams(msgs []model.ConversationMessage, language string) []openai.ChatCompletionMessageParamUnion {
	msgs = trimThread(msgs)
	system := llm.ChatAgentSystemPrompt
	if language != "" {
		system += "- The user's device language is \"" + language + "\" (ISO 639-1). Write in it, and pass it as generate_recipe's `language`, unless the user's own message is clearly in another language.\n"
	}
	params := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs)+1)
	params = append(params, openai.SystemMessage(system))
	for _, m := range msgs {
		switch m.Role {
		case "tool":
			params = append(params, openai.ToolMessage(m.Content, m.ToolCallID))
		case "assistant":
			assistant := openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				assistant.Content.OfString = openai.String(m.Content)
			}
			for _, c := range m.ToolCalls {
				assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallParam{
					ID:       c.ID,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{Name: c.Name, Arguments: c.Args},
				})
			}
			params = append(params, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
		default:
			params = append(params, openai.UserMessage(m.Content))
		}
	}
	return params
}

// trimThread keeps the last maxThreadMessages, then drops any leading tool results whose
// requesting assistant message fell off the front — the API rejects an orphan tool
// message outright.
func trimThread(msgs []model.ConversationMessage) []model.ConversationMessage {
	if len(msgs) > maxThreadMessages {
		msgs = msgs[len(msgs)-maxThreadMessages:]
	}
	for len(msgs) > 0 && msgs[0].Role == "tool" {
		msgs = msgs[1:]
	}
	return msgs
}
