package gatewaycfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderForModelExplicitList(t *testing.T) {
	cfg := &Config{
		Providers: []Provider{
			{Name: "cerebras", Models: []string{"gpt-oss-120b", "gemma-4-31b"}},
			{Name: "openrouter", Models: []string{"meta-llama/llama-3.1-8b-instruct", "openai/gpt-4o-mini"}},
		},
	}
	cases := map[string]string{
		"gpt-oss-120b":                      "cerebras",
		"gemma-4-31b":                       "cerebras",
		"meta-llama/llama-3.1-8b-instruct":  "openrouter",
		"openai/gpt-4o-mini":                "openrouter",
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
	if _, ok := cfg.ProviderForModel("meta-llama/llama-3.1-8b-instruct"); !ok {
		t.Fatal("openrouter model not mapped")
	}
}
