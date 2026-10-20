package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/providerallowance"
	"github.com/jjyr/pot/pkg/storage"
)

// ProviderAllowanceMonitorRequest is owner intent. Source and connection identity
// are always resolved by Control, never accepted from this request.
type ProviderAllowanceMonitorRequest struct {
	Enabled   bool    `json:"enabled"`
	Threshold float64 `json:"threshold"`
	Assistant string  `json:"assistant"`
	Revision  int64   `json:"revision"`
}
type ProviderAllowanceMonitor struct {
	Enabled   bool    `json:"enabled"`
	Threshold float64 `json:"threshold"`
	Assistant string  `json:"assistant"`
	Revision  int64   `json:"revision"`
	Status    string  `json:"status"`
	Queued    int     `json:"queued"`
	Attention int     `json:"attention"`
}
type ConnectionAllowanceView struct {
	ProfileID  string                   `json:"profile_id"`
	Snapshot   *core.AllowanceSnapshot  `json:"snapshot"`
	Status     string                   `json:"status"`
	Monitor    ProviderAllowanceMonitor `json:"monitor"`
	Assistants []assistant.Definition   `json:"assistants"`
}
type allowanceMonitorConfig struct {
	Threshold float64 `json:"threshold"`
}
type allowanceMonitorCheckpoint struct {
	Version      int                     `json:"version"`
	RouteID      string                  `json:"route_id"`
	SourceDigest string                  `json:"source_digest"`
	Threshold    float64                 `json:"threshold"`
	Policy       providerallowance.State `json:"policy"`
}

func allowanceInstanceID(profileID, pluginID string) string {
	digest, _ := core.HashJSON(pluginID)
	id := "provider-" + profileID + "-" + digest[:8]
	if !(core.Identifier(id) && len(id) <= 121 && !strings.ContainsRune(id, '.')) {
		digest, _ = core.HashJSON([]string{profileID, pluginID})
		id = "provider-" + digest
	}
	return id
}
func decodeAllowanceConfig(raw json.RawMessage) (allowanceMonitorConfig, error) {
	var c allowanceMonitorConfig
	if err := core.DecodeJSON(raw, &c); err != nil {
		return c, err
	}
	if math.IsNaN(c.Threshold) || math.IsInf(c.Threshold, 0) || c.Threshold <= 0 || c.Threshold > 100 {
		return c, errors.New("invalid allowance threshold")
	}
	return c, nil
}

