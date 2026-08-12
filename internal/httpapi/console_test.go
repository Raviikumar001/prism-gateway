package httpapi

import (
	"testing"
	"time"
)

func TestConsoleAttentionAllClear(t *testing.T) {
	items := consoleAttentionItems(true, 0, 0, 0, 0, nil)
	if len(items) != 1 || items[0].Tone != "success" {
		t.Fatalf("expected all-clear, got %+v", items)
	}
}

func TestConsoleAttentionFlagsBreakersAndBudget(t *testing.T) {
	tenants := []map[string]any{
		{"team": "search", "budget_used_frac": 0.9},
	}
	items := consoleAttentionItems(false, 1, 1, 3, 2, tenants)
	if len(items) != 5 {
		t.Fatalf("expected 5 attention items, got %d: %+v", len(items), items)
	}
	if items[0].Tone != "danger" || items[0].Title != "Platform degraded" {
		t.Fatalf("first item = %+v", items[0])
	}
}

func TestFillHourlySeriesPadsMissingHours(t *testing.T) {
	since := time.Date(2026, 8, 12, 10, 20, 0, 0, time.UTC)
	now := time.Date(2026, 8, 12, 12, 40, 0, 0, time.UTC)
	points := []hourPoint{{Hour: time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC), Cost: 1.25}}
	got := fillHourlySeries(since, now, points)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	if got[0]["cost_usd"].(float64) != 0 {
		t.Fatalf("hour 10 should be padded, got %+v", got[0])
	}
	if got[1]["cost_usd"].(float64) != 1.25 {
		t.Fatalf("hour 11 should keep cost, got %+v", got[1])
	}
}
