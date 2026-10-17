package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func (s *Service) validateTaskTarget(_ context.Context, _ core.ExecutionTarget, requirements protocol.TaskRequirements) error {
	if requirements.Desktop {
		return errors.New("desktop execution is unavailable")
	}
	return nil
}
