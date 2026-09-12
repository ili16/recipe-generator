// Package ratelimit provides an in-process, per-key token-bucket request limiter.
package ratelimit

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ili16/recipe-generator/internal/auth"
	"golang.org/x/time/rate"
)

// Limiter rate-limits requests by authenticated user ID (higher budget) or client IP
// (lower budget) for anonymous callers.
//
// ponytail: single-process map, swap for a shared store (Redis etc.) only if this
// backend is ever run on more than one instance — deploy/README.md shows it isn't.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	seen    map[string]time.Time

	anon, authed   rate.Limit
	anonB, authedB int
	window         time.Duration
}

// New creates a Limiter. anonPerWindow/authPerWindow are request caps per rolling
// window; burst is set to the full window cap so a caller can use their whole budget
// at once, then refills steadily over window.
func New(anonPerWindow, authPerWindow int, window time.Duration) *Limiter {
	seconds := window.Seconds()
	l := &Limiter{
		buckets: make(map[string]*rate.Limiter),
		seen:    make(map[string]time.Time),
		anon:    rate.Limit(float64(anonPerWindow) / seconds),
		anonB:   anonPerWindow,
		authed:  rate.Limit(float64(authPerWindow) / seconds),
		authedB: authPerWindow,
		window:  window,
	}
	go l.sweep()
	return l
}

// sweep evicts buckets idle for longer than window so the map doesn't grow unbounded.
func (l *Limiter) sweep() {
	for range time.Tick(l.window) {
		cutoff := time.Now().Add(-l.window)
		l.mu.Lock()
		for k, t := range l.seen {
			if t.Before(cutoff) {
				delete(l.seen, k)
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// ClientIP reads the caller's IP, preferring the Cloudflare Tunnel header (this backend
// always sits behind one, per deploy/README.md), then a generic proxy header, then the
// raw connection address.
func ClientIP(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	// RemoteAddr carries the ephemeral source port ("127.0.0.1:58640"), which is
	// different on every connection — returning it verbatim gave each request its own
	// bucket, so the anonymous rate limit never limited and anonymous /chat threads
	// (keyed by the same function) lost their history between turns.
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// Guard rate-limits next. A caller identified by auth.FromContext (route through
// authenticator.Optional first) gets the higher per-user budget; everyone else gets the
// lower per-IP budget.
func (l *Limiter) Guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := "ip:" + ClientIP(r)
		rps, burst := l.anon, l.anonB
		if u, ok := auth.FromContext(r.Context()); ok {
			key = fmt.Sprintf("user:%d", u.ID)
			rps, burst = l.authed, l.authedB
		}

		l.mu.Lock()
		b, ok := l.buckets[key]
		if !ok {
			b = rate.NewLimiter(rps, burst)
			l.buckets[key] = b
		}
		l.seen[key] = time.Now()
		l.mu.Unlock()

		if !b.Allow() {
			slog.WarnContext(r.Context(), "rate limited", "key", key, "path", r.URL.Path)
			w.Header().Set("Retry-After", strconv.Itoa(int(l.window.Seconds())))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate_limited"}`))
			return
		}
		next(w, r)
	}
}
