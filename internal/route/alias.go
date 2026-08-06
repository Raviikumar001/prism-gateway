package route

import (
	"fmt"
	"sort"

	"github.com/raviikumar001/prism-gateway/internal/gatewaycfg"
)

type Resolution struct {
	RequestedAlias string
	ResolvedModel  string
	Chain          []string // primary + fallbacks
	RouteReason    string
}

type Resolver struct {
	cfg *gatewaycfg.Config
}

type ModelInfo struct {
	ID      string
	OwnedBy string
}

func NewResolver(cfg *gatewaycfg.Config) *Resolver {
	return &Resolver{cfg: cfg}
}

// ResolvePrompt resolves aliases including auto (needs prompt text for classification).
func (r *Resolver) ResolvePrompt(requested, prompt string) (*Resolution, error) {
	if requested == "" {
		return nil, fmt.Errorf("model is required")
	}
	if requested == "auto" {
		d := Classify(prompt, DefaultAutoThreshold)
		aliasName := d.Tier
		alias, ok := r.cfg.Aliases[aliasName]
		if !ok || alias.Primary == "" {
			return nil, fmt.Errorf("auto resolved to %q but alias missing", aliasName)
		}
		chain := append([]string{alias.Primary}, alias.Fallbacks...)
		return &Resolution{
			RequestedAlias: "auto",
			ResolvedModel:  alias.Primary,
			Chain:          chain,
			RouteReason:    d.Reason,
		}, nil
	}
	return r.Resolve(requested)
}

// Resolve maps alias or concrete model to an ordered provider model chain.
func (r *Resolver) Resolve(requested string) (*Resolution, error) {
	if requested == "" {
		return nil, fmt.Errorf("model is required")
	}
	if requested == "auto" {
		return nil, fmt.Errorf("auto requires prompt; use ResolvePrompt")
	}

	if alias, ok := r.cfg.Aliases[requested]; ok {
		if alias.Primary == "" {
			return nil, fmt.Errorf("alias %q has no primary", requested)
		}
		chain := append([]string{alias.Primary}, alias.Fallbacks...)
		return &Resolution{
			RequestedAlias: requested,
			ResolvedModel:  alias.Primary,
			Chain:          chain,
		}, nil
	}

	if _, ok := r.cfg.ProviderForModel(requested); !ok {
		return nil, fmt.Errorf("unknown model %q", requested)
	}
	return &Resolution{
		RequestedAlias: requested,
		ResolvedModel:  requested,
		Chain:          []string{requested},
	}, nil
}

func (r *Resolver) Models() []ModelInfo {
	seen := make(map[string]ModelInfo)
	for name, alias := range r.cfg.Aliases {
		seen[name] = ModelInfo{ID: name, OwnedBy: "prism"}
		for _, id := range append([]string{alias.Primary}, alias.Fallbacks...) {
			if id == "" {
				continue
			}
			if provider, ok := r.cfg.ProviderForModel(id); ok {
				seen[id] = ModelInfo{ID: id, OwnedBy: provider.Name}
			}
		}
	}
	for _, provider := range r.cfg.Providers {
		for _, model := range provider.Models {
			seen[model] = ModelInfo{ID: model, OwnedBy: provider.Name}
		}
	}
	out := make([]ModelInfo, 0, len(seen))
	for _, model := range seen {
		out = append(out, model)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Resolver) Model(id string) (ModelInfo, bool) {
	for _, model := range r.Models() {
		if model.ID == id {
			return model, true
		}
	}
	return ModelInfo{}, false
}
