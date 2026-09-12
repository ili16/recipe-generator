// Package worker runs a single periodic background pass: recomputing per-user cooking
// profiles. It also used to close out stale generation_events; that table was dropped
// with the funnel it described (BACKLOG.md 3.10/3.14).
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/ili16/recipe-generator/internal/storage"
)

// Worker runs runOnce on a fixed interval, matching internal/ratelimit's self-starting
// time.Tick shape.
//
// ponytail: no Stop()/shutdown path, same as ratelimit.New() — this backend is
// single-instance (deploy/README.md) and restarted, not drained, on deploy.
type Worker struct {
	interval time.Duration
}

// Start spawns the background goroutine and returns immediately.
func Start(interval time.Duration) *Worker {
	w := &Worker{interval: interval}
	go w.loop()
	return w
}

func (w *Worker) loop() {
	for range time.Tick(w.interval) {
		w.runOnce(context.Background())
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	if n, err := storage.RecomputeAllCookingProfiles(ctx); err != nil {
		slog.Error("worker: recompute cooking profiles failed", "error", err)
	} else {
		slog.Debug("worker: recomputed cooking profiles", "users", n)
	}
}
