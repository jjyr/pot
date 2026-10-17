package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/storage"
)

func (s *Service) executionAdmission(revision uint64) (func(), error) {
	if s.Machines.Role() == "runner" {
		state := s.remote()
		state.mu.Lock()
		if state.permissionRevision != revision || state.permissionApplied != revision || revision == 0 {
			state.mu.Unlock()
			return nil, errors.New("Runner permission revision changed")
		}
		return state.mu.Unlock, nil
	}
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
	peer, err := s.Machines.Peer(machine)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]uint64{"revision": revision})
	_, err = peer.Call(ctx, controlplane.Request{ID: "permissions:" + storage.ID(), Method: "machine.permissions", Params: body})
	return err
}
func (s *Service) syncRemotePermission(ctx context.Context, peer *controlplane.Peer, revision uint64) error {
	body, _ := json.Marshal(map[string]uint64{"revision": revision})
	_, err := peer.Call(ctx, controlplane.Request{ID: "permissions:" + storage.ID(), Method: "machine.permissions", Params: body})
	return err
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
		peer, err := s.Machines.Peer(r.MachineID)
		if err != nil {
			return err
		}
		body, _ := json.Marshal(r.Context)
		data, err := peer.Call(ctx, controlplane.Request{ID: "rule-context:" + storage.ID(), Method: "machine.command_context", Params: body})
		if err != nil {
			return err
		}
		if err = core.DecodeJSON(data, &r.Context); err != nil {
			return err
		}
	}
	return s.Permissions.SaveRule(owner, r, oldID, revision)
}

// applyPermissions serializes Runner revision changes and retries failed drains.
// Announcing a revision fences admission before stopping old sessions.
func (state *remoteState) applyPermissions(revision uint64, drain func() error) error {
	state.permissionUpdate.Lock()
	defer state.permissionUpdate.Unlock()
	state.mu.Lock()
	old := state.permissionRevision
	if revision == 0 || revision < old {
		state.mu.Unlock()
		return errors.New("invalid or stale permission revision")
	}
	if revision == old && state.permissionApplied == revision {
		state.mu.Unlock()
		return nil
	}
	state.permissionRevision = revision
	state.mu.Unlock()
	if old != 0 {
		if err := drain(); err != nil {
			return err
		}
	}
	state.mu.Lock()
	state.permissionApplied = revision
	state.mu.Unlock()
	return nil
}
