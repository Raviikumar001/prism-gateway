package httpapi

import (
	"strings"
	"testing"
	"time"
)

func TestTruncateDetailKeepsShortText(t *testing.T) {
	if got := truncateDetail("budget exceeded"); got != "budget exceeded" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateDetailCapsLongText(t *testing.T) {
	long := strings.Repeat("a", 400)
	got := truncateDetail(long)
	if got == long {
		t.Fatal("expected truncation")
	}
	if strings.Count(got, "a") > maxLogDetailRunes {
		t.Fatalf("still too long: %d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("missing ellipsis: %q", got)
	}
}

func TestCompleteLogFillsDefaults(t *testing.T) {
	start := time.Now().Add(-12 * time.Millisecond)
	got := completeLog(start, logEntry{Status: "ok"})
	if got.RequestID == "" {
		t.Fatal("expected request id")
	}
	if got.VirtualKey != unauthenticatedKey {
		t.Fatalf("virtual key = %q", got.VirtualKey)
	}
	if got.Cache != "miss" {
		t.Fatalf("cache = %q", got.Cache)
	}
	if got.LatencyMs < 12 {
		t.Fatalf("latency = %d", got.LatencyMs)
	}
}
