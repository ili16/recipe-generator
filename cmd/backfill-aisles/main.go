// Command backfill-aisles is the one-off run BACKLOG 6.9 asks for: give every
// ingredient saved before 6.1 a shopping aisle so the grocery list stops showing a
// pre-6.1 library as one undifferentiated "Other" block.
//
// Needs DB_URL and OPENAI_KEY. Safe to re-run — it only ever looks at NULL rows, so a
// second run with nothing left to do makes no LLM call and costs nothing.
//
//	go run ./cmd/backfill-aisles -dry-run
//	go run ./cmd/backfill-aisles
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
	dryRun := flag.Bool("dry-run", false, "classify and log, but write nothing")
	flag.Parse()

	if os.Getenv("DB_URL") == "" || os.Getenv("OPENAI_KEY") == "" {
		slog.Error("DB_URL and OPENAI_KEY are both required")
		os.Exit(1)
	}
	storage.InitDBPool()

	classified, rows, err := service.BackfillIngredientAisles(context.Background(), *dryRun)
	if err != nil {
		slog.Error("backfill failed", "error", err, "classified", classified, "rows", rows)
		os.Exit(1)
	}
	fmt.Printf("classified %d ingredient names, updated %d rows (dry-run=%v)\n", classified, rows, *dryRun)
}
