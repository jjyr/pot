package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/protocol"
)

type scheduleProbes struct{ s *Service }

func (p scheduleProbes) RunProbe(ctx context.Context, r schedule.ProbeRequest) (result protocol.ScheduleHookResult, resultErr error) {
	s := p.s
	result.Status = "error"
	args, _ := json.Marshal(r)
	owner := "schedule-probe-" + r.ScheduleID
	op := Operation{Owner: owner, Turn: r.OccurrenceID, Generation: r.Plan.Generation + 1, CallID: "before_run", Target: core.ExecutionTarget{RuntimeID: r.Plan.Info.Spec.MachineID}, Tool: "schedule_before_run", Args: args}
	if r.ReconcileOnly {
		key, e := op.Key()
		if e != nil {
			return result, errors.Join(core.ErrCleanupUnconfirmed, e)
		}
		receipt, e := s.Operations.Get(ctx, key)
		if errors.Is(e, sql.ErrNoRows) {
			return protocol.ScheduleHookResult{Status: "not_started"}, nil
		}
		expected, _ := core.HashJSON(op)
		if e != nil || receipt.State != "complete" || receipt.InputHash != expected {
			return result, errors.Join(core.ErrCleanupUnconfirmed, e)
		}
		if e = core.DecodeJSON(receipt.Result, &result); e != nil {
			return result, errors.Join(core.ErrCleanupUnconfirmed, e)
		}
		return result, nil
	}
	receipt, claimed, err := s.Operations.Begin(ctx, op)
	if err != nil {
		return result, errors.Join(core.ErrCleanupUnconfirmed, err)
	}
	if !claimed {
		if err = core.DecodeJSON(receipt.Result, &result); err != nil {
			return result, errors.Join(core.ErrCleanupUnconfirmed, err)
		}
		return result, nil
	}
	defer func() {
		settle, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if core.IsCleanupUnconfirmed(resultErr) {
			_ = s.Operations.Unknown(settle, receipt.ID)
			return
		}
		if resultErr != nil {
			result.Status = "error"
			result.Diagnostic = probeDiagnostic(resultErr.Error())
			resultErr = nil
		}
		raw, _ := json.Marshal(result)
		if err := s.Operations.Complete(settle, receipt.ID, raw, nil); err != nil {
			_ = s.Operations.Unknown(settle, receipt.ID)
			resultErr = errors.Join(core.ErrCleanupUnconfirmed, err)
		}
	}()
	if _, err = s.Tasks.LocalMachine(ctx, r.Plan.Info.Spec.MachineID); err != nil {
		return result, err
	}
	config := r.Plan.Info.Spec.BeforeRun
	if config == nil {
		return result, fmt.Errorf("probe configuration missing")
	}
	workspace, err := s.ExecutionWorkspace(r.Owner)
	if err != nil {
		return result, err
	}
	cwd := config.CWD
	if cwd == "" {
		cwd = workspace
	} else if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(workspace, cwd)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return result, err
	}
	// argv execution uses machine policy. Shell exact-command grants do not grant
	// authority to an argv command; no unattended approval is manufactured.
	policy, epoch, err := s.Permissions.Policy(r.Plan.Info.Spec.MachineID)
	if err != nil {
		return result, err
	}
	mode, err := s.Permissions.Authorize(ctx, HostRequest{Owner: r.Owner, Turn: r.OccurrenceID, Generation: r.Plan.Generation + 1, CallID: "before_run", MachineID: r.Plan.Info.Spec.MachineID, EnrollmentEpoch: epoch, Tool: "schedule_before_run", Args: args})
	if err != nil {
		return result, err
	}
	result.ExecutionMode = mode
	if err = s.Permissions.CheckRevision(r.Plan.Info.Spec.MachineID, epoch, policy.Revision); err != nil {
		return result, err
	}
	engine, err := s.host(owner, r.Plan.Generation+1)
	if err != nil {
		return result, err
	}
	defer func() {
		cleanup := engine.Close()
		resultErr = errors.Join(resultErr, cleanup)
		if cleanup == nil {
			s.mu.Lock()
			if s.hosts[owner] == engine {
				delete(s.hosts, owner)
				delete(s.hostGenerations, owner)
			}
			s.mu.Unlock()
		}
	}()
	input, _ := json.Marshal(struct {
		ScheduleID   string `json:"schedule_id"`
		OccurrenceID string `json:"occurrence_id"`
		Trigger      string `json:"trigger"`
		ScheduledAt  string `json:"scheduled_at"`
		Timezone     string `json:"timezone"`
	}{r.ScheduleID, r.OccurrenceID, r.Trigger, r.ScheduledAt, r.Timezone})
	timeout := config.TimeoutSeconds
	if timeout == 0 {
		timeout = 30
	}
	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	shell := core.DefaultShellSettings()
	shell.MaxOutputBytes = 16384
	shell.DefaultOutputBytes = 16384
	shell.MaxYieldMS = 1000
	shell.DefaultYieldMS = 1000
	value, err := engine.RunCommand(commandCtx, executor.Request{Mode: mode, Revision: policy.Revision, ID: "before_run", CWD: cwd, Policy: shell}, executor.CommandInput{Argv: config.Command, Input: string(input) + "\n"})
	if err != nil {
		return result, err
	}
	if value["exit_code"] != 0 || value["truncated"] == true || value["invalid_utf8"] == true {
		stderr, _ := value["stderr"].(string)
		return result, fmt.Errorf("probe failed (exit=%v, truncated=%v): %s", value["exit_code"], value["truncated"], probeDiagnostic(stderr))
	}
	stdout, _ := value["stdout"].(string)
	result, err = schedule.ParseProbe([]byte(stdout))
	result.ExecutionMode = mode
	return result, err
}
func probeDiagnostic(s string) string {
	if len(s) > 2000 {
		s = s[:2000]
	}
	return strings.ToValidUTF8(s, "")
}
