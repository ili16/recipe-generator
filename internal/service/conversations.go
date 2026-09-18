package service

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/ili16/recipe-generator/internal/storage"
)

// The thread list and the thread itself (BACKLOG.md 10.4). Conversations have been
// persisted since 2.1 and the client never read one back: closing the tab lost the
// conversation from the user's side while it sat in Postgres.

// maxListedConversations bounds the list. Far more than anyone has, and the list is a
// picker, not an archive — paging it is a problem for whoever has 200 threads.
const maxListedConversations = 100

// threadBubble is one turn as the transcript shows it, which is not one stored message:
// the client renders a user bubble and then *one* assistant bubble per turn, with the
// tool cards folded into it. Collapsing here rather than in the client keeps the replayed
// thread the same shape as the streamed one, in one place.
type threadBubble struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"text"`
	// AttachmentCount replaces the image_ref markers the turn appended to the user's
	// message: the bytes are long gone, the fact that a photo was handed over is not.
	AttachmentCount int               `json:"attachment_count,omitempty"`
	Artifacts       []json.RawMessage `json:"artifacts,omitempty"`
}

// photoMarker is what HandleChat appends to a user message per attached photo. It is
// addressed to the model, not to the reader, so it is stripped back out on the way here.
var photoMarker = regexp.MustCompile(`\n*\[attached photo\] image_ref=\S+`)

// threadBubbles folds stored messages into the transcript. A tool message contributes its
// card and nothing else; the compaction summary (role system) is internal bookkeeping and
// is left out — replaying it would show the user a note about messages they can still see
// above it.
func threadBubbles(msgs []model.ConversationMessage) []threadBubble {
	out := []threadBubble{}
	assistant := -1 // index of the bubble this turn is filling, -1 between turns

	for _, m := range msgs {
		switch m.Role {
		case "system":
			continue
		case "user":
			text := m.Content
			count := len(photoMarker.FindAllString(text, -1))
			text = strings.TrimSpace(photoMarker.ReplaceAllString(text, ""))
			out = append(out, threadBubble{Role: "user", Text: text, AttachmentCount: count})
			assistant = -1
		default:
			if assistant < 0 {
				out = append(out, threadBubble{Role: "assistant"})
				assistant = len(out) - 1
			}
			if m.Role == "assistant" && m.Content != "" {
				if out[assistant].Text != "" {
					out[assistant].Text += "\n\n"
				}
				out[assistant].Text += m.Content
			}
			if len(m.Artifact) > 0 {
				out[assistant].Artifacts = append(out[assistant].Artifacts, m.Artifact)
			}
		}
	}
	return out
}

// HandleListConversations returns this user's threads, newest first.
func HandleListConversations(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	list, err := storage.ListConversations(r.Context(), user.ID, maxListedConversations)
	if err != nil {
		slog.ErrorContext(r.Context(), "conversations: list failed", "error", err)
		http.Error(w, "could not list conversations", http.StatusInternalServerError)
		return
	}
	writeJSON(w, list)
}

// HandleGetConversation returns one thread's transcript: the messages and the cards the
// turns produced, in the shape the chat screen renders.
func HandleGetConversation(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")

	title, err := storage.ConversationTitle(r.Context(), user.ID, id)
	if errors.Is(err, storage.ErrConversationNotFound) {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}
	if err == nil {
		var msgs []model.ConversationMessage
		if msgs, err = storage.ConversationMessages(r.Context(), user.ID, id); err == nil {
			writeJSON(w, map[string]any{"id": id, "title": title, "messages": threadBubbles(msgs)})
			return
		}
	}
	slog.ErrorContext(r.Context(), "conversations: read failed", "error", err, "conversation_id", id)
	http.Error(w, "could not read conversation", http.StatusInternalServerError)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("conversations: encode failed", "error", err)
	}
}

// maxTitleRunes bounds a hand-typed thread name, matching the cap the model-written title
// already gets in chat.go — the list renders one line either way.
const maxTitleRunes = 80

// HandleRenameConversation renames a thread (BACKLOG.md 10.7). An empty title is a valid
// body: it clears the name and the list falls back to the thread's last message.
func HandleRenameConversation(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(body.Title)
	if len([]rune(title)) > maxTitleRunes {
		title = string([]rune(title)[:maxTitleRunes])
	}
	id := r.PathValue("id")
	err := storage.SetConversationTitle(r.Context(), user.ID, id, title)
	if errors.Is(err, storage.ErrConversationNotFound) {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "conversations: rename failed", "error", err, "conversation_id", id)
		http.Error(w, "could not rename conversation", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"id": id, "title": title})
}

// HandleDeleteConversation removes a thread and its transcript. There is no undo and the
// client confirms first; the row is gone, not hidden.
func HandleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	err := storage.DeleteConversation(r.Context(), user.ID, id)
	if errors.Is(err, storage.ErrConversationNotFound) {
		http.Error(w, "conversation not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "conversations: delete failed", "error", err, "conversation_id", id)
		http.Error(w, "could not delete conversation", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
