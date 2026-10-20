package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
)

func (s *Service) executionAdmission(revision uint64) (func(), error) {
	return s.Permissions.Admit(s.options.RuntimeID, revision)
}
func (s *Service) drainPermissionEngines() error {
	s.mu.Lock()
	engines := make(map[string]*executor.Engine, len(s.hosts))
	for id, engine := range s.hosts {
		engines[id] = engine
	}
	s.mu.Unlock()
	var err error
	for id, engine := range engines {
		if closeErr := engine.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
			continue
		}
		s.mu.Lock()
		if s.hosts[id] == engine {
			delete(s.hosts, id)
		}
		s.mu.Unlock()
	}
	return err
}
func (s *Service) applyPermissionRevision(machine string, revision uint64) error {
	if machine == s.options.RuntimeID {
		return s.drainPermissionEngines()
	}
	return protocol.APIError{Code: "local_node_required"}
}

func (s *Service) SaveCommandRule(ctx context.Context, owner string, r CommandRule, oldID string, revision uint64) error {
	if r.MachineID == s.options.RuntimeID {
		args := map[string]any{"cmd": r.Context.Command, "shell": r.Context.Shell, "login": r.Context.Login, "interactive": r.Context.Interactive, "tty": r.Context.TTY}
		if len(r.Context.Credentials) > 0 {
			var refs any
			if err := json.Unmarshal(r.Context.Credentials, &refs); err != nil {
				return err
			}
			args["credentials"] = refs
		}
		value, err := ResolveCommandContext(r.Context.Directory, args, core.ShellSettings{Executable: r.Context.Shell, AllowLoginShell: true, ShellSnapshot: r.Context.Snapshot})
		if err != nil {
			return err
		}
		r.Context = *value
	} else {
		return protocol.APIError{Code: "local_node_required"}
	}
	return s.Permissions.SaveRule(owner, r, oldID, revision)
}
