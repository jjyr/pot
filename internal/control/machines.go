package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/sandbox"
	"github.com/jjyr/pot/pkg/storage"
	"runtime"
	"time"
)

type JoinControl struct {
	RequestID           string  `json:"request_id" required:"true"`
	URL                 string  `json:"url" required:"true"`
	Invitation          string  `json:"invitation" required:"true"`
	ExpectedRuntimeID   string  `json:"expected_runtime_id" required:"true"`
	ExpectedFingerprint string  `json:"expected_fingerprint" required:"true"`
	CAPEM               *string `json:"ca_pem"`
}
type MembershipCommand struct {
	Action    string       `json:"action" required:"true"`
	URL       string       `json:"url"`
	CAPEM     *string      `json:"ca_pem"`
	Request   *JoinControl `json:"request"`
	RuntimeID string       `json:"runtime_id"`
	Tags      []string     `json:"tags"`
}

func (s *Service) ManageMembership(ctx context.Context, r MembershipCommand) (any, error) {
	if s.options.Embedded && r.Action != "set_tags" {
		return nil, errors.New("embedded Control does not join or administer remote Runtime membership; connect a remote Space instead")
	}
	switch r.Action {
	case "preview":
		ca := ""
		if r.CAPEM != nil {
			ca = *r.CAPEM
		}
		client, err := controlplane.ClientForCA(s.options.ControlHTTPClient, ca)
		if err != nil {
			return nil, err
		}
		connector := controlplane.Connector{Directory: s.Machines, HTTPClient: client, CAPEM: ca}
		return connector.Preview(ctx, r.URL)
	case "join":
		if r.Request == nil {
			return nil, errors.New("enrollment request required")
		}
		q := r.Request
		ca := ""
		if q.CAPEM != nil {
			ca = *q.CAPEM
		}
		client, err := controlplane.ClientForCA(s.options.ControlHTTPClient, ca)
		if err != nil {
			return nil, err
		}
		connector := controlplane.Connector{Directory: s.Machines, HTTPClient: client, CAPEM: ca}
		preview, err := connector.Preview(ctx, q.URL)
		if err != nil {
			return nil, err
		}
		if preview.RuntimeID != q.ExpectedRuntimeID || preview.Fingerprint != q.ExpectedFingerprint {
			return nil, errors.New("Control identity does not match pinned Runtime/fingerprint")
		}
		member, err := connector.Enroll(ctx, q.URL, preview, q.Invitation, q.RequestID)
		if err != nil {
			return nil, err
		}
		s.StartRunner()
		s.Changes.Notify("machines")
		return map[string]any{"role": "runner", "control": member.Enrollment.Control, "epoch": member.Enrollment.Epoch}, nil
	case "owner_credential":
		if s.Machines.Role() != "control" || s.options.Embedded {
			return nil, errors.New("owner credential export requires a standalone Control")
		}
		return map[string]string{"runtime_id": s.options.RuntimeID, "credential": s.Machines.OwnerCredential()}, nil
	case "create_invitation":
		return s.Machines.Invitation(ctx, time.Hour)
	case "remove_member":
		if r.RuntimeID == s.options.RuntimeID {
			return nil, errors.New("cannot remove local Runtime")
		}
		if err := s.Permissions.Remove(r.RuntimeID); err != nil {
			return nil, err
		}
		if err := s.Machines.Remove(ctx, r.RuntimeID); err != nil {
			return nil, err
		}
		s.Changes.Notify("machines")
		s.wakeTasks()
		return map[string]bool{"removed": true}, nil
	case "set_tags":
		if err := s.Machines.SetLabels(ctx, r.RuntimeID, r.Tags); err != nil {
			return nil, err
		}
		s.Changes.Notify("machines")
		return map[string]any{"tags": r.Tags}, nil
	case "leave":
		err := s.Machines.Leave(ctx, func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			sub := s.RunnerJournal.Subscribe()
			defer sub.Close()
			runs, err := s.RunnerJournal.Runs(ctx)
			if err != nil {
				return err
			}
			for _, run := range runs {
				if !run.Phase.Ended() {
					if err = (remoteRunnerPort{s}).Stop(ctx, run.Launch.ControlID, run.Launch.TaskID, run.Launch.RunID, "cancel"); err != nil {
						return err
					}
					if run.Phase == "recovery_required" {
						return core.ErrCleanupUnconfirmed
					}
				}
			}
			for {
				runs, err := s.RunnerJournal.Runs(ctx)
				if err != nil {
					return err
				}
				all := true
				for _, run := range runs {
					if !run.Phase.Ended() || !run.CleanupConfirmed {
						all = false
						if run.Phase == "recovery_required" {
							return core.ErrCleanupUnconfirmed
						}
					}
				}
				if all {
					return nil
				}
				if _, err = sub.Next(ctx); err != nil {
					return core.ErrCleanupUnconfirmed
				}
			}
		})
		if err != nil {
			return nil, err
		}
		s.Changes.Notify("machines")
		return map[string]bool{"left": true}, nil
	}
	return nil, errors.New("unknown membership action")
}

