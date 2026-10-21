package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/pluginhost"
	"github.com/jjyr/pot/pkg/storage"
)

func (s *Service) backgroundPackage(id string) (plugin.Package, error) {
	packages, err := s.installedPlugins()
	if err != nil {
		return plugin.Package{}, err
	}
	for _, p := range packages {
		if p.Manifest.ID == id && p.Manifest.Contributes.Background != nil {
			enabled, err := s.enabled(context.Background(), "plugins", id)
			if err != nil {
				return plugin.Package{}, err
			}
			if !enabled {
				return plugin.Package{}, errors.New("plugin disabled")
			}
			return p, nil
		}
	}
	return plugin.Package{}, errors.New("background plugin unavailable")
}
func (s *Service) pluginLoop() {
	defer s.pluginWG.Done()
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
			s.runBackgrounds(s.ctx, storage.Now())
		}
	}
}

type backgroundStream struct {
	pluginID string
	revision int64
	accounts string
	digest   string
	cancel   context.CancelFunc
}

func (s *Service) runBackgrounds(ctx context.Context, now int64) {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	if err := s.ensurePluginNotifications(ctx); err != nil {
		return
	}
	if err := s.ensureProviderAllowanceBindings(ctx); err != nil {
		return
	}
	bindings, err := s.Mailbox.Backgrounds(ctx)
	if err != nil {
		return
	}
	if s.pluginStreams == nil {
		s.pluginStreams = map[string]backgroundStream{}
	}
	catalogNow, catalogErr := s.Integrations.List(ctx)
	catalogJSON, _ := json.Marshal(catalogNow)
	for id, active := range s.pluginStreams {
		valid := false
		for _, b := range bindings {
			if b.ID == id && b.Enabled && b.Revision == active.revision {
				valid = true
			}
		}
		p, e := s.backgroundPackage(active.pluginID)
		identity, identityErr := p.BackgroundIdentity()
		if !valid || catalogErr != nil || string(catalogJSON) != active.accounts || e != nil || identityErr != nil || identity.Digest != active.digest {
			active.cancel()
		}
	}
	for _, b := range bindings {
		if _, active := s.pluginStreams[b.ID]; active {
			continue
		}
		if !b.Enabled || b.Next > now {
			continue
		}
		if b.ConnectionID != "" {
			s.runProviderAllowance(ctx, b, now)
			continue
		}
		p, err := s.backgroundPackage(b.PluginID)
		status := "unavailable"
		if err == nil {
			b.EventRouting = p.Manifest.Contributes.Background.EventRouting
			if !b.EventRouting {
				err = s.validateNotificationTarget(ctx, b.Assistant)
			}
		}
		if err == nil {
			streaming := p.Manifest.Contributes.Background.Mode == "stream"
			exchangeCtx, cancel := context.WithCancel(ctx)
			if streaming {
				cancel()
				exchangeCtx, cancel = context.WithCancel(s.ctx)
			} else {
				cancel()
				exchangeCtx, cancel = context.WithTimeout(ctx, 45*time.Second)
			}
			env := map[string]string{}
			accounts := map[string]string{}
			scope := "plugin-background-" + b.ID
			catalog, listErr := s.Integrations.List(exchangeCtx)
			if listErr != nil {
				err = listErr
			}
			for key, binding := range p.Manifest.Contributes.Background.Credentials {
				if err != nil {
					break
				}
				for _, item := range catalog {
					if item.ID == binding.Service && item.Account != nil {
						accounts[binding.Service] = item.Account.Identity.ID
					}
				}
				env[key], err = s.Integrations.Credential(exchangeCtx, scope, binding.Service, false)
				if err != nil {
					status = "authorization"
				}
			}
			if err == nil {
				defs, directoryErr := s.Registry.Definitions(exchangeCtx)
				if directoryErr != nil {
					cancel()
					continue
				}
				assistants := make([]string, 0, len(defs))
				for _, d := range defs {
					assistants = append(assistants, d.ID)
				}
				var response core.BackgroundResponse
				worker, workerErr := os.Executable()
				if workerErr != nil {
					cancel()
					continue
				}
				if streaming {
					identity, identityErr := p.BackgroundIdentity()
					if identityErr != nil {
						cancel()
						continue
					}
					catalogBytes, _ := json.Marshal(catalog)
					s.pluginStreams[b.ID] = backgroundStream{pluginID: b.PluginID, revision: b.Revision, cancel: cancel, accounts: string(catalogBytes), digest: identity.Digest}
					s.pluginWG.Add(1)
					go func() {
						defer s.pluginWG.Done()
						defer cancel()
						defer s.Integrations.Forget(scope)
						streamErr := pluginhost.RunBackgroundStream(exchangeCtx, s.options.Node, p, core.BackgroundRequest{Version: 1, Now: now, Config: b.Config, State: b.State, Accounts: accounts, InstanceID: b.ID, Assistant: b.Assistant, Assistants: assistants}, env, []string{worker}, func(frame core.BackgroundResponse) error {
							after, e := s.Integrations.List(exchangeCtx)
							beforeJSON, _ := json.Marshal(catalog)
							afterJSON, _ := json.Marshal(after)
							if e != nil || string(beforeJSON) != string(afterJSON) {
								return errors.New("integration changed during stream")
							}
							if e = s.validateBackgroundTargets(exchangeCtx, b, frame); e != nil {
								return e
							}
							if e = s.Mailbox.CommitBackground(exchangeCtx, b, identity, frame, storage.Now()); e != nil {
								return e
							}
							s.Changes.Notify("plugin-notifications")
							return nil
						})
						s.pluginMu.Lock()
						defer s.pluginMu.Unlock()
						delete(s.pluginStreams, b.ID)
						if streamErr != nil && s.ctx.Err() == nil {
							_ = s.Mailbox.BackgroundError(ctx, b, "unavailable", streamErr.Error(), storage.Now())
						}
						s.Changes.Notify("plugin-notifications")
					}()
					continue
				}
				response, err = pluginhost.RunBackground(exchangeCtx, s.options.Node, p, core.BackgroundRequest{Version: 1, Now: now, Config: b.Config, State: b.State, Accounts: accounts, InstanceID: b.ID, Assistant: b.Assistant, Assistants: assistants}, env, []string{worker})
				if err == nil {
					after, checkErr := s.Integrations.List(exchangeCtx)
					beforeJSON, _ := json.Marshal(catalog)
					afterJSON, _ := json.Marshal(after)
					if checkErr != nil || string(beforeJSON) != string(afterJSON) {
						err = errors.New("integration changed during exchange")
					} else {
						identity, identityErr := p.BackgroundIdentity()
						err = identityErr
						if err == nil {
							err = s.validateBackgroundTargets(exchangeCtx, b, response)
						}
						if err == nil {
							err = s.Mailbox.CommitBackground(exchangeCtx, b, identity, response, now)
						}
					}
				}
			}
			s.Integrations.Forget(scope)
			cancel()
		}
		if err != nil {
			_ = s.Mailbox.BackgroundError(ctx, b, status, err.Error(), now)
		}
		s.Changes.Notify("plugin-notifications")
	}
}

