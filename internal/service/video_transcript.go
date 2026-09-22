package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
// The audio is only half the reel. The quantities are usually burned into the frame
// rather than spoken ("200 of this" out loud, "200g flour" on screen), so the same mp4
// is also sampled into a handful of stills and read by the vision path that already
// reads a photo of a recipe (BACKLOG 11.10). Both halves come out of one download and
// are cached as one reading of one post.

const (
	// maxTranscriptSeconds bounds one transcription. A recipe reel is a minute or two;
	// anything past this is a stream, a lecture or a mistake, and the cap is what keeps
	// a pasted link from becoming an unbounded bill.
	maxTranscriptSeconds = 900

	// transcriptTimeout bounds the whole read — download, transcode, transcribe. The
	// user is waiting on it inside a chat turn.
	transcriptTimeout = 3 * time.Minute

	// opusBytesPerSecond is what the -b:a 16k encode below actually produces, used both
	// to price the transcription and to space the frame sample: Whisper's response
	// carries no duration, and the file we just made does. Measured within 5% on a 127s
	// reel — and 5% of a reel's length is a fraction of a second of drift on where a
	// frame lands, which does not matter for reading an overlay that sits on screen for
	// several seconds.
	opusBytesPerSecond = 2000

	// wantFrames is how many stills are read per reel. Each one is an image in a vision
	// call the user is waiting on inside a chat turn, so this is a cost and a latency
	// number, not a coverage one. Six covers the usual shape: an overlay card near the
	// start, the steps through the middle, the plated result at the end.
	wantFrames = 6

	// frameWidth downscales each still before it is uploaded. A reel is 1080x1920 and
	// the only thing being read off it is text large enough to be legible on a phone
	// held at arm's length, which survives 640 wide intact.
	frameWidth = 640
)

// videoReading is what one post says: out loud, and on screen. Either half may be empty
// for a video that genuinely has none of it.
type videoReading struct {
	Transcript string
	ScreenText string
}

// errNoVideo is a source with no readable video behind it — every non-Instagram URL, and
// every Instagram post that is a photo. Not surfaced to the user: the caller keeps the
// original "no recipe in that" error.
var errNoVideo = errors.New("no video to read")

// frameSampleFPS is the ffmpeg -vf fps= value that spreads frames evenly across a video
// of the given length. Capped at one frame a second: a very short clip would otherwise
// ask for several frames out of the same second, which is the same picture billed twice.
// A length we could not measure falls back to 1, where -frames:v does the bounding.
func frameSampleFPS(seconds float64, frames int) float64 {
	if seconds < 1 || frames < 1 {
		return 1
	}
	return min(float64(frames)/seconds, 1)
}

