package service

import "testing"

// BACKLOG 11.10 samples frames evenly across a reel by handing ffmpeg a frames-per-second
// below 1. The arithmetic is the whole decision — get it wrong and either the sample
// clusters in the first seconds (missing the overlay that appears at 0:40) or a short clip
// pays for the same second twice.
func TestFrameSampleFPS(t *testing.T) {
	// The ordinary case: six frames spread over two minutes is one every twenty seconds.
	if got := frameSampleFPS(120, 6); got != 0.05 {
		t.Errorf("120s/6 frames: fps=%v, want 0.05", got)
	}
	// A short clip would ask for two frames a second; one is the ceiling, and -frames:v
	// bounds the count from there.
	if got := frameSampleFPS(3, 6); got != 1 {
		t.Errorf("3s/6 frames: fps=%v, want 1", got)
	}
	// A length we could not measure (a zero-byte audio encode) must still produce a
	// usable command rather than a divide-by-zero or an fps of +Inf.
	if got := frameSampleFPS(0, 6); got != 1 {
		t.Errorf("unmeasured length: fps=%v, want 1", got)
	}
	// Six frames of a fifteen-minute video is the cap's own edge: still even, still one
	// call's worth of images.
	if got := frameSampleFPS(maxTranscriptSeconds, wantFrames); got <= 0 || got >= 0.01 {
		t.Errorf("capped length: fps=%v, want a small positive spread", got)
	}
}
