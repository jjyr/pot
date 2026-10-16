package control

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
)

func (s *Service) settleTaskOperations(ctx context.Context, task string) error {
	scope, err := core.HashJSON([]any{task})
	if err != nil {
		return err
	}
	var pending bool
	if err = s.Operations.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM operations o JOIN operation_scopes s ON s.id=o.id WHERE s.scope=? AND o.state!='complete')", scope).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return core.ErrCleanupUnconfirmed
	}
	return nil
}
