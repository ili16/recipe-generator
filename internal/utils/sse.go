package utils

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
)

// WriteSSE writes one Server-Sent Event, JSON-encoding payload so arbitrary text
// (newlines, quotes) can never break the "data:" line framing, and flushes immediately
// so the client sees it without buffering. Shared by every SSE-emitting handler
// (internal/service and cmd) — was hand-duplicated once per package.
func WriteSSE(w http.ResponseWriter, flusher http.Flusher, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Error encoding SSE payload", "error", err)
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}
