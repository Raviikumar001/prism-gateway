package gatewaycfg

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Providers []Provider         `json:"providers"`
	Aliases   map[string]Alias   `json:"model_aliases"`
	Retry     Retry              `json:"retry"`
}

type Provider struct {
	Name         string            `json:"name"`
	BaseURL      string            `json:"base_url"`
	APIKey       string            `json:"api_key"`
	APIKeyEnv    string            `json:"api_key_env"`
	Models       []string          `json:"models"`
	ExtraHeaders map[string]string `json:"extra_headers"`
}

type Alias struct {
	Primary           string          `json:"primary"`
	Fallbacks         []string        `json:"fallbacks"`
	RouteByDifficulty json.RawMessage `json:"route_by_difficulty"`
}

type Retry struct {
	MaxAttempts       int `json:"max_attempts"`
	InitialBackoffMs  int `json:"initial_backoff_ms"`
	BackoffMultiplier int `json:"backoff_multiplier"`
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

// ApplyEnvSecrets fills empty api_key fields from api_key_env / known env vars.
func (c *Config) ApplyEnvSecrets() error {
	for i := range c.Providers {
		p := &c.Providers[i]
		if p.APIKey != "" {
			continue
		}
		envName := p.APIKeyEnv
		if envName == "" {
			switch strings.ToLower(p.Name) {
			case "cerebras":
				envName = "CEREBRAS_API_KEY"
			case "openrouter":
				envName = "OPENROUTER_API_KEY"
			}
		}
		if envName == "" {
			continue
		}
		if v := os.Getenv(envName); v != "" {
			p.APIKey = v
		}
	}
	return nil
}

func (c *Config) RequireAPIKeys() error {
	var missing []string
	for _, p := range c.Providers {
		if p.APIKey == "" {
			label := p.APIKeyEnv
			if label == "" {
				label = p.Name
			}
			missing = append(missing, label)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing API keys for providers: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (c *Config) ProviderByName(name string) (Provider, bool) {
	for _, p := range c.Providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// ProviderForModel resolves which provider owns a model id.
// Prefers explicit provider.models lists; falls back to mock-style "name-" prefix.
func (c *Config) ProviderForModel(model string) (Provider, bool) {
	for _, p := range c.Providers {
		for _, m := range p.Models {
			if m == model {
				return p, true
			}
		}
	}
	for _, p := range c.Providers {
		if strings.HasPrefix(model, p.Name+"-") || model == p.Name {
			return p, true
		}
		// openrouter/google/... style prefix
		if strings.HasPrefix(model, p.Name+"/") {
			return p, true
		}
	}
	return Provider{}, false
}
