package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/pkg/core"
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
		if err := s.Machines.Remove(ctx, r.RuntimeID); err != nil {
			return nil, err
		}
		if err := s.Permissions.Remove(r.RuntimeID); err != nil {
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

func (s *Service) localMachine(_ context.Context) (map[string]any, error) {
	policy, _, err := s.Permissions.Policy(s.options.RuntimeID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": s.options.RuntimeID, "runtime_id": s.options.RuntimeID, "name": s.options.RuntimeID, "os": runtime.GOOS, "connected": true, "role": s.Machines.Role(), "tags": s.Machines.LocalLabels(), "policy": policy}, nil
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
		policy, _, err := s.Permissions.Policy(m.Identity.RuntimeID)
		if err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"id": m.Identity.RuntimeID, "runtime_id": m.Identity.RuntimeID, "name": m.Identity.RuntimeID, "os": m.OS, "tags": m.Labels, "connected": m.Online, "role": "runner", "policy": policy})
	}
	return items, nil
}