// videoTranscript returns what is spoken in the video behind a social URL. An empty
// reading with a nil error is a video with no speech and no on-screen text in it.
func videoTranscript(ctx context.Context, pageURL string) (videoReading, error) {
	// Before the embed page, before the download: the same reel pasted twice must not be
	// read twice (BACKLOG 11.9). The key is the post's shortcode, so the four URL shapes
	// Instagram hands out for one reel are one cache entry — and a cached "" is a reel we
	// already know is silent, not a miss.
	cacheKey := utils.InstagramShortcode(pageURL)
	if cacheKey != "" {
		if text, screen, found, err := storage.VideoTranscript(ctx, cacheKey); err != nil {
			slog.WarnContext(ctx, "transcript: cache read failed", "error", err)
		} else if found {
			slog.InfoContext(ctx, "transcript: cache hit", "key", cacheKey, "chars", len(text), "screen_chars", len(screen))
			return videoReading{Transcript: text, ScreenText: screen}, nil
		}
	}

	videoURL := utils.InstagramVideoURL(pageURL)
	if videoURL == "" {
		return videoReading{}, errNoVideo
	}

	ctx, cancel := context.WithTimeout(ctx, transcriptTimeout)
	defer cancel()
	ctx, span := telemetry.StartSpan(ctx, "llm.video_transcript")
	defer span.End()

	dir, err := os.MkdirTemp("", "reel")
	if err != nil {
		return videoReading{}, err
	}
	defer os.RemoveAll(dir)

	video, audio := filepath.Join(dir, "in.mp4"), filepath.Join(dir, "out.ogg")
	if err := utils.FetchToFile(videoURL, video); err != nil {
		return videoReading{}, err
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
		return videoReading{}, errors.New("could not read that video's audio")
	}

	f, err := os.Open(audio)
	if err != nil {
		return videoReading{}, err
	}
	defer f.Close()
	size, err := f.Stat()
	if err != nil {
		return videoReading{}, err
	}

	client := llm.Client()
	transcription, err := client.Audio.Transcriptions.New(ctx, openai.AudioTranscriptionNewParams{
		File:  namedReader{Reader: f, name: "reel.ogg"},
		Model: openai.AudioModelWhisper1,
	})
	if err != nil {
		span.RecordError(err)
		slog.WarnContext(ctx, "transcript: transcription failed", "error", err)
		return videoReading{}, errors.New("could not transcribe that video")
	}

	seconds := float64(size.Size()) / opusBytesPerSecond
	span.SetAttributes(attribute.Float64("audio.seconds", seconds))
	llm.RecordCostUSD(ctx, "transcribe_video", string(openai.AudioModelWhisper1),
		billing.TranscribeCostUSDFor(seconds), 0, 0)
	// The screen half. It shares this download and this deadline, and a failure in it is
	// not a failure of the read: a reel whose amounts were only ever spoken still has a
	// usable transcript.
	screenText := videoScreenText(ctx, video, dir, seconds)

	slog.InfoContext(ctx, "transcript: read a video", "seconds", int(seconds),
		"chars", len(transcription.Text), "screen_chars", len(screenText))

	// Detached and on its own deadline: the read is paid for by now, and losing it to a
	// user who closed the tab would mean paying again for the same reel. A failed write
	// costs nothing but a second read later, so it is logged and not returned.
	if cacheKey != "" {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := storage.PutVideoTranscript(writeCtx, cacheKey, transcription.Text, screenText, seconds); err != nil {
			slog.WarnContext(ctx, "transcript: cache write failed", "error", err)
		}
	}
	return videoReading{Transcript: transcription.Text, ScreenText: screenText}, nil
}

// videoScreenText samples frames out of an already-downloaded mp4 and reads the recipe
// text burned into them (BACKLOG 11.10). It never fails the caller: every problem here —
// no ffmpeg, a codec it cannot decode, a vision call that errored — leaves the spoken
// transcript standing on its own, which is what the reel gave us before this existed.
//
// ponytail: a failed read still gets cached as "no screen text" by the caller, so a
// transient vision error freezes one reel's amounts out until someone clears its row. The
// alternative — skipping the cache write — re-pays for the transcript too, which is the
// more expensive half. Give the write a screen-only retry if this is ever seen happening.
func videoScreenText(ctx context.Context, video, dir string, seconds float64) string {
	fps := strconv.FormatFloat(frameSampleFPS(seconds, wantFrames), 'f', 4, 64)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-i", video,
		"-vf", "fps="+fps+",scale="+strconv.Itoa(frameWidth)+":-2",
		"-frames:v", strconv.Itoa(wantFrames), "-q:v", "5",
		filepath.Join(dir, "frame-%02d.jpg"))
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.WarnContext(ctx, "transcript: frame sampling failed", "error", err, "output", string(out))
		return ""
	}

	frames, err := filepath.Glob(filepath.Join(dir, "frame-*.jpg"))
	if err != nil || len(frames) == 0 {
		return ""
	}

	// The text part first, then the stills in order: the model is being asked to read an
	// overlay that builds up across them, so the sequence carries meaning.
	parts := []openai.ChatCompletionContentPartUnionParam{
		openai.TextContentPart("Read the recipe text on these frames, in order."),
	}
	for _, frame := range frames {
		data, err := os.ReadFile(frame)
		if err != nil {
			continue
		}
		parts = append(parts, openai.ImageContentPart(
			openai.ChatCompletionContentPartImageImageURLParam{URL: imageDataURL(data)}))
	}

	// recipeModel, for ScanPantryImage's reason: this is OCR over motion-blurred text in
	// somebody's own language, and a misread amount is not cosmetic — it gets cooked.
	text, _, err := llm.Call[string](ctx, llm.Spec{
		Model:    recipeModel,
		CallType: "video_frames",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(llm.VideoFramesPrompt),
			openai.UserMessage(parts),
		},
	})
	if err != nil {
		slog.WarnContext(ctx, "transcript: frame read failed", "error", err, "frames", len(frames))
		return ""
	}
	return strings.TrimSpace(text)
}
