package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/ili16/recipe-generator/internal/telemetry"
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

	// maxThreadMessages caps how much history is replayed to the model, and is the
	// point at which a thread is compacted (specs/AGENT_CHAT.md §2.1).
	maxThreadMessages = 40

	// compactKeep is how many recent messages survive compaction verbatim; everything
	// older is summarised into the one system message at the head. Well under
	// maxThreadMessages so a compacted thread has room to grow before it compacts
	// again — keeping 39 of 40 would mean an LLM call on every turn.
	compactKeep = 20

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
	// household is the caller's household name and member count when they are in one
	// (BACKLOG 15.1), rendered into the system prompt so the assistant knows it is
	// cooking for a table rather than a person. Empty for everyone else, including every
	// anonymous caller. One line, not a profile dump — the aggregated household memory
	// file is 15.6 and is deliberately not this.
	household string
	// fresh marks a thread whose conversations row was opened by *this* request, and
	// is the whole trigger for naming it (BACKLOG.md 10.4): a title is derived once,
	// from the first user message, and never recomputed.
	fresh bool
	// pending is runTurn's way out: set when the turn stopped on a mutating call the
	// user has to approve (BACKLOG.md 10.2), and written back to the session state by
	// the handler. Nil on every turn that ran to completion.
	pending *pendingApproval
	// endTurn is set by a tool that hands the move to the user (offer_options): the loop
	// stops after its result instead of paying a model round to comment on the chips.
	endTurn bool
	// intent is the router's verdict for this turn; intentAgent runs the full loop.
	intent intent
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
	// derivedFrom is the draft_ref or `recipe:<id>` this document was made out of, so
	// display_recipe can tell the client which card this one replaces (BACKLOG 10.3).
	derivedFrom string
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
	// pending is the mutating call this thread is waiting on the user to approve
	// (BACKLOG.md 10.2). At most one: the turn stops at the first one, so a reply
	// asking for three writes queues the first and reports the rest un-run.
	pending *pendingApproval
}