// Bootstrap uses the plugin's real MCP configuration interface. The persistent
// initialization marker and instance writes are atomic; the cache only avoids
// repeating discovery when accounts, assistants and package identity are stable.
func (s *Service) ensurePluginNotifications(ctx context.Context) error {
	packages, err := s.installedPlugins()
	if err != nil {
		return err
	}
	catalog, err := s.Integrations.List(ctx)
	if err != nil {
		return err
	}
	defs, err := s.Registry.Definitions(ctx)
	if err != nil {
		return err
	}
	if s.pluginBootstrap == nil {
		s.pluginBootstrap = map[string]string{}
	}
	for _, p := range packages {
		c := p.Manifest.Contributes.MCP
		if c == nil || c.Configuration == nil {
			continue
		}
		enabled, e := s.enabled(ctx, "plugins", p.Manifest.ID)
		if e != nil {
			return e
		}
		if !enabled {
			continue
		}
		identity, e := p.MCPIdentity()
		if e != nil {
			return e
		}
		key, _ := core.HashJSON([]any{catalog, defs, identity})
		if s.pluginBootstrap[p.Manifest.ID] == key {
			continue
		}
		_, r, e := s.invokePluginMCP(ctx, p, c.Configuration.List, json.RawMessage(`{}`))
		if e != nil {
			return e
		}
		if r.IsError {
			return errors.New("plugin configuration initialization failed")
		}
		s.pluginBootstrap[p.Manifest.ID] = key
	}
	return nil
}

// Configuration projections come from each plugin's MCP server. The host never
// interprets service-specific filters, defaults or checkpoint formats.
func (s *Service) PluginNotifications(ctx context.Context) (any, error) {
	packages, err := s.installedPlugins()
	if err != nil {
		return nil, err
	}
	defs, err := s.Registry.Definitions(ctx)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, p := range packages {
		c := p.Manifest.Contributes.MCP
		if c == nil || c.Configuration == nil {
			continue
		}
		enabled, e := s.enabled(ctx, "plugins", p.Manifest.ID)
		if e != nil {
			return nil, e
		}
		if !enabled {
			continue
		}
		_, r, e := s.invokePluginMCP(ctx, p, c.Configuration.List, json.RawMessage(`{}`))
		if e != nil {
			items = append(items, map[string]any{"id": p.Manifest.ID, "diagnostic": e.Error()})
			continue
		}
		items = append(items, map[string]any{"id": p.Manifest.ID, "settings": r.StructuredContent})
	}
	return map[string]any{"items": items, "assistants": defs}, nil
}
func (s *Service) PluginNotificationCall(ctx context.Context, id, tool string, args json.RawMessage) (any, error) {
	p, err := s.backgroundPackage(id)
	if err != nil {
		return nil, err
	}
	c := p.Manifest.Contributes.MCP
	if c == nil || c.Configuration == nil {
		return nil, errors.New("plugin configuration unavailable")
	}
	if tool != c.Configuration.List && tool != c.Configuration.Create && tool != c.Configuration.Update && tool != c.Configuration.Delete {
		return nil, errors.New("unknown configuration tool")
	}
	_, r, err := s.invokePluginMCP(ctx, p, tool, args)
	if err != nil {
		return nil, err
	}
	if r.IsError {
		return nil, errors.New(string(r.StructuredContent))
	}
	return s.PluginNotifications(ctx)
}

func (s *Service) validateNotificationTarget(ctx context.Context, id string) error {
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return err
	}
	for _, d := range definitions {
		if d.ID == id {
			return nil
		}
	}
	return errors.New("notification assistant unavailable")
}

func (s *Service) validateBackgroundTargets(ctx context.Context, b assistant.BackgroundBinding, r core.BackgroundResponse) error {
	if !b.EventRouting {
		return s.validateNotificationTarget(ctx, b.Assistant)
	}
	for _, event := range r.Events {
		if err := s.validateNotificationTarget(ctx, event.Assistant); err != nil {
			return err
		}
	}
	return nil
}
