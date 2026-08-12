package httpapi

import "testing"

func TestShouldBillStreamEstimate(t *testing.T) {
	cases := []struct {
		status         string
		headersWritten bool
		want           bool
	}{
		{status: "ok", headersWritten: true, want: true},
		{status: "ok", headersWritten: false, want: false},
		{status: "client_abort", headersWritten: true, want: true},
		{status: "upstream_error", headersWritten: true, want: true},
		{status: "client_abort", headersWritten: false, want: false},
		{status: "upstream_error", headersWritten: false, want: false},
		{status: "upstream_client_error", headersWritten: false, want: false},
		{status: "gateway_overloaded", headersWritten: false, want: false},
	}
	for _, tc := range cases {
		got := shouldBillStreamEstimate(tc.status, tc.headersWritten)
		if got != tc.want {
			t.Fatalf("status=%s headers=%t: got %t want %t", tc.status, tc.headersWritten, got, tc.want)
		}
	}
}
