package billing

import "testing"

func TestCostUSDKnownModel(t *testing.T) {
	got := CostUSD("gpt-5.4-nano", 1_000_000, 1_000_000)
	want := 0.15 + 0.60
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCostUSDUnknownModelIsZero(t *testing.T) {
	if got := CostUSD("some-future-model", 1000, 1000); got != 0 {
		t.Fatalf("got %v, want 0", got)
	}
}