// pendingApproval is a turn parked mid-reply, waiting for Apply or Change it.
//
// The assistant message is held here rather than appended to the thread, because a stored
// assistant message whose tool_calls have no matching tool results is a thread the model
// API refuses. Approving appends the assistant message and every tool result together, so
// the thread is only ever valid; abandoning the approval drops the reply entirely, which
// is also why the model's prose for it is re-generated rather than resumed.
type pendingApproval struct {
	// id is what the client echoes back, so a stale card cannot approve a newer call.
	id string
	// assistant is the reply that asked for the call, tool_calls included.
	assistant model.ConversationMessage
	// calls is that reply's tool calls; index marks the one awaiting approval.
	calls []openai.ChatCompletionMessageToolCall
	index int
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
		// Approval answers an event: approval_request from the previous turn on this
		// thread (BACKLOG.md 10.2). It arrives *instead* of a message: the decision is
		// the whole turn, and the parked tool call resumes the loop from it.
		Approval *struct {
			ID      string `json:"id"`
			Approve bool   `json:"approve"`
		} `json:"approval"`
		Attachments []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
			// Data carries an image inline, either as a data: URL or bare base64.
			Data string `json:"data"`
		} `json:"attachments"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		(strings.TrimSpace(req.Message) == "" && req.Approval == nil) {
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
			data, err := DecodeInlineImage(a.Data)
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

	// An approval decision resumes the parked reply instead of starting a turn: no new
	// user message, no compaction (the thread has not grown since it was compacted for
	// the turn that parked), and the pending call is consumed whichever way it went.
	if req.Approval != nil {
		pending := takePending(state, req.Approval.ID)
		if pending == nil {
			emit("error", map[string]string{"code": "approval_expired"})
			return
		}
		mark := len(session.messages)
		resumeApproval(ctx, emit, session, pending, req.Approval.Approve)
		_, code := runTurn(ctx, emit, session)
		telemetry.RecordChatTurn(ctx, turnOutcome(ctx, code))
		storePending(state, session.pending)
		persistTurn(ctx, session, state, mark)
		if code != "" {
			emit("error", map[string]string{"code": code})
			return
		}
		emit("done", map[string]string{"conversation_id": session.id})
		return
	}

	// Compact before the turn, not during it: the summary has to be written to the
	// thread ahead of this turn's messages to keep the stored order readable, and the
	// loop below then runs against a thread that already fits.
	if compacted, summary := compactThread(ctx, session.messages); summary != nil {
		session.messages = compacted
		if session.userID != 0 {
			if err := storage.AppendConversationMessages(ctx, session.userID, session.id, []model.ConversationMessage{*summary}); err != nil {
				slog.ErrorContext(ctx, "chat: persist thread summary failed", "error", err, "conversation_id", session.id)
			}
		}
	}
	mark := len(session.messages)
	session.messages = append(session.messages, model.ConversationMessage{Role: "user", Content: req.Message})

	_, code := runTurn(ctx, emit, session)
	telemetry.RecordChatTurn(ctx, turnOutcome(ctx, code))
	// A reply parked on an approval is the one thing that outlives the turn; anything
	// already stored is stale the moment a new turn runs.
	storePending(state, session.pending)

	// Persist whatever the turn produced even when it ended on an error — a half-done
	// turn the user can see must be a half-done turn the next turn can read.
	persistTurn(ctx, session, state, mark)

	if session.fresh {
		go nameThread(ctx, session.userID, session.id, req.Message)
	}

	if code != "" {
		emit("error", map[string]string{"code": code})
		return
	}
	emit("done", map[string]string{"conversation_id": session.id})
}

// turnOutcome labels a finished turn for the funnel counter. A client that hangs up
// mid-stream cancels the request context, which runTurn reports as "timeout" — but a
// user who walked away and a backend that was too slow are different problems, so they
// are separated here by *which* context error fired (Canceled vs DeadlineExceeded).
func turnOutcome(ctx context.Context, code string) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "abandoned"
	}
	if code == "" {
		return "ok"
	}
	return code
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

// DecodeInlineImage turns an attachment's `data` into image bytes. It accepts a data:
// URL or bare base64, and rejects anything that doesn't sniff as an image — this is a
// trust boundary, and the bytes go straight to the model as an image part.
//
// Exported because /feedback's screenshot (BACKLOG 9.16) is the same trust boundary with
// the same two failure modes. One decoder, so a fix to the sniffing applies to both.
func DecodeInlineImage(s string) ([]byte, error) {
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
			session.id, session.fresh = id, true
		} else {
			msgs, err := storage.ConversationMessages(ctx, userID, session.id)
			if err != nil {
				return nil, nil, err
			}
			session.messages = sinceCompaction(msgs)
		}
	} else if session.id == "" {
		session.id = newConversationID()
	}

	if userID != 0 {
		if h, err := storage.GetHousehold(ctx, userID); err == nil {
			session.household = fmt.Sprintf("%s (%d people)", h.Name, len(h.Members))
		}
	}

	state := loadSessionState(sessionKey(r, userID, session.id))
	session.drafts, session.draftInfo = snapshotDrafts(state)
	session.nextDraft = state.nextDraft
	if userID == 0 {
		session.messages = state.messages
	} else {
		rehydrateDrafts(session)
	}
	return session, state, nil
}

// rehydrateDrafts puts the thread's recipe drafts back in reach after the in-memory
// session behind it has gone (a restart, or anonThreadTTL). The documents are on the
// stored artifacts, so a thread reopened tomorrow can still be transformed and still has
// a working Save button, instead of the model being told draft_7 does not exist.
//
// Drafts still held in memory win: they carry savedID, which the stored card may predate.
func rehydrateDrafts(session *chatSession) {
	for _, m := range session.messages {
		if len(m.Artifact) == 0 {
			continue
		}
		var art struct {
			Kind string `json:"kind"`
			Data struct {
				DraftRef string               `json:"draft_ref"`
				RecipeID int                  `json:"recipe_id"`
				Document model.RecipeDocument `json:"document"`
			} `json:"data"`
		}
		if json.Unmarshal(m.Artifact, &art) != nil || art.Kind != "recipe" || art.Data.DraftRef == "" {
			continue
		}
		ref := art.Data.DraftRef
		if _, held := session.drafts[ref]; held {
			continue
		}
		session.drafts[ref] = art.Data.Document
		session.draftInfo[ref] = draftInfo{savedID: art.Data.RecipeID}
		if n, err := strconv.Atoi(strings.TrimPrefix(ref, "draft_")); err == nil && n > session.nextDraft {
			session.nextDraft = n
		}
	}
}

// nameThread derives a thread's title from the message that opened it, once. Cheap tier,
// detached from the request: the turn it belongs to has already been answered, and a
// failed title is a thread listed by its preview rather than a failed turn.
func nameThread(ctx context.Context, userID int, conversationID, firstMessage string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()

	title, err := chatCompletion(ctx, llm.ThreadTitlePrompt, firstMessage, string(assistModel), "thread_title")
	title = strings.Trim(strings.TrimSpace(title), `"'`)
	if err != nil || title == "" {
		slog.WarnContext(ctx, "chat: naming thread failed", "error", err, "conversation_id", conversationID)
		return
	}
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80])
	}
	if err := storage.SetConversationTitle(ctx, userID, conversationID, title); err != nil {
		slog.ErrorContext(ctx, "chat: storing thread title failed", "error", err, "conversation_id", conversationID)
	}
}

