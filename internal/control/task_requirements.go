package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/storage"
)

func (s *Service) validateTaskTarget(ctx context.Context, target core.ExecutionTarget, requirements protocol.TaskRequirements) error {
	if !requirements.Desktop {
		return nil
	}
	if target.RuntimeID != s.options.RuntimeID {
		peer, err := s.Machines.Peer(target.RuntimeID)
		if err != nil {
			return err
		}
		body, err := json.Marshal(struct {
			Target       core.ExecutionTarget      `json:"target"`
			Requirements protocol.TaskRequirements `json:"requirements"`
		}{target, requirements})
		if err != nil {
			return err
		}
		_, err = peer.Call(ctx, controlplane.Request{ID: "requirements:" + storage.ID(), Method: "machine.requirements", Params: body})
		return err
	}
	if target.Target.Kind != "sandbox" || s.options.Sandboxes == nil {
		return errors.New("fixed Task target does not advertise desktop execution")
	}
	definitions, err := s.options.Sandboxes.Inventory(ctx)
	if err != nil {
		return err
	}
	for _, d := range definitions {
		if d.ID == target.Target.SandboxID && !d.Retired && !d.Quarantined {
			for _, provider := range s.options.Sandboxes.Descriptors() {
				if provider.ID == d.Provider && provider.Capabilities.Desktop {
					return nil
				}
			}
		}
	}
	return errors.New("fixed Task Sandbox does not advertise desktop execution")
}
