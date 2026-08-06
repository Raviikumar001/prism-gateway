package route_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/raviikumar001/prism-gateway/internal/route"
)

func TestClassifyTraps(t *testing.T) {
	cases := []struct {
		prompt string
		want   string
	}{
		{"Prove that the square root of 2 is irrational.", "smart"},
		{"What is the capital of France?", "fast"},
		{"Here is our on-call roster for the next two weeks: Monday - Priya, Tuesday - Chen. Who is on call this Thursday?", "fast"},
	}
	for _, tc := range cases {
		d := route.Classify(tc.prompt, route.DefaultAutoThreshold)
		if d.Tier != tc.want {
			t.Fatalf("prompt %q: got %s want %s (%s)", tc.prompt, d.Tier, tc.want, d.Reason)
		}
	}
}

func TestRoutingEvalBeatsLengthBaseline(t *testing.T) {
	path := filepath.Join("..", "..", "data", "routing_eval.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Skip("routing_eval.jsonl not found")
	}
	defer f.Close()

	type row struct {
		ID           string `json:"id"`
		ExpectedTier string `json:"expected_tier"`
		Prompt       string `json:"prompt"`
	}

	var featureOK, lengthOK, total int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		total++
		if route.Classify(r.Prompt, route.DefaultAutoThreshold).Tier == r.ExpectedTier {
			featureOK++
		}
		if route.LengthOnlyBaseline(r.Prompt, 40) == r.ExpectedTier {
			lengthOK++
		}
	}
	if total == 0 {
		t.Fatal("empty eval")
	}
	featAcc := float64(featureOK) / float64(total)
	lenAcc := float64(lengthOK) / float64(total)
	t.Logf("feature=%.2f (%d/%d) length=%.2f (%d/%d)", featAcc, featureOK, total, lenAcc, lengthOK, total)
	if featAcc < 0.85 {
		t.Fatalf("feature router accuracy too low: %.2f", featAcc)
	}
	if featAcc <= lenAcc {
		t.Fatalf("feature router (%.2f) did not beat length baseline (%.2f)", featAcc, lenAcc)
	}
}
