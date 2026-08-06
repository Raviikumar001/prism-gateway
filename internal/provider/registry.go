package provider

import (
	"fmt"
	"time"

	"github.com/raviikumar001/prism-gateway/internal/gatewaycfg"
)

type Registry struct {
	byName map[string]*OpenAICompat
	cfg    *gatewaycfg.Config
}

func NewRegistry(cfg *gatewaycfg.Config, timeout time.Duration) *Registry {
	r := &Registry{
		byName: make(map[string]*OpenAICompat, len(cfg.Providers)),
		cfg:    cfg,
	}
	for _, p := range cfg.Providers {
		r.byName[p.Name] = NewOpenAICompat(p.Name, p.BaseURL, p.APIKey, timeout, p.ExtraHeaders)
	}
	return r
}

func (r *Registry) Client(providerName string) (*OpenAICompat, error) {
	c, ok := r.byName[providerName]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q", providerName)
	}
	return c, nil
}

func (r *Registry) ClientForModel(model string) (*OpenAICompat, string, error) {
	p, ok := r.cfg.ProviderForModel(model)
	if !ok {
		return nil, "", fmt.Errorf("no provider for model %q", model)
	}
	c, err := r.Client(p.Name)
	if err != nil {
		return nil, "", err
	}
	return c, p.Name, nil
}
