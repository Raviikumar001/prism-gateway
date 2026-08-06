package route_test

import (
	"testing"

	"github.com/raviikumar001/prism-gateway/internal/gatewaycfg"
	"github.com/raviikumar001/prism-gateway/internal/route"
)

func TestModelsIncludesAliasesExplicitAndFallbackModels(t *testing.T) {
	cfg := &gatewaycfg.Config{
		Providers: []gatewaycfg.Provider{
			{Name: "alpha", Models: []string{"alpha-explicit"}},
			{Name: "beta"},
		},
		Aliases: map[string]gatewaycfg.Alias{
			"fast": {Primary: "alpha-explicit", Fallbacks: []string{"beta-small"}},
		},
	}
	models := route.NewResolver(cfg).Models()
	got := make(map[string]string, len(models))
	for _, model := range models {
		got[model.ID] = model.OwnedBy
	}
	for id, owner := range map[string]string{
		"fast":           "prism",
		"alpha-explicit": "alpha",
		"beta-small":     "beta",
	} {
		if got[id] != owner {
			t.Fatalf("model %q owner = %q, want %q", id, got[id], owner)
		}
	}
}
