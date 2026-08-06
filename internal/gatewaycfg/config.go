package gatewaycfg

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Providers []Provider `json:"providers"`
	Aliases   map[string]Alias `json:"model_aliases"`
	Retry     Retry `json:"retry"`
}

type Provider struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
}

type Alias struct {
	Primary   string   `json:"primary"`
	Fallbacks []string `json:"fallbacks"`
	// auto-only fields ignored here for phase 1
	RouteByDifficulty json.RawMessage `json:"route_by_difficulty"`
}

type Retry struct {
	MaxAttempts        int `json:"max_attempts"`
	InitialBackoffMs   int `json:"initial_backoff_ms"`
	BackoffMultiplier  int `json:"backoff_multiplier"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gateway config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse gateway config: %w", err)
	}
	if len(cfg.Providers) == 0 {
		return nil, fmt.Errorf("gateway config has no providers")
	}
	if cfg.Retry.MaxAttempts == 0 {
		cfg.Retry.MaxAttempts = 3
	}
	if cfg.Retry.InitialBackoffMs == 0 {
		cfg.Retry.InitialBackoffMs = 200
	}
	if cfg.Retry.BackoffMultiplier == 0 {
		cfg.Retry.BackoffMultiplier = 2
	}
	for i := range cfg.Providers {
		cfg.Providers[i].BaseURL = strings.TrimRight(cfg.Providers[i].BaseURL, "/")
	}
	return &cfg, nil
}

func (c *Config) ProviderByName(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// ProviderForModel maps alpha-small -> alpha using provider name prefix convention.
func (c *Config) ProviderForModel(model string) (Provider, bool) {
	for _, p := range c.Providers {
		if strings.HasPrefix(model, p.Name+"-") || model == p.Name {
			return p, true
		}
	}
	return Provider{}, false
}
