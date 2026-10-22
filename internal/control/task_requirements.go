package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
)

func (s *Service) validateTaskRequirements(_ context.Context, requirements protocol.TaskRequirements) error {
	if requirements.Desktop {
		return errors.New("desktop execution is unavailable")
	}
	return nil
}
