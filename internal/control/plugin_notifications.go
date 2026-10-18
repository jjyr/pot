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

func (s *Service) ConfigurePluginNotifications(ctx context.Context, b assistant.BackgroundBinding) error {
	return s.Machines.OwnerAction(func() error {
		_, err := s.backgroundPackage(b.ID)
		if err != nil && b.ID != "github" {
			return err
		}
		definitions, err := s.Registry.Definitions(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, d := range definitions {
			if d.ID == b.Assistant {
				found = true
			}
		}
		if b.Enabled && !found {
			return errors.New("notification assistant unavailable")
		}
		if err = s.Mailbox.ConfigureBackground(ctx, b); err == nil {
			s.Changes.Notify("plugin-notifications")
		}
		return err
	})
}
func (s *Service) backgroundPackage(id string) (plugin.Package, error) {
	packages, err := plugin.Discover(s.options.PluginRoot)
	if err != nil {
		return plugin.Package{}, err
	}
	for _, p := range packages {
		if p.Manifest.ID == id && p.Manifest.Contributes.Background != nil {
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
func (s *Service) runBackgrounds(ctx context.Context, now int64) {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	if s.Machines.Role() == "runner" {
		return
	}
	if err := s.ensureGitHubNotifications(ctx); err != nil {
		return
	}
	bindings, err := s.Mailbox.Backgrounds(ctx)
	if err != nil {
		return
	}
	for _, b := range bindings {
		if !b.Enabled || b.Next > now {
			continue
		}
		p, err := s.backgroundPackage(b.ID)
		status := "unavailable"
		if err == nil {
			err = s.validateNotificationTarget(ctx, b.Assistant)
		}
		if err == nil {
			exchangeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
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
				var response core.BackgroundResponse
				worker, workerErr := os.Executable()
				if workerErr != nil {
					cancel()
					continue
				}
				response, err = pluginhost.RunBackground(exchangeCtx, s.options.Node, p, core.BackgroundRequest{Version: 1, Now: now, Config: b.Config, State: b.State, Accounts: accounts}, env, []string{worker})
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
							err = s.validateNotificationTarget(exchangeCtx, b.Assistant)
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
			_ = s.Mailbox.BackgroundError(ctx, b, status, now)
		}
		s.Changes.Notify("plugin-notifications")
	}
}

// Registry definitions are ordered by their persistent creation sequence. Only
// an absent binding is uninitialized; never infer intent from enabled/target.
// Reuse the background loop for late account/assistant creation, without a new poller.
func (s *Service) ensureGitHubNotifications(ctx context.Context) error {
	if s.Machines.Role() == "runner" {
		return nil
	}
	return s.Machines.OwnerAction(func() error {
		stored, err := s.Mailbox.Backgrounds(ctx)
		if err != nil {
			return err
		}
		for _, b := range stored {
			if b.ID == "github" {
				return nil
			}
		}
		definitions, err := s.Registry.Definitions(ctx)
		if err != nil || len(definitions) == 0 {
			return err
		}
		catalog, err := s.Integrations.List(ctx)
		if err != nil {
			return err
		}
		for _, item := range catalog {
			if item.ID == "github" && item.Account != nil {
				return s.Mailbox.EnsureBackground(ctx, "github", definitions[0].ID)
			}
		}
		return nil
	})
}

func (s *Service) PluginNotifications(ctx context.Context) (any, error) {
	if err := s.ensureGitHubNotifications(ctx); err != nil {
		return nil, err
	}
	packages := []plugin.Package{}
	var err error
	if s.options.PluginRoot != "" {
		packages, err = plugin.Discover(s.options.PluginRoot)
	}
	discoveryErr := err
	stored, err := s.Mailbox.Backgrounds(ctx)
	if err != nil {
		return nil, err
	}
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return nil, err
	}
	catalog, err := s.Integrations.List(ctx)
	if err != nil {
		return nil, err
	}
	ids := []string{"github"}
	installed := map[string]bool{}
	for _, p := range packages {
		if p.Manifest.Contributes.Background != nil {
			installed[p.Manifest.ID] = true
			if p.Manifest.ID != "github" {
				ids = append(ids, p.Manifest.ID)
			}
		}
	}
	bindings := []assistant.BackgroundBinding{}
	for _, id := range ids {
		b := assistant.BackgroundBinding{ID: id, Enabled: id == "github", Config: json.RawMessage(`{}`), Status: "pending"}
		for _, old := range stored {
			if old.ID == id {
				b = old
			}
		}
		if !b.Enabled {
			b.Status = "disabled"
		} else if discoveryErr != nil {
			b.Status = "unavailable"
			b.Diagnostic = discoveryErr.Error()
		} else if !installed[id] {
			b.Status = "plugin_missing"
		} else if b.Assistant == "" {
			b.Status = "waiting_assistant"
		} else if s.validateNotificationTarget(ctx, b.Assistant) != nil {
			b.Status = "assistant_missing"
		} else {
			for _, item := range catalog {
				if item.ID == id && item.Account == nil {
					b.Status = "authorization"
				}
			}
			if b.Status == "disabled" {
				b.Status = "pending"
			}
		}
		bindings = append(bindings, b)
	}
	return map[string]any{"items": bindings, "assistants": definitions}, nil
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
