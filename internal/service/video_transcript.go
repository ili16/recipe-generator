package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ili16/recipe-generator/internal/billing"
	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/ili16/recipe-generator/internal/utils"
	"github.com/openai/openai-go"
	"go.opentelemetry.io/otel/attribute"
)

// Reading a reel out loud.
//
// A cooking reel keeps its recipe in three places: the caption, the screen, and the
// voice. social.go reads the caption, and when that holds nothing — "full recipe in my
// bio", a hook and a hashtag — this reads the voice instead: download the mp4 the embed
// page exposes, strip it to a 16kHz mono Opus track (a 27MB video becomes ~260KB of
// audio, which is both cheaper to upload and under Whisper's 25MB limit), transcribe it.
//
// It is deliberately the *fallback*. It costs a download, an ffmpeg run and about a cent
// a minute of audio, so nothing pays for it until a source has already failed to yield a
// recipe from its text.
//
// ponytail: audio only. The quantities in a reel are often burned into the frame rather
// than spoken ("200 of this" out loud, "200g flour" on screen) — sampling frames into the
// vision path we already use for photos is the other half, and is not built.

const (
	// maxTranscriptSeconds bounds one transcription. A recipe reel is a minute or two;
	// anything past this is a stream, a lecture or a mistake, and the cap is what keeps
	// a pasted link from becoming an unbounded bill.
	maxTranscriptSeconds = 900

	// transcriptTimeout bounds the whole read — download, transcode, transcribe. The
	// user is waiting on it inside a chat turn.
	transcriptTimeout = 3 * time.Minute

	// opusBytesPerSecond is what the -b:a 16k encode below actually produces, used to
	// price the call: Whisper's response carries no duration, and the file we just made
	// does. Measured within 5% on a 127s reel.
	opusBytesPerSecond = 2000
)

// errNoVideo is a source with no readable video behind it — every non-Instagram URL, and
// every Instagram post that is a photo. Not surfaced to the user: the caller keeps the
// original "no recipe in that" error.
var errNoVideo = errors.New("no video to read")

// videoTranscript returns what is spoken in the video behind a social URL. An empty
// string with a nil error is a video with no speech in it.
func videoTranscript(ctx context.Context, pageURL string) (string, error) {
	// Before the embed page, before the download: the same reel pasted twice must not be
	// read twice (BACKLOG 11.9). The key is the post's shortcode, so the four URL shapes
	// Instagram hands out for one reel are one cache entry — and a cached "" is a reel we
	// already know is silent, not a miss.
	cacheKey := utils.InstagramShortcode(pageURL)
	if cacheKey != "" {
		if text, found, err := storage.VideoTranscript(ctx, cacheKey); err != nil {
			slog.WarnContext(ctx, "transcript: cache read failed", "error", err)
		} else if found {
			slog.InfoContext(ctx, "transcript: cache hit", "key", cacheKey, "chars", len(text))
			return text, nil
		}
	}

	videoURL := utils.InstagramVideoURL(pageURL)
	if videoURL == "" {
		return "", errNoVideo
	}

	ctx, cancel := context.WithTimeout(ctx, transcriptTimeout)
	defer cancel()
	ctx, span := telemetry.StartSpan(ctx, "llm.video_transcript")
	defer span.End()

	dir, err := os.MkdirTemp("", "reel")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	video, audio := filepath.Join(dir, "in.mp4"), filepath.Join(dir, "out.ogg")
	if err := utils.FetchToFile(videoURL, video); err != nil {
		return "", err
	}

	// -vn drops the picture, -t caps the length, and Opus at 16k mono is plenty for
	// speech — this is the whole reason the upload fits.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y",
		"-i", video, "-vn", "-t", strconv.Itoa(maxTranscriptSeconds),
		"-ac", "1", "-ar", "16000", "-c:a", "libopus", "-b:a", "16k", audio)
	if out, err := cmd.CombinedOutput(); err != nil {
		// A missing ffmpeg lands here too, which is the right shape: the caller falls
		// back to the caption-only answer rather than failing the turn.
		slog.WarnContext(ctx, "transcript: ffmpeg failed", "error", err, "output", string(out))
		return "", errors.New("could not read that video's audio")
	}

	f, err := os.Open(audio)
	if err != nil {
		return "", err
	}
	defer f.Close()
	size, err := f.Stat()
	if err != nil {
		return "", err
	}

	client := llm.Client()
	transcription, err := client.Audio.Transcriptions.New(ctx, openai.AudioTranscriptionNewParams{
		File:  namedReader{Reader: f, name: "reel.ogg"},
		Model: openai.AudioModelWhisper1,
	})
	if err != nil {
		span.RecordError(err)
		slog.WarnContext(ctx, "transcript: transcription failed", "error", err)
		return "", errors.New("could not transcribe that video")
	}

	seconds := float64(size.Size()) / opusBytesPerSecond
	span.SetAttributes(attribute.Float64("audio.seconds", seconds))
	llm.RecordCostUSD(ctx, "transcribe_video", string(openai.AudioModelWhisper1),
		billing.TranscribeCostUSDFor(seconds), 0, 0)
	slog.InfoContext(ctx, "transcript: read a video", "seconds", int(seconds), "chars", len(transcription.Text))

	// Detached and on its own deadline: the read is paid for by now, and losing it to a
	// user who closed the tab would mean paying again for the same reel. A failed write
	// costs nothing but a second read later, so it is logged and not returned.
	if cacheKey != "" {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := storage.PutVideoTranscript(writeCtx, cacheKey, transcription.Text, seconds); err != nil {
			slog.WarnContext(ctx, "transcript: cache write failed", "error", err)
		}
	}
	return transcription.Text, nil
}
