package control

import (
	"context"
	"github.com/jjyr/pot/internal/credentials"
)

func (s *Service) SaveCredentialAccount(ctx context.Context, r credentials.SaveRequest) (credentials.Account, error) {
	var account credentials.Account
	err := s.Machines.OwnerAction(func() error { var err error; account, err = s.Credentials.Save(ctx, r); return err })
	if err == nil {
		s.Changes.Notify("credentials")
	}
	return account, err
}
func (s *Service) RemoveCredentialAccount(ctx context.Context, r credentials.RemoveRequest) error {
	err := s.Machines.OwnerAction(func() error { return s.Credentials.Remove(ctx, r) })
	if err == nil {
		s.Changes.Notify("credentials")
	}
	return err
}
