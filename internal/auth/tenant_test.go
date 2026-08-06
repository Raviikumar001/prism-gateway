package auth_test

import (
	"testing"

	"github.com/raviikumar001/prism-gateway/internal/auth"
)

func TestAllowsModel(t *testing.T) {
	tn := &auth.Tenant{Allowlist: []string{"fast", "auto"}}
	if !tn.AllowsModel("fast") {
		t.Fatal("expected fast allowed")
	}
	if tn.AllowsModel("smart") {
		t.Fatal("expected smart denied")
	}
	star := &auth.Tenant{Allowlist: []string{"*"}}
	if !star.AllowsModel("openai/gpt-4o-mini") {
		t.Fatal("expected wildcard to allow any model id")
	}
}

func TestBearerToken(t *testing.T) {
	k, err := auth.BearerToken("Bearer prism-sk-x")
	if err != nil || k != "prism-sk-x" {
		t.Fatalf("got %q %v", k, err)
	}
	if _, err := auth.BearerToken(""); err != auth.ErrMissingKey {
		t.Fatalf("expected missing, got %v", err)
	}
}
