// Package billing estimates and enforces per-user LLM spend.
package billing

import (
	"log/slog"
	"os"
	"strconv"
)

// modelPricing is USD per 1M tokens for the models actually in use. Update when adding a
// new model or after an OpenAI price change.
var modelPricing = map[string]struct{ PromptPer1M, CompletionPer1M float64 }{
	"gpt-5.6-luna": {PromptPer1M: 5.00, CompletionPer1M: 15.00},
	"gpt-5.4-nano": {PromptPer1M: 0.15, CompletionPer1M: 0.60},
}

// fallbackPricing is charged for any model not in modelPricing (e.g. an LLM_MODEL A/B
// override that hasn't been priced yet) — set to the most expensive known tier so an
// unpriced model over-bills rather than silently billing $0 and evading the budget cap.
var fallbackPricing = modelPricing["gpt-5.6-luna"]

// transcribeCostUSD is a flat per-call estimate for Whisper transcription.
// ponytail: the current SDK call doesn't return audio duration, so this approximates a
// short voice note; move to duration-based pricing if /transcribe volume ever matters.
var transcribeCostUSD = envFloat("LLM_TRANSCRIBE_COST_USD", 0.003)

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// CostUSD estimates the dollar cost of one chat completion call. An unknown model logs a
// warning and is charged fallbackPricing — conservative, non-zero — rather than $0, so an
// unpriced model (e.g. an LLM_MODEL override) can't silently evade the budget cap.
func CostUSD(model string, promptTokens, completionTokens int64) float64 {
	p, ok := modelPricing[model]
	if !ok {
		slog.Warn("billing: unpriced model, using conservative fallback rate", "model", model)
		p = fallbackPricing
	}
	return float64(promptTokens)/1_000_000*p.PromptPer1M + float64(completionTokens)/1_000_000*p.CompletionPer1M
}

// TranscribeCostUSD returns the flat estimated cost of one Whisper transcription call.
func TranscribeCostUSD() float64 {
	return transcribeCostUSD
}