// storeDrafts writes the session's drafts back to the shared state. Separate from
// persistTurn because a draft can now be saved outside a turn (HandleSaveChatDraft), and
// that write has to mark the draft saved or the next "save it" would duplicate it.
func storeDrafts(session *chatSession, state *sessionState) {
	sessions.Lock()
	defer sessions.Unlock()
	state.drafts, state.draftInfo, state.nextDraft = session.drafts, session.draftInfo, session.nextDraft
}

// HandleSaveChatDraft saves a recipe the agent drafted in this conversation, without
// asking the model to do it — the client's "Save to my recipes" button. It is the same
// saveDraft the save_recipe tool calls, so saving twice (button then "save it", or two
// taps) returns the one recipe rather than making a second.
func HandleSaveChatDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}

	var req struct {
		ConversationID string   `json:"conversation_id"`
		DraftRef       string   `json:"draft_ref"`
		Tags           []string `json:"tags"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil ||
		strings.TrimSpace(req.ConversationID) == "" || strings.TrimSpace(req.DraftRef) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	id, err := saveConversationDraft(r.Context(), sessionKey(r, user.ID, req.ConversationID), user.ID, req.DraftRef, req.Tags)
	if errors.Is(err, errDraftExpired) {
		writeJSONError(w, http.StatusNotFound, "draft_expired")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "save_failed")
		return
	}

	// The full row, not just the id: the client drops it straight into its recipe cache
	// so My Recipes shows it without a refetch.
	saved, err := storage.GetRecipeByID(r.Context(), user.ID, id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "save_failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(saved); err != nil {
		slog.Error("chat: encode saved draft failed", "error", err)
	}
}

// errDraftExpired is a draft the session no longer holds: swept after anonThreadTTL, or
// lost to a restart. Distinct from a failed write so the client can fall back to asking
// the assistant rather than offering a button that cannot work.
var errDraftExpired = errors.New("draft expired")

// saveConversationDraft is HandleSaveChatDraft without the HTTP: it takes the same
// in-memory half of openSession the turn does, saves through the same idempotent
// saveDraft the save_recipe tool calls, and writes the drafts back so the draft is marked
// saved — without that last step a later "save it" would write a second copy.
func saveConversationDraft(ctx context.Context, key string, userID int, ref string, tags []string) (int, error) {
	state := loadSessionState(key)
	session := &chatSession{userID: userID}
	session.drafts, session.draftInfo = snapshotDrafts(state)
	session.nextDraft = state.nextDraft

	if _, held := session.drafts[strings.TrimSpace(ref)]; !held {
		return 0, errDraftExpired
	}
	id, _, err := session.saveDraft(ctx, ref, tags)
	if err != nil {
		return 0, err
	}
	storeDrafts(session, state)
	return id, nil
}

func writeJSONError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code})
}

// persistTurn appends the turn's new messages to wherever this thread lives. mark is the
// thread length before the turn added anything — taken by the caller rather than counted
// here, because an approval resumes with several messages at once and an ordinary turn
// starts with exactly one.
func persistTurn(ctx context.Context, session *chatSession, state *sessionState, mark int) {
	storeDrafts(session, state)
	if session.userID == 0 {
		// An anonymous thread has no conversations row to append to: the session map is
		// where it lives, so storing the messages there is the whole of persisting it.
		sessions.Lock()
		state.messages = session.messages
		sessions.Unlock()
		return
	}
	added := session.messages[mark:]
	if len(added) == 0 {
		return
	}
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

	// Only a fresh user message is routed; an approval resume continues a turn whose
	// shape was already decided, so it gets the full loop.
	if n := len(session.messages); n > 0 && session.messages[n-1].Role == "user" {
		session.intent = routeIntent(ctx, session.messages)
	}
	tools := toolDefinitions(routeTools[session.intent]...)
	chatModel := routedModel(session.intent)
	steer := routeSteer(session.intent)

	for i := range maxTurnIterations {
		// A stop (BACKLOG.md 10.1) cancels the request context, and it has to land
		// here rather than inside the next model call: without this the loop would
		// start another iteration and run its tools against a dead context — writes
		// that half-fail instead of never happening.
		if ctx.Err() != nil {
			return produced(), haltCode(ctx)
		}

		// An agent turn is N model calls, so the budget has to be re-read between
		// iterations: a runaway loop must trip the cap mid-turn, not after the whole
		// turn has been paid for (BACKLOG.md 2.4). The tool calls inside the previous
		// iteration record their own cost, so re-reading spend counts those too.
		if i > 0 && overBudget(ctx, session.userID) {
			return produced(), "budget_exceeded"
		}

		msg, _, err := llm.CallMessage(ctx, llm.Spec{
			Model:    chatModel,
			CallType: "chat",
			Messages: threadToParams(session.messages, session.language, session.household, steer),
			Tools:    tools,
			OnDelta:  func(delta string) { send("token", map[string]string{"text": delta}) },
		})
		if err != nil {
			if ctx.Err() != nil {
				return produced(), haltCode(ctx)
			}
			slog.ErrorContext(ctx, "chat: model call failed", "error", err)
			return produced(), "model_error"
		}

		// A mutating call is gated on the user, not on the model's prose (BACKLOG.md
		// 10.2) — and the check comes *before* the assistant message is appended: a
		// stored reply whose tool_calls have no tool results is a thread the model API
		// refuses, so the whole reply is parked and appended on approval instead.
		// Anonymous callers are skipped: every mutating tool is NeedsAuth, so there is
		// nothing to approve — asking would only lead to a sign-in error after Apply.
		if at := firstMutating(msg.ToolCalls); at >= 0 && session.userID != 0 {
			call := msg.ToolCalls[at]
			session.pending = &pendingApproval{id: newConversationID(), assistant: fromOpenAIMessage(msg), calls: msg.ToolCalls, index: at}
			send("approval_request", map[string]any{
				"id":       session.pending.id,
				"name":     call.Function.Name,
				"args":     call.Function.Arguments,
				"replaces": approvalPreview(ctx, session, call.Function.Name, json.RawMessage(call.Function.Arguments)),
			})
			return produced(), ""
		}

		// An assistant message with neither prose nor tool calls is not a turn — and
		// storing one would make every later turn replay a message the API rejects.
		if msg.Content != "" || len(msg.ToolCalls) > 0 {
			session.messages = append(session.messages, fromOpenAIMessage(msg))
		}
		if len(msg.ToolCalls) == 0 {
			return produced(), ""
		}

		// Checked per call, not just per iteration: one model reply can ask for
		// several tools, and a stop during the first must not run the rest.
		for _, call := range msg.ToolCalls {
			if ctx.Err() != nil {
				return produced(), haltCode(ctx)
			}
			session.messages = append(session.messages, runToolCall(ctx, send, session, call))
		}
		if session.endTurn {
			return produced(), ""
		}
	}
	// Out of iterations with the model still asking for tools: surface it as a code
	// rather than silently truncating a turn the user thinks completed.
	return produced(), "iteration_limit"
}

// haltCode names why a cancelled turn stopped: the user pressed Stop (BACKLOG.md 10.1),
// or the turn ran past turnDeadline. Same cancellation, different thing to tell the user
// — and turnOutcome already separates the two for the funnel counter.
func haltCode(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "stopped"
}

// firstMutating is the index of the first call in this reply that needs the user's
// approval, or -1 when none does.
func firstMutating(calls []openai.ChatCompletionMessageToolCall) int {
	for i, c := range calls {
		if chatTools[c.Function.Name].Mutating {
			return i
		}
	}
	return -1
}

// resumeApproval turns the user's decision into the tool results the parked reply is
// missing, so the thread the loop continues from is a valid one: the assistant message,
// then exactly one tool message per tool call it asked for.
//
// Only the approved call runs. The calls after it are reported un-run rather than
// executed, because the model wrote them expecting the gated one to have already
// happened — it can ask again now that it knows the answer.
func resumeApproval(ctx context.Context, send emit, session *chatSession, p *pendingApproval, approved bool) {
	session.messages = append(session.messages, p.assistant)
	for i, call := range p.calls {
		switch {
		case i == p.index && approved:
			call.Function.Arguments = approvedArgs(call)
			session.messages = append(session.messages, runToolCall(ctx, send, session, call))
		case i == p.index:
			session.messages = append(session.messages, toolResult(call, map[string]any{
				"approved": false,
				"note":     "the user did not approve this; nothing was written. Ask what they would like changed.",
			}))
		default:
			session.messages = append(session.messages, toolResult(call, map[string]any{
				"error": "not run — an earlier call in the same reply was waiting on the user. Call it again if it is still what you want.",
			}))
		}
	}
}

// approvedArgs is the gated call's arguments as they should run now that the user has
// said yes. apply_week_plan's own `confirm` flag asks the *model* to get the user's
// agreement before replacing a planned day — which is exactly what the approval just
// was, in a form the user actually saw. Leaving it unset would refuse the write and send
// the model back to ask a second time, in prose, for the thing already approved.
func approvedArgs(call openai.ChatCompletionMessageToolCall) string {
	if call.Function.Name != "apply_week_plan" {
		return call.Function.Arguments
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
		return call.Function.Arguments
	}
	args["confirm"] = true
	patched, err := json.Marshal(args)
	if err != nil {
		return call.Function.Arguments
	}
	return string(patched)
}

// takePending consumes the thread's awaiting-approval call if it is the one the client
// answered. Nil means there is nothing to resume — an expired session, a restart, or a
// stale card from a turn that has since been superseded.
func takePending(state *sessionState, id string) *pendingApproval {
	sessions.Lock()
	defer sessions.Unlock()
	p := state.pending
	if p == nil || p.id != id {
		return nil
	}
	state.pending = nil
	return p
}

// storePending parks (or clears) the thread's awaiting-approval call.
func storePending(state *sessionState, p *pendingApproval) {
	sessions.Lock()
	defer sessions.Unlock()
	state.pending = p
}

// toolResult is the tool message for one call, already JSON-encoded.
func toolResult(call openai.ChatCompletionMessageToolCall, result any) model.ConversationMessage {
	payload, err := json.Marshal(result)
	if err != nil {
		payload = []byte(`{"error":"the tool returned something unreadable"}`)
	}
	return model.ConversationMessage{Role: "tool", Content: string(payload), ToolCallID: call.ID}
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
	telemetry.RecordChatTool(ctx, name, err == nil)
	send("tool_end", map[string]any{"name": name, "ok": err == nil})
	msg := toolResult(call, result)
	if art != nil {
		send("artifact", art)
		// Stored on the message, not just streamed: a thread reopened tomorrow has to
		// show the card it wrote (BACKLOG.md 10.4). A card that will not marshal is
		// dropped rather than failing the turn — the prose is still worth keeping.
		if encoded, err := json.Marshal(art); err == nil {
			msg.Artifact = encoded
		}
	}
	return msg
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
func threadToParams(msgs []model.ConversationMessage, language, household string, extra ...string) []openai.ChatCompletionMessageParamUnion {
	msgs = trimThread(msgs)
	system := llm.ChatAgentSystemPrompt
	// Without the date the model guesses starts_on, which is how "plan this week" ended up
	// naming days that were already cooked (the plan tools also refuse those, see today()).
	system += "- Today is " + time.Now().Format("Monday, 2006-01-02") + ". Resolve every date you pass to a tool against it, and never plan, change or clear a day before today — those meals have already been cooked.\n"
	if household != "" {
		system += "- The user shares a household: " + household + ". Recipes, the pantry and the week are shared with them, so \"our recipes\" and \"what's in the fridge\" mean the household's, and a saved recipe someone else owns can be cooked and planned but not edited.\n"
	}
	if language != "" {
		system += "- The user's device language is \"" + language + "\" (ISO 639-1). Write in it, and pass it as generate_recipe's `language`, unless the user's own message is clearly in another language.\n"
	}
	for _, e := range extra {
		if e != "" {
			system += e + "\n"
		}
	}
	params := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs)+1)
	params = append(params, openai.SystemMessage(system))
	for _, m := range msgs {
		switch m.Role {
		case "tool":
			params = append(params, openai.ToolMessage(m.Content, m.ToolCallID))
		// The rolling summary of the compacted span, sent as a system message so the
		// model reads it as context rather than as something the user said.
		case "system":
			params = append(params, openai.SystemMessage(m.Content))
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

// trimThread is the within-turn backstop for a thread that outgrows the cap between
// compactions: it keeps the last maxThreadMessages, then drops any leading tool results
// whose requesting assistant message fell off the front — the API rejects an orphan tool
// message outright. A leading summary is held back from the cut and re-attached: it
// stands in for everything before it, and dropping it is exactly the silent forgetting
// compaction exists to remove.
func trimThread(msgs []model.ConversationMessage) []model.ConversationMessage {
	var head []model.ConversationMessage
	if len(msgs) > 0 && msgs[0].Role == "system" {
		head, msgs = msgs[:1:1], msgs[1:]
	}
	if limit := maxThreadMessages - len(head); len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	for len(msgs) > 0 && msgs[0].Role == "tool" {
		msgs = msgs[1:]
	}
	if len(head) == 0 {
		return msgs
	}
	out := make([]model.ConversationMessage, 0, len(head)+len(msgs))
	return append(append(out, head...), msgs...)
}

// compactCut is where an over-long thread is split: everything before the returned index
// is summarised, everything from it is kept verbatim. 0 means nothing to compact. The cut
// walks forward past any tool results that would otherwise lose the assistant message
// that requested them.
func compactCut(msgs []model.ConversationMessage) int {
	if len(msgs) <= maxThreadMessages {
		return 0
	}
	cut := len(msgs) - compactKeep
	for cut < len(msgs) && msgs[cut].Role == "tool" {
		cut++
	}
	if cut >= len(msgs) {
		return 0
	}
	return cut
}

// compactThread replaces the front of an over-long thread with one summary rather than
// dropping it silently (BACKLOG.md 2.6): past the cap the oldest messages used to simply
// stop being sent, so the agent forgot the recipe it had just written with no signal to
// anyone. Returns the compacted thread and the summary message to persist, or a nil
// summary when nothing was compacted — including when the summarising call fails, in
// which case trimThread's plain cut still applies.
func compactThread(ctx context.Context, msgs []model.ConversationMessage) ([]model.ConversationMessage, *model.ConversationMessage) {
	cut := compactCut(msgs)
	if cut == 0 {
		return msgs, nil
	}
	summary, err := chatCompletion(ctx, llm.ThreadSummaryPrompt, renderThread(msgs[:cut]), string(assistModel), "thread_summary")
	if err != nil || strings.TrimSpace(summary) == "" {
		slog.WarnContext(ctx, "chat: thread compaction failed, falling back to a plain cut", "error", err)
		return msgs, nil
	}
	// A previous summary sits at msgs[0] and is itself part of the span being summarised,
	// which is what makes this rolling rather than a one-off.
	m := model.ConversationMessage{
		Role:    "system",
		Content: "Earlier in this conversation (" + strconv.Itoa(cut) + " messages, summarised):\n" + strings.TrimSpace(summary),
	}
	return append([]model.ConversationMessage{m}, msgs[cut:]...), &m
}

// renderThread flattens messages into the plain text the summariser reads. Tool calls
// are named but their arguments and results are not replayed — what matters downstream
// is that a recipe was generated and what it was, not the JSON that carried it.
func renderThread(msgs []model.ConversationMessage) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "tool":
			continue
		case "system":
			b.WriteString("[earlier summary] " + m.Content + "\n")
		default:
			if m.Content != "" {
				b.WriteString(m.Role + ": " + m.Content + "\n")
			}
			for _, c := range m.ToolCalls {
				b.WriteString("assistant called " + c.Name + " with " + c.Args + "\n")
			}
		}
	}
	return b.String()
}

// sinceCompaction drops the span an earlier turn already summarised. The stored thread
// keeps those messages for the record; from here on the summary stands in for them.
func sinceCompaction(msgs []model.ConversationMessage) []model.ConversationMessage {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "system" {
			return msgs[i:]
		}
	}
	return msgs
}
