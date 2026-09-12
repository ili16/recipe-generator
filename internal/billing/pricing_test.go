package billing

import "testing"

func TestCostUSDKnownModel(t *testing.T) {
	got := CostUSD("gpt-5.4-nano", 1_000_000, 1_000_000)
	want := 0.15 + 0.60
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCostUSDUnknownModelUsesFallback(t *testing.T) {
	got := CostUSD("some-future-model", 1_000_000, 1_000_000)
	if got == 0 {
		t.Fatalf("got 0, want a non-zero conservative fallback charge")
	}
	want := fallbackPricing.PromptPer1M + fallbackPricing.CompletionPer1M
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
