package storage

import (
	"context"
	"os"
	"testing"
)

// BACKLOG 11.9's acceptance, second half: "the second paste of the same URL costs
// nothing". The two cases that make that true and are easy to get wrong — a silent reel
// caches its silence rather than re-reading it, and a second write does not move an
// answer everything else has already been generated from.
func TestVideoTranscriptCache(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	const spoken, silent = "test-spoken-reel", "test-silent-reel"
	t.Cleanup(func() {
		Pool.Exec(ctx, `DELETE FROM video_transcripts WHERE source_key = ANY($1)`, []string{spoken, silent})
	})

	if _, found, err := VideoTranscript(ctx, spoken); err != nil || found {
		t.Fatalf("empty cache: found=%v err=%v, want false/nil", found, err)
	}

	if err := PutVideoTranscript(ctx, spoken, "first the flour, then the water", 127); err != nil {
		t.Fatalf("PutVideoTranscript: %v", err)
	}
	text, found, err := VideoTranscript(ctx, spoken)
	if err != nil || !found || text != "first the flour, then the water" {
		t.Fatalf("after write: %q found=%v err=%v", text, found, err)
	}

	// A reel with no speech in it. "" is an answer — without found, the caller would
	// re-download and re-transcribe 27 MB every time to rediscover the same silence.
	if err := PutVideoTranscript(ctx, silent, "", 31); err != nil {
		t.Fatalf("PutVideoTranscript(silent): %v", err)
	}
	if text, found, err := VideoTranscript(ctx, silent); err != nil || !found || text != "" {
		t.Fatalf("silent reel: %q found=%v err=%v, want \"\"/true/nil", text, found, err)
	}

	// DO NOTHING, like UpsertGrams: a second reader does not move an answer already
	// stored, so two turns racing on one reel cannot disagree about what it said.
	if err := PutVideoTranscript(ctx, spoken, "something else entirely", 9); err != nil {
		t.Fatalf("second write: %v", err)
	}
	if text, _, _ := VideoTranscript(ctx, spoken); text != "first the flour, then the water" {
		t.Fatalf("second write moved the row: %q", text)
	}
}