// ensureProviderAllowanceBindings uses the existing durable background service.
// Owner choices survive refresh, reconnect, and a late first assistant.
func (s *Service) ensureProviderAllowanceBindings(ctx context.Context) error {
	return s.Machines.OwnerAction(func() error {
		definitions, err := s.Registry.Definitions(ctx)
		if err != nil {
			return err
		}
		stored, err := s.Mailbox.Backgrounds(ctx)
		if err != nil {
			return err
		}
		for _, profile := range s.Profiles.Snapshot().Profiles {
			_, _, typ, err := s.Profiles.ProviderConnection(ctx, profile.ID)
			if err != nil || !typ.Capabilities.Allowances {
				continue
			}
			id := allowanceInstanceID(profile.ID, typ.PluginID)
			found := false
			for _, b := range stored {
				if b.ID != id {
					continue
				}
				found = true
				if b.Assistant == "" && len(definitions) > 0 {
					b.Assistant = definitions[0].ID
					if err = s.Mailbox.ConfigureBackground(ctx, b); err != nil {
						return err
					}
				}
			}
			if found {
				continue
			}
			target := ""
			if len(definitions) > 0 {
				target = definitions[0].ID
			}
			config, _ := json.Marshal(allowanceMonitorConfig{Threshold: 90})
			if err = s.Mailbox.EnsureBackgroundInstance(ctx, assistant.BackgroundBinding{ID: id, PluginID: typ.PluginID, ConnectionID: profile.ID, Enabled: true, Assistant: target, Config: config}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) allowanceBinding(ctx context.Context, profileID string) (assistant.BackgroundBinding, error) {
	profile, route, typ, connectionErr := s.Profiles.ProviderConnection(ctx, profileID)
	if profile.ID == "" {
		return assistant.BackgroundBinding{}, connectionErr
	}
	pluginID := typ.PluginID
	if pluginID == "" && route.Source != nil {
		pluginID = route.Source.ID
	}
	stored, err := s.Mailbox.Backgrounds(ctx)
	if err != nil {
		return assistant.BackgroundBinding{}, err
	}
	for _, b := range stored {
		if b.ConnectionID == profileID && b.PluginID == pluginID {
			return b, nil
		}
	}
	if connectionErr != nil {
		return assistant.BackgroundBinding{}, connectionErr
	}
	return assistant.BackgroundBinding{}, errors.New("connection allowances unavailable")
}

func (s *Service) ConnectionAllowances(ctx context.Context, profileID string) (ConnectionAllowanceView, error) {
	return s.connectionAllowances(ctx, profileID, false)
}
func (s *Service) RefreshConnectionAllowances(ctx context.Context, profileID string) (ConnectionAllowanceView, error) {
	return s.connectionAllowances(ctx, profileID, true)
}
func (s *Service) connectionAllowances(ctx context.Context, profileID string, refresh bool) (ConnectionAllowanceView, error) {
	view := ConnectionAllowanceView{ProfileID: profileID}
	if err := s.ensureProviderAllowanceBindings(ctx); err != nil {
		return view, err
	}
	b, err := s.allowanceBinding(ctx, profileID)
	if err != nil {
		return view, err
	}
	config, err := decodeAllowanceConfig(b.Config)
	if err != nil {
		return view, err
	}
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return view, err
	}
	view.Assistants = definitions
	snapshot, readErr := s.Profiles.ReadAllowances(ctx, profileID, refresh)
	view.Status = "ready"
	if readErr != nil {
		view.Status = allowanceReadStatus(readErr)
	} else {
		view.Snapshot = &snapshot
	}
	status := b.Status
	if !b.Enabled {
		status = "disabled"
	} else if b.Assistant == "" {
		status = "waiting_assistant"
	} else if s.validateNotificationTarget(ctx, b.Assistant) != nil {
		status = "assistant_missing"
	} else if readErr != nil {
		status = view.Status
	}
	view.Monitor = ProviderAllowanceMonitor{Enabled: b.Enabled, Threshold: config.Threshold, Assistant: b.Assistant, Revision: b.Revision, Status: status, Queued: b.Queued, Attention: b.Attention}
	return view, nil
}
func (s *Service) ConfigureConnectionAllowance(ctx context.Context, profileID string, r ProviderAllowanceMonitorRequest) error {
	if err := s.ensureProviderAllowanceBindings(ctx); err != nil {
		return err
	}
	return s.Machines.OwnerAction(func() error {
		b, err := s.allowanceBinding(ctx, profileID)
		if err != nil {
			return err
		}
		if r.Assistant != "" {
			if err = s.validateNotificationTarget(ctx, r.Assistant); err != nil {
				return err
			}
		}
		config, _ := json.Marshal(allowanceMonitorConfig{Threshold: r.Threshold})
		if _, err = decodeAllowanceConfig(config); err != nil {
			return err
		}
		b.Enabled = r.Enabled
		b.Assistant = r.Assistant
		b.Revision = r.Revision
		b.Config = config
		if err = s.Mailbox.ConfigureBackground(ctx, b); err != nil {
			return err
		}
		s.Changes.Notify("provider-allowances")
		return nil
	})
}
func allowanceReadStatus(err error) string {
	var status *core.ModelProviderError
	if errors.As(err, &status) {
		return status.Code
	}
	return "unavailable"
}

// runProviderAllowance is called under pluginMu with one job per immutable
// connection/package instance. Credentials and adapter reads remain Control-local.
func (s *Service) runProviderAllowance(ctx context.Context, b assistant.BackgroundBinding, now int64) {
	before, route, typ, err := s.Profiles.ProviderConnection(ctx, b.ConnectionID)
	if err != nil || !typ.Capabilities.Allowances || typ.PluginID != b.PluginID {
		_ = s.Mailbox.BackgroundError(ctx, b, "unavailable", "", now)
		return
	}
	config, err := decodeAllowanceConfig(b.Config)
	if err != nil {
		_ = s.Mailbox.BackgroundError(ctx, b, "unavailable", "", now)
		return
	}
	checkpoint := allowanceMonitorCheckpoint{Version: 1}
	if len(b.State) > 0 && string(b.State) != "null" {
		if err = core.DecodeJSON(b.State, &checkpoint); err != nil || checkpoint.Version != 1 {
			_ = s.Mailbox.BackgroundError(ctx, b, "unavailable", "", now)
			return
		}
	}
	if checkpoint.RouteID != route.ID || checkpoint.SourceDigest != typ.Identity.Digest || checkpoint.Threshold != config.Threshold {
		checkpoint.Policy = providerallowance.State{Version: 1, Episode: checkpoint.Policy.Episode}
	}
	checkpoint.RouteID = route.ID
	checkpoint.SourceDigest = typ.Identity.Digest
	checkpoint.Threshold = config.Threshold
	exchangeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	snapshot, readErr := s.Profiles.ReadAllowances(exchangeCtx, b.ConnectionID, false)
	status := "ready"
	events := []core.PluginEvent{}
	if readErr != nil {
		checkpoint.Policy = providerallowance.Unavailable(checkpoint.Policy)
		status = allowanceReadStatus(readErr)
		if status == "unavailable" || status == "invalid_response" {
			status = "backoff"
		}
	} else if b.Assistant != "" && s.validateNotificationTarget(ctx, b.Assistant) == nil {
		transition, policyErr := providerallowance.Evaluate(snapshot, checkpoint.Policy, providerallowance.Options{Now: time.UnixMilli(now), Threshold: config.Threshold})
		if policyErr != nil {
			checkpoint.Policy = providerallowance.Unavailable(checkpoint.Policy)
			status = "backoff"
		} else {
			checkpoint.Policy = transition.State
			for _, alert := range transition.Alerts {
				// Account labels remain reference data; no account or window identity is invented.
				source := alert.Account
				source.Windows = []core.AllowanceWindow{alert.Window}
				body, _ := json.Marshal(map[string]any{"type": "provider_allowance_threshold", "connection_name": before.Name, "threshold": config.Threshold, "used": alert.Used, "source": source})
				title := before.Name
				if len(title) > 512 {
					title = title[:512]
					for !utf8.ValidString(title) {
						title = title[:len(title)-1]
					}
				}
				events = append(events, core.PluginEvent{ID: fmt.Sprintf("allowance-%d", alert.Episode), Title: title, Body: string(body), OccurredAt: time.UnixMilli(now).UTC().Format(time.RFC3339)})
			}
		}
	} else {
		// No target cannot consume an alert episode. A later first assistant sees
		// the next valid high observation and receives the ordinary warning.
		checkpoint.Policy = providerallowance.Unavailable(checkpoint.Policy)
		status = "baseline"
	}
	state, _ := json.Marshal(checkpoint)
	response := core.BackgroundResponse{State: state, DelaySeconds: 60, Status: status, Events: events}
	// Serialize the final source fence with owner edits; Mailbox atomically fences
	// the binding revision/route and accepts its checkpoint and complete event set.
	err = s.Machines.OwnerAction(func() error {
		current, currentRoute, currentType, e := s.Profiles.ProviderConnection(exchangeCtx, b.ConnectionID)
		if e != nil {
			return e
		}
		if current.Revision != before.Revision || currentRoute.ID != route.ID || currentType.Identity != typ.Identity || !currentType.Capabilities.Allowances || currentType.PluginID != b.PluginID {
			return errors.New("stale provider allowance source")
		}
		if len(events) > 0 {
			if e = s.validateNotificationTarget(exchangeCtx, b.Assistant); e != nil {
				return e
			}
		}
		return s.Mailbox.CommitBackground(exchangeCtx, b, typ.Identity, response, now)
	})
	if err == nil {
		s.Changes.Notify("provider-allowances")
	} else {
		_ = s.Mailbox.BackgroundError(ctx, b, "unavailable", "", storage.Now())
	}
}
