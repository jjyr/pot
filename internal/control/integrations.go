package control

import (
	"path/filepath"

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
	s.integrationDB, err = storage.OpenDatabase(path, storage.ControlDatabase)
	if err != nil {
		return err
	}
	s.integrationAccounts = integrations.NewSQLAccounts(s.integrationDB)
	s.integrationSecrets, err = secrets.Open(path, s.options.Keys)
	if err != nil {
		return err
	}
	adapters := s.options.IntegrationAdapters
	if adapters == nil {
		adapters = []integrations.Adapter{githubintegration.New(githubintegration.Options{ClientID: githubClientID})}
	}
	s.Integrations, err = integrations.New(integrations.Options{Context: s.ctx, Accounts: s.integrationAccounts, Secrets: s.integrationSecrets, Adapters: adapters, Changed: func() { s.Changes.Notify("integrations") }, Write: func(work func() error) error { return s.Machines.OwnerAction(work) }})
	return err
}
