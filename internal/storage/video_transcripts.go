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

// VideoTranscript returns the cached transcript for a source key. found is separate from
// the text because "" is a legitimate cached answer — a reel with no speech in it — and
// re-deriving that silence costs a download and an ffmpeg run every time.
func VideoTranscript(ctx context.Context, sourceKey string) (text string, found bool, err error) {
	err = Pool.QueryRow(ctx,
		`SELECT transcript FROM video_transcripts WHERE source_key = $1`, sourceKey).Scan(&text)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return text, true, nil
}

// PutVideoTranscript stores one. DO NOTHING on conflict, matching UpsertGrams: two turns
// racing on the same reel both paid for their own read, and either answer is the same
// video — the first one home wins rather than the last.
func PutVideoTranscript(ctx context.Context, sourceKey, transcript string, seconds float64) error {
	_, err := Pool.Exec(ctx, `
		INSERT INTO video_transcripts (source_key, transcript, seconds)
		VALUES ($1, $2, $3)
		ON CONFLICT (source_key) DO NOTHING`, sourceKey, transcript, seconds)
	return err
}
