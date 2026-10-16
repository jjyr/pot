package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/storage"
	"github.com/jjyr/pot/internal/usage"
	"sort"
)

type ProviderAllowance struct {
	usage.Quota
	Enforcement string `json:"enforcement"`
}
type SaveProviderAllowance struct {
	Provider string       `json:"provider" required:"true"`
	Revision int64        `json:"revision" required:"true"`
	Policy   usage.Policy `json:"policy" required:"true"`
}

func (s *Service) ProviderAllowances(ctx context.Context) (map[string]any, error) {
	ids := map[string]bool{}
	for _, p := range s.Profiles.Snapshot().Profiles {
		ids[p.Provider] = true
	}
	ordered := []string{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	result := []ProviderAllowance{}
	for _, id := range ordered {
		quota, err := s.Usage.ProviderQuota(ctx, id, storage.Now())
		if err != nil {
			return nil, err
		}
		result = append(result, ProviderAllowance{quota, "pre_request"})
	}
	return map[string]any{"providers": result}, nil
}
func (s *Service) SaveProviderAllowance(ctx context.Context, r SaveProviderAllowance) (usage.Quota, error) {
	configured := false
	for _, p := range s.Profiles.Snapshot().Profiles {
		if p.Provider == r.Provider {
			configured = true
			break
		}
	}
	if !configured {
		return usage.Quota{}, errors.New("only configured Providers support an allowance")
	}
	if err := s.Usage.SetProviderPolicy(ctx, r.Provider, r.Revision, r.Policy); err != nil {
		return usage.Quota{}, err
	}
	s.Changes.Notify("usage")
	s.wakeTasks()
	return s.Usage.ProviderQuota(ctx, r.Provider, storage.Now())
}
