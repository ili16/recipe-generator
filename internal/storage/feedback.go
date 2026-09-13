package storage

import (
	"context"
	"errors"
	"strings"
)

// ErrInvalidFeedback is a 400, not a 500: the caller sent a report with nothing in it, or
// a kind the table does not accept. The CHECK constraints would catch both, but as a 500.
var ErrInvalidFeedback = errors.New("feedback is empty or malformed")

// Caps, applied by truncation rather than rejection: a reporter who pasted a stack trace
// should still get their report filed, not a 400 telling them to write less.
const (
	maxFeedbackMessage = 8000
	maxFeedbackContact = 320 // the maximum length of an email address per RFC 3696
	// MaxFeedbackScreenshot bounds one attached image. Generous for a phone screenshot
	// (a 1179x2556 PNG is ~1-2 MB) and small enough that the table cannot run away.
	MaxFeedbackScreenshot = 5 << 20
)

// ErrScreenshotTooLarge is a 413, not a silent truncation: half an image is not an image.
var ErrScreenshotTooLarge = errors.New("screenshot is too large")

var feedbackKinds = map[string]bool{"bug": true, "idea": true, "pulse": true}

// truncateRunes caps s at n runes. Slicing to n *bytes* would split a multi-byte rune and
// hand Postgres invalid UTF-8, which it rejects outright — so a report with an emoji in it
// would fail where the same report in ASCII succeeded.
func truncateRunes(s string, n int) string {
	if len(s) <= n { // len in bytes is >= len in runes, so this is the cheap early out
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// CreateFeedback files one report. userID is nil for an anonymous reporter — the whole
// point of this table is that a friend who never signed in can still tell us something.
// sentiment is nil unless this is a 'pulse' row, context is the raw JSON the client
// attached (route, version, platform, locale), and screenshot is decoded image bytes;
// pass nil for either.
func CreateFeedback(ctx context.Context, userID *int, kind, message, contact string, sentiment *int16, jsonContext, screenshot []byte) error {
	if !feedbackKinds[kind] {
		return ErrInvalidFeedback
	}
	message = strings.TrimSpace(message)
	contact = strings.TrimSpace(contact)
	if message == "" && sentiment == nil {
		return ErrInvalidFeedback
	}
	if sentiment != nil && *sentiment != 1 && *sentiment != -1 {
		return ErrInvalidFeedback
	}
	message = truncateRunes(message, maxFeedbackMessage)
	contact = truncateRunes(contact, maxFeedbackContact)
	if len(jsonContext) == 0 {
		jsonContext = []byte(`{}`)
	}
	if len(screenshot) > MaxFeedbackScreenshot {
		return ErrScreenshotTooLarge
	}
	// A zero-length slice must go in as NULL, not an empty bytea, so "did they attach one"
	// stays a single IS NOT NULL check in the morning dump.
	if len(screenshot) == 0 {
		screenshot = nil
	}

	_, err := Pool.Exec(ctx, `
		INSERT INTO feedback (user_id, kind, message, sentiment, contact, context, screenshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		userID, kind, message, sentiment, contact, jsonContext, screenshot)
	return err
}
