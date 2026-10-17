package control

import (
	"os"
	"path/filepath"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/integrations"
	githubintegration "github.com/jjyr/pot/pkg/integrations/github"
	"github.com/jjyr/pot/pkg/storage"
)

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
		adapters = []integrations.Adapter{githubintegration.New(githubintegration.Options{ClientID: os.Getenv("POT_GITHUB_CLIENT_ID"), ClientSecret: os.Getenv("POT_GITHUB_CLIENT_SECRET")})}
	}
	s.Integrations, err = integrations.New(integrations.Options{Context: s.ctx, Accounts: s.integrationAccounts, Secrets: s.integrationSecrets, Adapters: adapters, Changed: func() { s.Changes.Notify("integrations") }, Write: func(work func() error) error { return s.Machines.OwnerAction(work) }})
	return err
}
