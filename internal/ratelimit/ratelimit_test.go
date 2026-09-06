package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuardBlocksAfterBurst(t *testing.T) {
	l := New(2, 60) // anon: 2/hour, burst 2
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

func TestGuardSeparatesKeysByIP(t *testing.T) {
	l := New(1, 60)
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
