// Command backfill-grams fills ingredient_grams for the library that already exists.
// Live saves resolve their own conversions (service.EnsureGrams on recipe save), so this
// is for everything written before that: without it, a pre-existing recipe asking for
// "2 onions" still cannot be summed with one asking for "150 g onion", and cooking it
// cannot deduct anything from the pantry.
//
// Needs DB_URL and OPENAI_KEY. Safe to re-run — it only asks about pairs nothing can
// answer yet, so a second run with nothing left to do makes no LLM call and costs
// nothing. Re-running is also the retry: a pair the model dropped or garbled was never
// written, so it comes back on the worklist.
//
//	go run ./cmd/backfill-grams -dry-run
//	go run ./cmd/backfill-grams
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/ili16/recipe-generator/internal/service"
	"github.com/ili16/recipe-generator/internal/storage"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "list what would be resolved, but write nothing")
	flag.Parse()

	if os.Getenv("DB_URL") == "" || os.Getenv("OPENAI_KEY") == "" {
		slog.Error("DB_URL and OPENAI_KEY are both required")
		os.Exit(1)
	}
	storage.InitDBPool()
	ctx := context.Background()

	pairs, err := storage.MissingGramsPairs(ctx)
	if err != nil {
		slog.Error("read worklist failed", "error", err)
		os.Exit(1)
	}
	if len(pairs) == 0 {
		fmt.Println("nothing to do: every measure in use converts already")
		return
	}

	if *dryRun {
		for _, p := range pairs {
			unit := p.Unit
			if unit == "" {
				unit = "(one piece)"
			}
			fmt.Printf("%s\t%s\n", p.Item, unit)
		}
		fmt.Printf("would resolve %d pairs (dry-run)\n", len(pairs))
		return
	}

	if err := service.EnsureGrams(ctx, pairs); err != nil {
		slog.Error("backfill failed", "error", err)
		os.Exit(1)
	}

	// The worklist is the measure of success: whatever is still on it, the model did not
	// answer usably and a later run should try again.
	left, err := storage.MissingGramsPairs(ctx)
	if err != nil {
		slog.Error("re-read worklist failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("resolved %d of %d pairs, %d still unknown\n", len(pairs)-len(left), len(pairs), len(left))
}
