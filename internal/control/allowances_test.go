package control

import (
	"context"
	"github.com/jjyr/pot/internal/usage"
	"testing"
)

func TestConfiguredProviderAllowancesRetainCASAndEnforceOriginalRoute(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "provider", ProfileID: "profile", Name: "Fixture", Model: "model", Endpoint: "https://example.test", Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 20})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.ProviderAllowances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items := initial["providers"].([]ProviderAllowance)
	if len(items) != 1 || items[0].Provider != profile.Provider || items[0].Revision != 0 || items[0].Enforcement != "pre_request" {
		t.Fatal(initial)
	}
	policy := usage.Policy{Period: "day", Limit: 100, Timezone: "UTC", AutoResume: false}
	current, err := s.SaveProviderAllowance(ctx, SaveProviderAllowance{profile.Provider, 0, policy})
	if err != nil || current.Revision != 1 || current.Remaining != 100 {
		t.Fatal(current, err)
	}
	if _, err = s.SaveProviderAllowance(ctx, SaveProviderAllowance{profile.Provider, 0, policy}); err == nil {
		t.Fatal("stale Provider allowance edit accepted")
	}
	if _, err = s.SaveProviderAllowance(ctx, SaveProviderAllowance{"unknown", 0, policy}); err == nil {
		t.Fatal("unknown Provider allowance accepted")
	}
	r := usage.ReserveRequest{ID: "request", Scope: "main", Turn: "turn", AtMS: current.ResetAt - 1, Model: "model", InputEstimate: 80, MaxOutput: 100, Route: &usage.Route{Provider: profile.Provider, Generation: 1, SpecHash: "fixed", Agent: "assistant", ProfileID: profile.ID}}
	reserved, err := s.Usage.ReserveRouted(ctx, r)
	if err != nil || reserved == nil || reserved.OutputCap != 20 {
		t.Fatal(reserved, err)
	}
}
