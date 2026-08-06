package route

import (
	"fmt"

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
