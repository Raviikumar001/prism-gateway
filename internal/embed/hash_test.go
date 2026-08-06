package embed_test

import (
	"testing"

	"github.com/raviikumar001/prism-gateway/internal/embed"
)

func TestParaphraseCloserThanNearMiss(t *testing.T) {
	e := embed.NewHashEmbedder()
	a := e.Embed("How do I reset my password on the dashboard?")
	b := e.Embed("What are the steps to reset my dashboard password?")
	c := e.Embed("How do I reset my two-factor authentication on the dashboard?")

	simAB := embed.Cosine(a, b)
	simAC := embed.Cosine(a, c)
	if simAB < 0.8 {
		t.Fatalf("paraphrase similarity too low: %f", simAB)
	}
	if simAC >= simAB {
		t.Fatalf("near-miss should be less similar than paraphrase: para=%f near=%f", simAB, simAC)
	}
}
