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
	if alias, ok := r.cfg.Aliases[requested]; ok {
		routes, err := alias.DifficultyRoutes()
		if err != nil {
			return nil, fmt.Errorf("alias %q route_by_difficulty: %w", requested, err)
		}
		if len(routes) > 0 {
			return r.resolveByDifficulty(requested, routes, prompt)
		}
	}
	if requested == "auto" {
		return r.resolveByDifficulty("auto", nil, prompt)
	}
	return r.Resolve(requested)
}

func (r *Resolver) resolveByDifficulty(requested string, routes map[string]string, prompt string) (*Resolution, error) {
	d := Classify(prompt, DefaultAutoThreshold)
	aliasName := difficultyTarget(routes, d.Tier)
	alias, ok := r.cfg.Aliases[aliasName]
	if !ok || alias.Primary == "" {
		return nil, fmt.Errorf("auto resolved to %q but alias missing", aliasName)
	}
	chain := append([]string{alias.Primary}, alias.Fallbacks...)
	reason := d.Reason
	if aliasName != d.Tier {
		reason = fmt.Sprintf("%s mapped via %s.%s=%s", d.Reason, requested, d.Tier, aliasName)
	}
	return &Resolution{
		RequestedAlias: requested,
		ResolvedModel:  alias.Primary,
		Chain:          chain,
		RouteReason:    reason,
	}, nil
}

func difficultyTarget(routes map[string]string, tier string) string {
	if target := routes[tier]; target != "" {
		return target
	}
	switch tier {
	case "fast":
		if target := routes["simple"]; target != "" {
			return target
		}
	case "smart":
		if target := routes["complex"]; target != "" {
			return target
		}
	}
	return tier
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
