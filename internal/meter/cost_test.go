package meter_test

import (
	"testing"

	"github.com/raviikumar001/prism-gateway/internal/meter"
)

func TestCostUSD(t *testing.T) {
	price := meter.Price{InputPer1M: 0.15, OutputPer1M: 0.60}
	got := meter.CostUSD(1_000_000, 1_000_000, price)
	want := 0.75
	if got != want {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCostUSDTiny(t *testing.T) {
	price := meter.Price{InputPer1M: 0.15, OutputPer1M: 0.60}
	got := meter.CostUSD(5, 17, price)
	if got <= 0 {
		t.Fatalf("expected positive tiny cost, got %v", got)
	}
	micro := meter.ToMicroCents(got)
	if micro <= 0 {
		t.Fatalf("expected positive micro-cents, got %d", micro)
	}
	// 1 USD = 1e8 micro-cents
	if meter.ToMicroCents(1.0) != 100_000_000 {
		t.Fatalf("1 USD should be 1e8 micro-cents")
	}
}
