package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/pluginhost"
	"github.com/jjyr/pot/pkg/storage"
)

func (s *Service) ConfigurePluginNotifications(ctx context.Context, b assistant.BackgroundBinding) error {
	return s.Machines.OwnerAction(func() error {
		if b.ConnectionID != "" || b.PluginID != "" && b.PluginID != b.ID {
			return errors.New("invalid plugin background identity")
		}
		b.PluginID = b.ID
		if b.ID == "github" {
			if err := validateGitHubNotificationConfig(b.Config); err != nil {
				return err
			}
		}
		_, err := s.backgroundPackage(b.PluginID)
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
	if s.Machines.Role() == "runner" {
		return
	}
	if err := s.ensureGitHubNotifications(ctx); err != nil {
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
			err = s.validateNotificationTarget(ctx, b.Assistant)
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
						streamErr := pluginhost.RunBackgroundStream(exchangeCtx, s.options.Node, p, core.BackgroundRequest{Version: 1, Now: now, Config: b.Config, State: b.State, Accounts: accounts}, env, []string{worker}, func(frame core.BackgroundResponse) error {
							after, e := s.Integrations.List(exchangeCtx)
							beforeJSON, _ := json.Marshal(catalog)
							afterJSON, _ := json.Marshal(after)
							if e != nil || string(beforeJSON) != string(afterJSON) {
								return errors.New("integration changed during stream")
							}
							if e = s.validateNotificationTarget(exchangeCtx, b.Assistant); e != nil {
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
			_ = s.Mailbox.BackgroundError(ctx, b, status, err.Error(), now)
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
		packages, err = s.installedPlugins()
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
		b := assistant.BackgroundBinding{ID: id, PluginID: id, Enabled: id == "github", Config: json.RawMessage(`{}`), Status: "pending"}
		for _, old := range stored {
			if old.ID == id {
				b = old
			}
		}
		enabled, checkErr := s.enabled(ctx, "plugins", id)
		if checkErr != nil {
			return nil, checkErr
		}
		if !b.Enabled || !enabled {
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

// Reject removed/unknown settings before committing a configuration that cannot poll.
func validateGitHubNotificationConfig(raw json.RawMessage) error {
	var config struct {
		Repositories []string `json:"repositories"`
		Reasons      []string `json:"reasons"`
	}
	if string(raw) == "null" {
		return errors.New("invalid GitHub notification configuration")
	}
	if err := core.DecodeJSON(raw, &config); err != nil {
		return err
	}
	repository := regexp.MustCompile(`^[-\w.]+/[-\w.]+$`)
	reason := regexp.MustCompile(`^[-a-zA-Z0-9_]+$`)
	if len(config.Repositories) > 100 || len(config.Reasons) > 100 {
		return errors.New("too many GitHub notification filters")
	}
	for _, value := range config.Repositories {
		if len(value) > 200 || !repository.MatchString(value) {
			return errors.New("invalid GitHub notification repository")
		}
	}
	for _, value := range config.Reasons {
		if len(value) > 200 || !reason.MatchString(value) {
			return errors.New("invalid GitHub notification reason")
		}
	}
	return nil
}
