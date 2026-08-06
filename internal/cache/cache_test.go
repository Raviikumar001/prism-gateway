package cache_test

import (
	"testing"

	"github.com/raviikumar001/prism-gateway/internal/cache"
	"github.com/raviikumar001/prism-gateway/internal/embed"
)

func TestNormalizeStable(t *testing.T) {
	a := cache.Normalize([]string{"  Hello   World  "})
	b := cache.Normalize([]string{"hello world"})
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
}

func TestPromptHashTenantIsolation(t *testing.T) {
	n := "reset password"
	h1 := cache.PromptHash("key-a", n)
	h2 := cache.PromptHash("key-b", n)
	if h1 == h2 {
		t.Fatal("hashes must differ across tenants")
	}
}

func TestSemanticThresholdNearMiss(t *testing.T) {
	e := embed.NewHashEmbedder()
	a := e.Embed(cache.Normalize([]string{"How do I reset my password on the dashboard?"}))
	near := e.Embed(cache.Normalize([]string{"How do I reset my two-factor authentication on the dashboard?"}))
	para := e.Embed(cache.Normalize([]string{"What are the steps to reset my dashboard password?"}))
	if embed.Cosine(a, para) < 0.8 {
		t.Fatalf("expected paraphrase >= 0.8, got %f", embed.Cosine(a, para))
	}
	if embed.Cosine(a, near) >= embed.Cosine(a, para) {
		t.Fatalf("near-miss should rank below paraphrase")
	}
}
