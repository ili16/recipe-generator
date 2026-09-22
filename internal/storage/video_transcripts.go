package storage

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// video_transcripts caches what a video says, keyed by the post it belongs to (migration
// 00047). Like ingredient_grams it is global and takes no userID: a public reel's
// soundtrack is not a fact about a household, and the cost of reading it should be paid
// once between everyone who pastes the link.

// VideoTranscript returns the cached reading of one post: what is spoken in it and what
// is written on its frames (migration 00051). found is separate from the two strings
// because "" is a legitimate cached answer for either — a reel with no speech in it, or
// one whose frames carry no recipe text — and re-deriving that silence costs a download,
// an ffmpeg run and a vision call every time.
func VideoTranscript(ctx context.Context, sourceKey string) (text, screenText string, found bool, err error) {
	err = Pool.QueryRow(ctx,
		`SELECT transcript, screen_text FROM video_transcripts WHERE source_key = $1`,
		sourceKey).Scan(&text, &screenText)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return text, screenText, true, nil
}

// PutVideoTranscript stores one. DO NOTHING on conflict, matching UpsertGrams: two turns
// racing on the same reel both paid for their own read, and either answer is the same
// video — the first one home wins rather than the last.
func PutVideoTranscript(ctx context.Context, sourceKey, transcript, screenText string, seconds float64) error {
	_, err := Pool.Exec(ctx, `
		INSERT INTO video_transcripts (source_key, transcript, screen_text, seconds)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (source_key) DO NOTHING`, sourceKey, transcript, screenText, seconds)
	return err
}
