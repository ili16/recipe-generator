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
// -audit is the other half and the one to run after changing the prompt: it re-reads the
// substitution classes already in the table and reports the foods that do not belong in
// theirs. A class is reviewed as a family rather than a row because that is how it goes
// wrong -- "black pepper is a spice" is fine until you see the vanilla bean standing next
// to it. It prints and exits; -apply writes. Only class is ever rewritten, never canonical.
//
//	go run ./cmd/backfill-aliases -dry-run
//	go run ./cmd/backfill-aliases
//	go run ./cmd/backfill-aliases -audit
//	go run ./cmd/backfill-aliases -audit -apply
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ili16/recipe-generator/internal/service"
	"github.com/ili16/recipe-generator/internal/storage"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "list what would be resolved, but write nothing")
	audit := flag.Bool("audit", false, "review the substitution classes already resolved, instead of filling in missing names")
	apply := flag.Bool("apply", false, "with -audit: write the corrections instead of only printing them")
	flag.Parse()

	if os.Getenv("DB_URL") == "" || os.Getenv("OPENAI_KEY") == "" {
		slog.Error("DB_URL and OPENAI_KEY are both required")
		os.Exit(1)
	}
	storage.InitDBPool()
	ctx := context.Background()

	if *audit {
		runAudit(ctx, *apply)
		return
	}

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

// runAudit is the periodic second look at the classes (BACKLOG 17.6): it prints what it
// would change and writes nothing unless asked, because the reviewer is the same kind of
// model as the author and its disagreement is a question, not a verdict.
func runAudit(ctx context.Context, apply bool) {
	// BACKLOG 18.0: one food under two canonicals. Report-only; merging is a migration.
	splits, err := service.AuditCanonicals(ctx)
	if err != nil {
		slog.Error("canonical audit failed", "error", err)
		os.Exit(1)
	}
	for _, s := range splits {
		fmt.Printf("SPLIT  %-20s %s\n", s.Class, strings.Join(s.Canonicals, " = "))
	}
	if len(splits) > 0 {
		fmt.Println("Same food, several canonicals: pick one and UPDATE ingredient_alias SET canonical = ..., source = 'manual' (add a migration).")
	}

	fixes, err := service.AuditClasses(ctx)
	if err != nil {
		slog.Error("audit failed", "error", err)
		os.Exit(1)
	}
	if len(fixes) == 0 {
		fmt.Printf("%d canonical split(s); every substitution class holds together\n", len(splits))
		return
	}

	for _, f := range fixes {
		to := "no class (nothing substitutes for it)"
		if f.Class != nil {
			to = *f.Class
		}
		fmt.Printf("%-40s %s -> %s\n", f.Name, f.Was, to)
	}
	if !apply {
		fmt.Printf("%d to correct; re-run with -apply to write them.\n", len(fixes))
		fmt.Println("Disagree with one? Pin the row and it is never audited or overwritten again:")
		fmt.Println("  UPDATE ingredient_alias SET source = 'manual' WHERE name = '<food>';")
		return
	}

	classes := make(map[string]*string, len(fixes))
	for _, f := range fixes {
		classes[f.Name] = f.Class
	}
	changed, err := storage.CorrectAliasClasses(ctx, classes)
	if err != nil {
		slog.Error("writing corrections failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("corrected %d of %d\n", changed, len(fixes))
}
