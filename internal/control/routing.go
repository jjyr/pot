package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modeldirectory"
	"github.com/jjyr/pot/pkg/storage"
)

// RoutingPolicy contains only runtime admission limits, never model selection
// preferences. Assistants choose their own explicit routes from saved Providers.
type RoutingPolicy struct {
	Revision       uint64            `json:"revision"`
	ProfileLimits  map[string]uint64 `json:"profile_limits"`
	ProviderLimits map[string]uint64 `json:"provider_limits"`
}

func defaultRoutingPolicy() RoutingPolicy {
	return RoutingPolicy{ProfileLimits: map[string]uint64{}, ProviderLimits: map[string]uint64{}}
}

type routingReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readRouting(ctx context.Context, db routingReader) (RoutingPolicy, error) {
	policy := defaultRoutingPolicy()
	var raw []byte
	err := db.QueryRowContext(ctx, "SELECT body FROM task_routing WHERE singleton=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	err = core.DecodeJSON(raw, &policy)
	return policy, err
}
func (s *Service) Routing(ctx context.Context) (RoutingPolicy, error) {
	return readRouting(ctx, s.Tasks.DB())
}
func (s *Service) SaveRouting(ctx context.Context, policy RoutingPolicy) (RoutingPolicy, error) {
	if policy.Revision >= core.MaxSafeInteger {
		return policy, errors.New("routing revision exceeds supported range")
	}
	profiles, providers := map[string]bool{}, map[string]bool{}
	for _, p := range s.Profiles.Snapshot().Profiles {
		profiles[p.ID] = true
		providers[p.Provider] = true
	}
	for key, limit := range policy.ProfileLimits {
		if !profiles[key] || limit < 1 || limit > 1000 {
			return policy, errors.New("routing requires known profiles and limits1..1000")
		}
	}
	for key, limit := range policy.ProviderLimits {
		if !providers[key] || limit < 1 || limit > 1000 {
			return policy, errors.New("routing requires known providers and limits1..1000")
		}
	}
	if policy.ProfileLimits == nil {
		policy.ProfileLimits = map[string]uint64{}
	}
	if policy.ProviderLimits == nil {
		policy.ProviderLimits = map[string]uint64{}
	}
	err := storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		old, err := readRouting(ctx, tx)
		if err != nil {
			return err
		}
		if old.Revision != policy.Revision {
			return errors.New("routing policy changed; reload before saving")
		}
		policy.Revision++
		raw, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO task_routing(singleton,body)VALUES(1,?)ON CONFLICT(singleton)DO UPDATE SET body=excluded.body", string(raw))
		return err
	})
	if err == nil {
		s.Changes.Notify("settings")
		s.wakeTasks()
	}
	return policy, err
}
func (s *Service) admitTaskModel(ctx context.Context, tx *sql.Tx, spec core.ResolvedSpec) (bool, error) {
	policy, err := readRouting(ctx, tx)
	if err != nil {
		return false, err
	}
	for _, item := range []struct {
		limit      uint64
		key, value string
	}{{policy.ProfileLimits[spec.SourceProfileID], "source_profile_id", spec.SourceProfileID}, {policy.ProviderLimits[spec.Provider], "provider", spec.Provider}} {
		if item.limit == 0 {
			continue
		}
		var active uint64
		err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE phase IN ('queued','running','waiting') AND json_extract(body,'$.launch_sent')=1 AND json_extract(body,'$.config."+item.key+"')=?", item.value).Scan(&active)
		if err != nil {
			return false, err
		}
		if active >= item.limit {
			return false, nil
		}
	}
	return true, nil
}

type ModelCatalogQuery struct {
	Profile *string `json:"profile"`
	Offset  uint64  `json:"offset"`
	Limit   uint64  `json:"limit"`
}

