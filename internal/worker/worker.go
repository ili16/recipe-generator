// Package worker runs a single periodic background pass: recomputing per-user cooking
// profiles and closing out stale generation_events. These two are combined into one
// goroutine deliberately — both are "periodic pass over user/recipe data", and splitting
// into two goroutines for two cheap SQL statements would add ceremony, not isolation, at
// this scale.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/ili16/recipe-generator/internal/telemetry"
)

// staleAfter is how long a generation can sit "pending" before the sweep decides it's
// done — long enough that a slow in-progress review isn't misclassified, short enough to
// close the funnel same-day.
const staleAfter = 6 * time.Hour

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

	counts, err := storage.SweepStale(ctx, staleAfter)
	if err != nil {
		slog.Error("worker: sweep stale generation events failed", "error", err)
		return
	}
	for outcome, n := range counts {
		telemetry.RecordGenerationOutcome(ctx, outcome, n)
	}
	if len(counts) > 0 {
		slog.Debug("worker: swept stale generation events", "counts", counts)
	}
}
