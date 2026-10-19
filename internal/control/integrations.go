package control

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/jjyr/pot/internal/assistant"
	codexintegration "github.com/jjyr/pot/pkg/integrations/codex"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/integrations"
	githubintegration "github.com/jjyr/pot/pkg/integrations/github"
	"github.com/jjyr/pot/pkg/storage"
)

// Public identity of the Pot OAuth App owned by jjyr. Device authorization and
// refresh require no client secret. Deployment configuration cannot replace it.
const githubClientID = "Ov23lijqmW8fsHmUPXl9"

func (s *Service) openIntegrations() error {
	path := filepath.Join(s.options.Root, "control.sqlite")
	var err error
	s.integrationDB, err = storage.OpenSQLite(path)
	if err != nil {
		return err
	}
	s.integrationAccounts, err = integrations.NewSQLAccounts(s.integrationDB)
	if err != nil {
		return err
	}
	s.integrationSecrets, err = secrets.Open(path, s.options.Keys)
	if err != nil {
		return err
	}
	adapters := s.options.IntegrationAdapters
	if adapters == nil {
		adapters = []integrations.Adapter{githubintegration.New(githubintegration.Options{ClientID: githubClientID}), codexintegration.Adapter{}}
	}
	s.Integrations, err = integrations.New(integrations.Options{Context: s.ctx, Accounts: s.integrationAccounts, Secrets: s.integrationSecrets, Adapters: adapters, Changed: func() { s.Changes.Notify("integrations") }, Write: func(work func() error) error { return s.Machines.OwnerAction(work) }})
	return err
}

// PairCodex binds an authenticated device pairing to this Control and one
// existing assistant. Remote payloads cannot select the notification recipient.
func (s *Service) PairCodex(ctx context.Context, raw json.RawMessage) (any, error) {
	var pair codexintegration.Pairing
	if json.Unmarshal(raw, &pair) != nil {
		return nil, errors.New("invalid_codex_pairing")
	}
	prefix := s.options.RuntimeID + "/"
	if len(pair.Route) <= len(prefix) || pair.Route[:len(prefix)] != prefix {
		return nil, errors.New("codex_pairing_wrong_control")
	}
	target := pair.Route[len(prefix):]
	if err := s.validateNotificationTarget(ctx, target); err != nil {
		return nil, err
	}
	account, err := s.Integrations.Connect(ctx, "codex", raw)
	if err != nil {
		return nil, err
	}
	bindings, err := s.Mailbox.Backgrounds(ctx)
	if err != nil {
		return nil, err
	}
	b := assistant.BackgroundBinding{ID: "codex", Enabled: true, Assistant: target, Config: json.RawMessage(`{}`)}
	for _, old := range bindings {
		if old.ID == "codex" {
			if old.Enabled && old.Assistant == target {
				return account, nil
			}
			b.Revision = old.Revision
		}
	}
	if err = s.ConfigurePluginNotifications(ctx, b); err != nil {
		return nil, err
	}
	return account, nil
}
