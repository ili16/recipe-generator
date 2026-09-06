// Package billing estimates and enforces per-user LLM spend.
package billing

import (
	"os"
	"strconv"
)

// modelPricing is USD per 1M tokens for the models actually in use. Update when adding a
// new model or after an OpenAI price change.
var modelPricing = map[string]struct{ PromptPer1M, CompletionPer1M float64 }{
	"gpt-5.6-luna": {PromptPer1M: 5.00, CompletionPer1M: 15.00},
	"gpt-5.4-nano": {PromptPer1M: 0.15, CompletionPer1M: 0.60},
}

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

// CostUSD estimates the dollar cost of one chat completion call. Unknown models cost 0
// (rather than erroring) so a model rename doesn't break generation, just under-bills it.
func CostUSD(model string, promptTokens, completionTokens int64) float64 {
	p, ok := modelPricing[model]
	if !ok {
		return 0
	}
	return float64(promptTokens)/1_000_000*p.PromptPer1M + float64(completionTokens)/1_000_000*p.CompletionPer1M
}

// TranscribeCostUSD returns the flat estimated cost of one Whisper transcription call.
func TranscribeCostUSD() float64 {
	return transcribeCostUSD
}
