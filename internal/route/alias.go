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

// Resolve maps alias or concrete model to an ordered provider model chain.
// auto is not implemented in phase 1 — returns a clear error.
func (r *Resolver) Resolve(requested string) (*Resolution, error) {
	if requested == "" {
		return nil, fmt.Errorf("model is required")
	}
	if requested == "auto" {
		return nil, fmt.Errorf("auto routing not enabled yet")
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

	// Concrete model name
	if _, ok := r.cfg.ProviderForModel(requested); !ok {
		return nil, fmt.Errorf("unknown model %q", requested)
	}
	return &Resolution{
		RequestedAlias: requested,
		ResolvedModel:  requested,
		Chain:          []string{requested},
	}, nil
}
