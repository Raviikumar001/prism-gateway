package gatewaycfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderForModelExplicitList(t *testing.T) {
	cfg := &Config{
		Providers: []Provider{
			{Name: "cerebras", Models: []string{"gemma-4-31b", "gpt-oss-120b"}},
			{Name: "openrouter", Models: []string{"mistralai/mistral-nemo", "google/gemma-4-31b-it", "openai/gpt-oss-120b"}},
		},
	}
	cases := map[string]string{
		"gemma-4-31b":            "cerebras",
		"gpt-oss-120b":           "cerebras",
		"mistralai/mistral-nemo": "openrouter",
		"google/gemma-4-31b-it":  "openrouter",
		"openai/gpt-oss-120b":    "openrouter",
	}
	for model, want := range cases {
		p, ok := cfg.ProviderForModel(model)
		if !ok {
			t.Fatalf("expected provider for %q", model)
		}
		if p.Name != want {
			t.Fatalf("model %q: got provider %q want %q", model, p.Name, want)
		}
	}
}

func TestProviderForModelPrefixFallback(t *testing.T) {
	cfg := &Config{
		Providers: []Provider{
			{Name: "alpha"},
			{Name: "beta"},
		},
	}
	p, ok := cfg.ProviderForModel("alpha-small")
	if !ok || p.Name != "alpha" {
		t.Fatalf("prefix fallback failed: ok=%v name=%q", ok, p.Name)
	}
}

func TestApplyEnvSecrets(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "cb-test")
	t.Setenv("OPENROUTER_API_KEY", "or-test")
	cfg := &Config{
		Providers: []Provider{
			{Name: "cerebras", APIKeyEnv: "CEREBRAS_API_KEY"},
			{Name: "openrouter", APIKeyEnv: "OPENROUTER_API_KEY"},
		},
	}
	if err := cfg.ApplyEnvSecrets(); err != nil {
		t.Fatal(err)
	}
	if cfg.Providers[0].APIKey != "cb-test" || cfg.Providers[1].APIKey != "or-test" {
		t.Fatalf("secrets not applied: %+v", cfg.Providers)
	}
}

func TestLoadLiveConfig(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// tests run from package dir; live file is at repo data/
	path := filepath.Join(root, "..", "..", "data", "gateway_config.live.json")
	if _, err := os.Stat(path); err != nil {
		t.Skip("live config not found:", path)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) < 2 {
		t.Fatalf("expected live providers, got %d", len(cfg.Providers))
	}
	if _, ok := cfg.ProviderForModel("gpt-oss-120b"); !ok {
		t.Fatal("gpt-oss-120b not mapped")
	}
	if _, ok := cfg.ProviderForModel("mistralai/mistral-nemo"); !ok {
		t.Fatal("openrouter mistral not mapped")
	}
	if _, ok := cfg.ProviderForModel("openai/gpt-5.6-sol"); !ok {
		t.Fatal("openrouter sol not mapped")
	}
	if _, ok := cfg.ProviderForModel("anthropic/claude-fable-5"); !ok {
		t.Fatal("openrouter fable not mapped")
	}
	if cfg.Aliases["sol"].Primary != "openai/gpt-5.6-sol" {
		t.Fatalf("sol alias = %+v", cfg.Aliases["sol"])
	}
	if cfg.Aliases["fable"].Primary != "anthropic/claude-fable-5" {
		t.Fatalf("fable alias = %+v", cfg.Aliases["fable"])
	}
}

func TestValidateRejectsUnreachableFallback(t *testing.T) {
	cfg := &Config{
		Providers: []Provider{
			{Name: "one", BaseURL: "http://one", Models: []string{"one-a"}},
			{Name: "two", BaseURL: "http://two", Models: []string{"two-a"}},
		},
		Aliases: map[string]Alias{
			"fast": {Primary: "one-a", Fallbacks: []string{"two-a"}},
		},
		Retry: Retry{MaxAttempts: 1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected max_attempts validation error")
	}
}

func TestValidateRejectsAmbiguousModelOwner(t *testing.T) {
	cfg := &Config{
		Providers: []Provider{
			{Name: "one", BaseURL: "http://one", Models: []string{"shared"}},
			{Name: "two", BaseURL: "http://two", Models: []string{"shared"}},
		},
		Retry: Retry{MaxAttempts: 1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate model owner validation error")
	}
}