type MachineCommand struct {
	Action    string             `json:"action" required:"true"`
	MachineID string             `json:"machine_id"`
	SandboxID string             `json:"sandbox_id"`
	Resources *sandbox.Resources `json:"resources"`
}

func (s *Service) localMachine(ctx context.Context) (map[string]any, error) {
	policy, _, err := s.Permissions.Policy(s.options.RuntimeID)
	if err != nil {
		return nil, err
	}
	desktop := false
	if s.options.Sandboxes != nil {
		for _, d := range s.options.Sandboxes.Descriptors() {
			desktop = desktop || d.Capabilities.Desktop
		}
	}
	item := map[string]any{"id": s.options.RuntimeID, "runtime_id": s.options.RuntimeID, "name": s.options.RuntimeID, "os": runtime.GOOS, "connected": true, "role": s.Machines.Role(), "tags": s.Machines.LocalLabels(), "policy": policy, "capabilities": map[string]bool{"host": true, "sandbox": s.HasSandbox(), "desktop": desktop}}
	if s.options.Sandboxes != nil {
		definitions, err := s.options.Sandboxes.Inventory(ctx)
		if err != nil {
			return nil, err
		}
		for _, d := range definitions {
			if d.Retired {
				continue
			}
			raw, _ := json.Marshal(d)
			var view map[string]any
			json.Unmarshal(raw, &view)
			status, err := s.options.Sandboxes.Lifecycle(ctx, d.ID, "status")
			if err == nil {
				view["state"] = status
			}
			item["sandbox"] = view
			break
		}
	}
	return item, nil
}
func (s *Service) MachineList(ctx context.Context) ([]map[string]any, error) {
	local, err := s.localMachine(ctx)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{local}
	members, err := s.Machines.Machines(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		item := map[string]any{"id": m.Identity.RuntimeID, "runtime_id": m.Identity.RuntimeID, "name": m.Identity.RuntimeID, "os": m.OS, "tags": m.Labels, "connected": m.Online, "role": "runner", "capabilities": map[string]bool{"host": true, "sandbox": false, "desktop": false}}
		if m.Online {
			if peer, err := s.Machines.Peer(m.Identity.RuntimeID); err == nil {
				reply, err := peer.Call(ctx, controlplane.Request{ID: "inventory:" + storage.ID(), Method: "machine.inventory", Params: json.RawMessage(`{}`)})
				if err == nil {
					var remote map[string]any
					if err = core.DecodeJSON(reply, &remote); err == nil {
						for _, key := range []string{"capabilities", "sandbox"} {
							if value, ok := remote[key]; ok {
								item[key] = value
							}
						}
					}
				}
			}
		}
		policy, _, err := s.Permissions.Policy(m.Identity.RuntimeID)
		if err == nil {
			item["policy"] = policy
		}
		items = append(items, item)
	}
	return items, nil
}
func (s *Service) ManageMachine(ctx context.Context, r MachineCommand) (any, error) {
	if r.MachineID != "" && r.MachineID != s.options.RuntimeID {
		peer, err := s.Machines.Peer(r.MachineID)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(r)
		reply, err := peer.Call(ctx, controlplane.Request{ID: "machine-manage:" + storage.ID(), Method: "machine.manage", Params: raw})
		if err != nil {
			return nil, err
		}
		var value any
		if err = core.DecodeJSON(reply, &value); err != nil {
			return nil, err
		}
		s.Changes.Notify("machines")
		return value, nil
	}
	return s.manageLocalMachine(ctx, r)
}
func (s *Service) manageLocalMachine(ctx context.Context, r MachineCommand) (any, error) {
	m := s.options.Sandboxes
	if m == nil {
		return nil, errors.New("machine has no available Sandbox provider")
	}
	id := r.SandboxID
	if id == "" {
		d, err := m.EnsureShared(ctx)
		if err != nil {
			return nil, err
		}
		id = d.ID
	}
	defer s.Changes.Notify("machines")
	switch r.Action {
	case "start", "stop", "rebuild":
		if r.Action == "start" {
			if err := s.PrepareSandboxResources(ctx, id); err != nil {
				return nil, err
			}
		}
		return m.Lifecycle(ctx, id, r.Action)
	case "recover":
		err := m.Recover(ctx, id)
		return map[string]bool{"recovered": err == nil}, err
	case "configure_resources":
		if r.Resources == nil {
			return nil, errors.New("Sandbox resources required")
		}
		err := m.Configure(ctx, id, *r.Resources)
		return map[string]bool{"configured": err == nil}, err
	}
	return nil, errors.New("unknown Sandbox management action")
}

func (s *Service) PrepareSandboxResources(ctx context.Context, id string) error {
	if s.options.PrepareSandboxResources != nil {
		return s.options.PrepareSandboxResources(ctx, id)
	}
	return nil
}
