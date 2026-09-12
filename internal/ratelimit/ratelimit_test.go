package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGuardBlocksAfterBurst(t *testing.T) {
	l := New(2, 60, time.Hour) // anon: 2/hour, burst 2
	h := l.Guard(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/generate", nil)
	req.RemoteAddr = "1.2.3.4:1234"

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, w.Code)
		}
	}

	w := httptest.NewRecorder()
	h(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request: got %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}
}

// Every connection from one client carries a different ephemeral source port. Keying on
// the raw RemoteAddr therefore handed each request its own bucket and the anonymous limit
// never fired — the same key is what /chat uses to find an anonymous thread, so that lost
// its history too.
func TestGuardKeysIgnoreSourcePort(t *testing.T) {
	l := New(2, 60, time.Hour) // anon: 2/hour, burst 2
	h := l.Guard(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	codes := make([]int, 0, 3)
	for _, port := range []string{"1111", "2222", "3333"} {
		req := httptest.NewRequest(http.MethodPost, "/generate", nil)
		req.RemoteAddr = "9.9.9.9:" + port
		w := httptest.NewRecorder()
		h(w, req)
		codes = append(codes, w.Code)
	}
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
		t.Fatalf("first two requests: got %v, want 200s", codes)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("3rd request from the same IP on a new port: got %d, want 429", codes[2])
	}
}

func TestGuardSeparatesKeysByIP(t *testing.T) {
	l := New(1, 60, time.Hour)
	h := l.Guard(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	for _, ip := range []string{"1.1.1.1:1", "2.2.2.2:2"} {
		req := httptest.NewRequest(http.MethodPost, "/generate", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		h(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ip %s: got %d, want 200", ip, w.Code)
		}
	}
}
