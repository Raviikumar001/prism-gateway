package httpapi

import (
	"net/url"
	"testing"
	"time"
)

func TestParseUsageWindowDefaultsToCurrentMonth(t *testing.T) {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	window, err := parseUsageWindow(url.Values{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if window.PrimaryMonth != "202608" || len(window.Months) != 1 {
		t.Fatalf("unexpected window: %+v", window)
	}
	if window.From.Format("2006-01-02") != "2026-08-01" || window.To.Format("2006-01-02") != "2026-08-31" {
		t.Fatalf("unexpected bounds: from=%s to=%s", window.From, window.To)
	}
}

func TestParseUsageWindowFromToAcrossMonths(t *testing.T) {
	q := url.Values{}
	q.Set("from", "2026-07-15")
	q.Set("to", "2026-08-02")
	window, err := parseUsageWindow(q, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got := window.Months; len(got) != 2 || got[0] != "202607" || got[1] != "202608" {
		t.Fatalf("months=%v", got)
	}
}

func TestParseUsageWindowRejectsMixedParams(t *testing.T) {
	q := url.Values{}
	q.Set("month", "202608")
	q.Set("from", "2026-08-01")
	if _, err := parseUsageWindow(q, time.Now().UTC()); err == nil {
		t.Fatal("expected error")
	}
}
