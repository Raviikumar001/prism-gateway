package route_test

import (
	"encoding/json"
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

func TestResolvePromptHonorsRouteByDifficulty(t *testing.T) {
	cfg := &gatewaycfg.Config{
		Providers: []gatewaycfg.Provider{
			{Name: "alpha", Models: []string{"alpha-small", "alpha-large"}},
			{Name: "beta", Models: []string{"beta-code"}},
		},
		Aliases: map[string]gatewaycfg.Alias{
			"fast":  {Primary: "alpha-small"},
			"smart": {Primary: "alpha-large"},
			"code":  {Primary: "beta-code"},
			"auto":  {RouteByDifficulty: json.RawMessage(`{"simple":"code","complex":"smart"}`)},
		},
	}
	resolver := route.NewResolver(cfg)
	got, err := resolver.ResolvePrompt("auto", "What is the capital of France?")
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolvedModel != "beta-code" {
		t.Fatalf("simple auto route = %q, want beta-code from route_by_difficulty", got.ResolvedModel)
	}

	got, err = resolver.ResolvePrompt("auto", "Prove that the square root of 2 is irrational.")
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolvedModel != "alpha-large" {
		t.Fatalf("complex auto route = %q, want alpha-large", got.ResolvedModel)
	}
}
