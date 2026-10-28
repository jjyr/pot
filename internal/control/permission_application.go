package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
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

func (s *Service) ApprovalView(ctx context.Context, assistantID string) map[string]any {
	type pendingApproval struct {
		Approval
		TaskTitle string `json:"task_title,omitempty"`
	}
	pending := []any{}
	for _, approval := range s.Permissions.Pending(assistantID) {
		if approval.Request.Task != "" && s.potMCPRemoteTask(ctx, approval.Request.Task) {
			continue
		}
		item := pendingApproval{Approval: approval}
		if approval.Request.Task != "" {
			if task, err := s.Tasks.Get(ctx, assistantID, approval.Request.Task); err == nil && task != nil {
				item.TaskTitle = task.Info.Title
			}
		}
		pending = append(pending, item)
	}
	return s.reviewView(ctx, assistantID, s.Permissions.Snapshot(), pending)
}

func (s *Service) ManageApproval(ctx context.Context, assistantID string, body []byte) (any, error) {
	var input struct {
		ID               string         `json:"id,omitempty"`
		Action           string         `json:"action" required:"true"`
		MachineID        string         `json:"machine_id,omitempty"`
		MachinePolicy    *MachinePolicy `json:"machine_policy,omitempty"`
		Rule             *CommandRule   `json:"rule,omitempty"`
		OldID            string         `json:"old_id,omitempty"`
		ExpectedRevision uint64         `json:"expected_revision,omitempty"`
	}
	if err := protocol.DecodeStrict(body, &input); err != nil {
		return nil, err
	}
	if isLinkedApproval(input.ID) {
		err := s.decideLinkedApproval(ctx, assistantID, input.ID, input.Action)
		return map[string]bool{"accepted": err == nil}, err
	}
	var err error
	switch input.Action {
	case "save_rule":
		if input.Rule == nil {
			return nil, errors.New("command rule required")
		}
		err = s.SaveCommandRule(ctx, "owner", *input.Rule, input.OldID, input.ExpectedRevision)
	case "delete_rule":
		err = s.Permissions.DeleteRule(input.MachineID, input.ID, input.ExpectedRevision)
	case "remove_task_grant":
		err = s.Permissions.RemoveGrant(input.ID)
	case "retry":
		err = s.Permissions.Retry(input.MachineID, input.ExpectedRevision)
	case "configure":
		if input.MachinePolicy == nil || input.MachinePolicy.Revision == 0 {
			return nil, errors.New("machine policy required")
		}
		err = s.Permissions.Configure(input.MachineID, *input.MachinePolicy)
	default:
		for _, approval := range s.Permissions.Pending(assistantID) {
			if approval.ID == input.ID && approval.Request.Task != "" && s.potMCPRemoteTask(ctx, approval.Request.Task) {
				return nil, agentlink.ErrUnauthorized
			}
		}
		err = s.Permissions.Decide(assistantID, input.ID, input.Action)
	}
	return map[string]bool{"accepted": err == nil}, err
}
