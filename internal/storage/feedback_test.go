package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestFeedback is BACKLOG 9.16's acceptance against a real DB (skipped unless DB_URL is
// set): an anonymous report persists with a NULL user, an authed one links, a report with
// nothing in it is refused, a thumb with no words is accepted, and deleting the reporter
// keeps the report.
func TestFeedback(t *testing.T) {
	if os.Getenv("DB_URL") == "" {
		t.Skip("DB_URL not set, skipping DB integration test")
	}
	InitDBPool()
	ctx := context.Background()

	var userID int
	if err := Pool.QueryRow(ctx, `INSERT INTO users (oauth_id) VALUES ('test-feedback-user') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID) })

	// Clean up by id range, not by message: the wordless pulse row below has no message
	// and no contact to match on, and leaving it behind would show up in the next
	// feedback-today.sh run as a real report.
	var startID int
	if err := Pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM feedback`).Scan(&startID); err != nil {
		t.Fatalf("read high-water mark: %v", err)
	}
	t.Cleanup(func() { Pool.Exec(ctx, `DELETE FROM feedback WHERE id > $1`, startID) })

	// The whole point of the table: a reporter who never signed in.
	if err := CreateFeedback(ctx, nil, "bug", "test-feedback anonymous bug", "anon@example.com", nil, []byte(`{"route":"GroceryList"}`), nil); err != nil {
		t.Fatalf("anonymous report: %v", err)
	}
	var gotUser *int
	var gotContext, gotContact string
	err := Pool.QueryRow(ctx, `SELECT user_id, context::text, contact FROM feedback WHERE message = 'test-feedback anonymous bug'`).
		Scan(&gotUser, &gotContext, &gotContact)
	if err != nil {
		t.Fatalf("read back anonymous report: %v", err)
	}
	if gotUser != nil {
		t.Fatalf("anonymous report got user_id %d, want NULL", *gotUser)
	}
	if !strings.Contains(gotContext, "GroceryList") {
		t.Fatalf("context not stored: %q", gotContext)
	}
	if gotContact != "anon@example.com" {
		t.Fatalf("contact not stored: %q", gotContact)
	}

	// An authed report links to its user.
	if err := CreateFeedback(ctx, &userID, "idea", "test-feedback an idea", "", nil, nil, nil); err != nil {
		t.Fatalf("authed report: %v", err)
	}
	if err := Pool.QueryRow(ctx, `SELECT user_id FROM feedback WHERE message = 'test-feedback an idea'`).Scan(&gotUser); err != nil {
		t.Fatalf("read back authed report: %v", err)
	}
	if gotUser == nil || *gotUser != userID {
		t.Fatalf("authed report lost its user: %v", gotUser)
	}

	// A thumb with no words is a real answer — that is the whole design of the pulse card.
	thumb := int16(-1)
	if err := CreateFeedback(ctx, &userID, "pulse", "", "", &thumb, nil, nil); err != nil {
		t.Fatalf("wordless pulse: %v", err)
	}

	// Nothing at all is not.
	for _, tc := range []struct {
		name      string
		kind      string
		message   string
		sentiment *int16
	}{
		{"empty message, no thumb", "bug", "   ", nil},
		{"unknown kind", "rant", "test-feedback something", nil},
		{"out-of-range sentiment", "pulse", "", ptrInt16(3)},
	} {
		if err := CreateFeedback(ctx, &userID, tc.kind, tc.message, "", tc.sentiment, nil, nil); !errors.Is(err, ErrInvalidFeedback) {
			t.Fatalf("%s: want ErrInvalidFeedback, got %v", tc.name, err)
		}
	}

	// An over-long message is truncated, not rejected — and truncation must not split a
	// multi-byte rune, or Postgres rejects the whole insert as invalid UTF-8.
	long := "test-feedback " + strings.Repeat("é", maxFeedbackMessage)
	if err := CreateFeedback(ctx, &userID, "bug", long, "", nil, nil, nil); err != nil {
		t.Fatalf("over-long report should be truncated, not refused: %v", err)
	}

	// A screenshot round-trips byte-for-byte, and an absent one is NULL rather than an
	// empty bytea — "did they attach one" has to stay a single IS NOT NULL check.
	shot := []byte("\x89PNG\r\n\x1a\n fake png bytes")
	if err := CreateFeedback(ctx, &userID, "bug", "test-feedback with a shot", "", nil, nil, shot); err != nil {
		t.Fatalf("report with screenshot: %v", err)
	}
	var got []byte
	if err := Pool.QueryRow(ctx, `SELECT screenshot FROM feedback WHERE message = 'test-feedback with a shot'`).Scan(&got); err != nil {
		t.Fatalf("read back screenshot: %v", err)
	}
	if !bytes.Equal(got, shot) {
		t.Fatalf("screenshot round-trip: got %d bytes, want %d", len(got), len(shot))
	}
	var isNull bool
	if err := Pool.QueryRow(ctx, `SELECT screenshot IS NULL FROM feedback WHERE message = 'test-feedback an idea'`).Scan(&isNull); err != nil {
		t.Fatalf("read back null screenshot: %v", err)
	}
	if !isNull {
		t.Fatal("a report with no screenshot must store NULL, not an empty bytea")
	}

	// Over the cap is refused outright — half an image is not an image, so truncating the
	// way an over-long message is truncated would store something unopenable.
	if err := CreateFeedback(ctx, &userID, "bug", "test-feedback huge shot", "", nil, nil,
		make([]byte, MaxFeedbackScreenshot+1)); !errors.Is(err, ErrScreenshotTooLarge) {
		t.Fatalf("oversize screenshot: want ErrScreenshotTooLarge, got %v", err)
	}

	// Deleting the reporter keeps the report (ON DELETE SET NULL, not CASCADE).
	before := countFeedback(ctx, t, userID)
	if before == 0 {
		t.Fatal("expected rows linked to the seeded user")
	}
	if _, err := Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var orphaned int
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM feedback WHERE message LIKE 'test-feedback%' AND user_id IS NULL`).Scan(&orphaned); err != nil {
		t.Fatalf("count orphaned: %v", err)
	}
	if orphaned < before {
		t.Fatalf("deleting the user took %d reports with it", before-orphaned)
	}
}

func ptrInt16(v int16) *int16 { return &v }

func countFeedback(ctx context.Context, t *testing.T, userID int) int {
	t.Helper()
	var n int
	if err := Pool.QueryRow(ctx, `SELECT COUNT(*) FROM feedback WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatalf("count feedback: %v", err)
	}
	return n
}

// TestTruncateRunes guards the one piece of non-obvious logic in feedback.go: byte-slicing
// a string with multi-byte characters produces invalid UTF-8, which Postgres refuses.
func TestTruncateRunes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"ééé", 3, "ééé"},   // 6 bytes, 3 runes — must not be cut
		{"ééééé", 2, "éé"},  // cut on a rune boundary
		{"a🍳b", 2, "a🍳"}, // 4-byte rune
	} {
		if got := truncateRunes(tc.in, tc.n); got != tc.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
		if !utf8.ValidString(truncateRunes(tc.in, tc.n)) {
			t.Errorf("truncateRunes(%q, %d) produced invalid UTF-8", tc.in, tc.n)
		}
	}
}
