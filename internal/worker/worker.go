// Package worker runs the periodic background passes: recomputing per-user cooking
// profiles, and purging recipes that have sat in the trash past the retention window. It also used to close out stale generation_events; that table was dropped
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

// trashRetention is how long a soft-deleted recipe stays restorable (BACKLOG 8.2).
const trashRetention = 30 * 24 * time.Hour

func (w *Worker) runOnce(ctx context.Context) {
	if n, err := storage.RecomputeAllCookingProfiles(ctx); err != nil {
		slog.Error("worker: recompute cooking profiles failed", "error", err)
	} else {
		slog.Debug("worker: recomputed cooking profiles", "users", n)
	}

	if n, err := storage.PurgeDeletedRecipes(ctx, trashRetention); err != nil {
		slog.Error("worker: purge deleted recipes failed", "error", err)
	} else if n > 0 {
		slog.Info("worker: purged trashed recipes", "count", n)
	}
}