func (s *Service) ModelCatalog(query ModelCatalogQuery) (map[string]any, error) {
	if query.Limit == 0 {
		query.Limit = 20
	}
	query.Limit = min(query.Limit, 100)
	snapshot := s.Profiles.Snapshot()
	sort.Slice(snapshot.Profiles, func(i, j int) bool { return snapshot.Profiles[i].ID < snapshot.Profiles[j].ID })
	rows := []any{}
	for _, p := range snapshot.Profiles {
		if query.Profile != nil && p.ID != *query.Profile {
			continue
		}
		_, err := s.Profiles.Resolve(p.ID)
		eligible := err == nil
		if query.Profile == nil {
			rows = append(rows, map[string]any{"id": p.ID, "name": p.Name, "model": p.Model, "eligible": eligible})
			continue
		}
		models := []modeldirectory.ModelOption{}
		if p.ModelSettings != nil && p.ModelSettings.CatalogProvider != nil {
			for _, directory := range snapshot.Directory {
				if directory.ID == *p.ModelSettings.CatalogProvider {
					models = directory.Models
					break
				}
			}
		}
		configured := append([]string{}, snapshot.ConfiguredModels[p.ID]...)
		sort.Strings(configured)
		row := map[string]any{"id": p.ID, "name": p.Name, "provider": p.Provider, "model": p.Model, "eligible": eligible, "models": pageModels(models, query.Offset, query.Limit), "models_total": len(models), "configured_models": pageModels(configured, query.Offset, query.Limit), "configured_models_total": len(configured), "capabilities": map[string]bool{"native_compaction": true, "safe_checkpoint_resume": true, "live_steering": true, "model_switch": false, "usage_reporting": true, "request_level_budget_enforcement": true, "tool_policy_enforcement": true, "mcp_tools": false, "subagents_disableable": true}}
		if !eligible {
			row["unavailable_reason"] = "Provider configuration unavailable"
		}
		rows = append(rows, row)
	}
	if query.Profile != nil {
		if len(rows) == 0 {
			return nil, errors.New("configured Provider not found")
		}
		return map[string]any{"profiles": rows}, nil
	}
	total := uint64(len(rows))
	var next *uint64
	if query.Offset < total && query.Limit < total-query.Offset {
		n := query.Offset + query.Limit
		next = &n
	}
	return map[string]any{"profiles": pageModels(rows, query.Offset, query.Limit), "total": total, "next_offset": next}, nil
}
func pageModels[T any](items []T, offset, limit uint64) []T {
	start := min(offset, uint64(len(items)))
	end := start + min(limit, uint64(len(items))-start)
	return append([]T{}, items[start:end]...)
}
func (s *Service) modelContext() string {
	snapshot := s.Profiles.Snapshot()
	sort.Slice(snapshot.Profiles, func(i, j int) bool { return snapshot.Profiles[i].ID < snapshot.Profiles[j].ID })
	rows := []any{}
	for _, p := range snapshot.Profiles {
		models := append([]string{}, snapshot.ConfiguredModels[p.ID]...)
		sort.Strings(models)
		_, err := s.Profiles.Resolve(p.ID)
		row := map[string]any{"profile": p.ID, "name": p.Name, "default_model": p.Model, "eligible": err == nil, "models": pageModels(models, 0, 5), "more_models": len(models) > 5}
		candidate := append(append([]any{}, rows...), row)
		encoded, _ := json.Marshal(candidate)
		if len(encoded) > 3500 {
			break
		}
		rows = candidate
	}
	raw, _ := json.Marshal(map[string]any{"profiles": rows})
	return "Configured Provider/model summary (bounded; may omit choices). Choose autonomously; omission uses your active route. Use get_model_catalog for complete configured choices and model facts before selecting an omitted model. Explicit user choices take precedence; unavailable routes fail visibly.\n" + string(raw)
}

func (s *Service) selectTaskMachine(_ context.Context, machine string) (string, error) {
	if machine == "" {
		machine = s.options.RuntimeID
	}
	return machine, nil
}
