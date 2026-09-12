package utils

import "testing"

// BenchmarkDetectLanguage's first iteration pays for building the lingua-go detector;
// every iteration after should be materially cheaper since languageDetector is built
// once (sync.OnceValue) and reused. Run with -benchtime=2x to see per-call cost drop.
func BenchmarkDetectLanguage(b *testing.B) {
	for b.Loop() {
		DetectLanguage("ein rezept für sauce bearnaise mit frischen kräutern")
	}
}

func TestDetectLanguage(t *testing.T) {
	if lang, ok := DetectLanguage("ein rezept für sauce bearnaise mit frischen kräutern und butter"); !ok || lang != "de" {
		t.Fatalf("got (%q, %v), want (de, true)", lang, ok)
	}
}
