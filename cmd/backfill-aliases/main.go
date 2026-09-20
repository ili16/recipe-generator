// Command backfill-aliases fills ingredient_alias for the library that already exists.
// Live saves resolve their own names (service.EnsureAliases on recipe save), so this is
// for everything written before that: without it, a pre-existing German recipe's
// "zwiebel" and an English one's "onion" stay two foods, and a pantry holding
// "parmigiano reggiano" covers no recipe asking for parmesan.
//
// Needs DB_URL and OPENAI_KEY. Safe to re-run — it only asks about names nothing can
// answer yet, so a second run with nothing left to do makes no LLM call and costs
// nothing. Re-running is also the retry: a name the model dropped or garbled was never
// written, so it comes back on the worklist.
//
// Run this BEFORE cmd/backfill-grams on a cold database: grams are keyed by the canonical
// name, so resolving the aliases first means each food is weighed once instead of once
// per language it was written in.
//
//	go run ./cmd/backfill-aliases -dry-run
//	go run ./cmd/backfill-aliases
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

	names, err := storage.MissingAliasNames(ctx)
	if err != nil {
		slog.Error("read worklist failed", "error", err)
		os.Exit(1)
	}
	if len(names) == 0 {
		fmt.Println("nothing to do: every food in use has a canonical name already")
		return
	}

	if *dryRun {
		for _, n := range names {
			fmt.Println(n)
		}
		fmt.Printf("would resolve %d names (dry-run)\n", len(names))
		return
	}

	if err := service.EnsureAliases(ctx, names); err != nil {
		slog.Error("backfill failed", "error", err)
		os.Exit(1)
	}

	// The worklist is the measure of success: whatever is still on it, the model did not
	// answer usably and a later run should try again.
	left, err := storage.MissingAliasNames(ctx)
	if err != nil {
		slog.Error("re-read worklist failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("resolved %d of %d names, %d still unknown\n", len(names)-len(left), len(names), len(left))
}
