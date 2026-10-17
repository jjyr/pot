package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
)

type MachinePolicy struct {
	ExecutionAccess string `json:"execution_access"`
}

func DefaultMachinePolicy() MachinePolicy { return MachinePolicy{ExecutionAccess: "ask"} }
func (p MachinePolicy) Validate() error {
	if p.ExecutionAccess != "ask" && p.ExecutionAccess != "allow" && p.ExecutionAccess != "deny" {
		return errors.New("invalid execution access policy")
	}
	return nil
}

type permissionFile struct {
	Version  int                      `json:"version"`
	Machines map[string]MachinePolicy `json:"machines"`
	Epochs   map[string]string        `json:"epochs"`
}
type HostRequest struct {
	Owner           string          `json:"owner"`
	Task            string          `json:"task,omitempty"`
	Turn            string          `json:"turn"`
	Generation      uint64          `json:"generation"`
	CallID          string          `json:"call_id"`
	MachineID       string          `json:"machine_id"`
	EnrollmentEpoch string          `json:"enrollment_epoch"`
	Tool            string          `json:"tool"`
	Args            json.RawMessage `json:"args"`
}
type Approval struct {
	ID      string      `json:"id"`
	Request HostRequest `json:"request"`
}
type hostDecision struct {
	approval Approval
	done     chan struct{}
	resolved bool
	action   string
	allow    bool
	invalid  bool
}
type PermissionStore struct {
	mu      sync.Mutex
	path    string
	state   permissionFile
	pending map[string]*hostDecision
	Changes core.Changes
}

func OpenPermissions(path string) (*PermissionStore, error) {
	p := &PermissionStore{path: path, state: permissionFile{5, map[string]MachinePolicy{}, map[string]string{}}, pending: map[string]*hostDecision{}}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err = core.DecodeJSON(body, &p.state); err != nil {
		return nil, err
	}
	if p.state.Version != 5 || p.state.Machines == nil || p.state.Epochs == nil {
		return nil, errors.New("unsupported machine policy format")
	}
	for id, policy := range p.state.Machines {
		if !core.Identifier(id) || p.state.Epochs[id] == "" {
			return nil, errors.New("machine policy enrollment missing")
		}
		if err = policy.Validate(); err != nil {
			return nil, err
		}
	}
	return p, nil
}
func (p *PermissionStore) saveLocked(next permissionFile) error {
	if err := instance.WriteJSON(p.path, next); err != nil {
		return err
	}
	p.state = next
	p.Changes.Notify("permissions")
	return nil
}
func (p *PermissionStore) copyLocked() permissionFile {
	next := permissionFile{5, map[string]MachinePolicy{}, map[string]string{}}
	for k, v := range p.state.Machines {
		next.Machines[k] = v
	}
	for k, v := range p.state.Epochs {
		next.Epochs[k] = v
	}
	return next
}
func (p *PermissionStore) invalidateLocked(machine string) {
	for id, d := range p.pending {
		if d.approval.Request.MachineID == machine {
			d.invalid = true
			if !d.resolved {
				d.resolved = true
				close(d.done)
			}
			delete(p.pending, id)
		}
	}
}

// Enroll creates a fresh epoch and resets policy/temporary decisions. A heartbeat
// must never call it; repeated presence cannot restore revoked Host authority.
func (p *PermissionStore) Enroll(machine, epoch string) error {
	if !core.Identifier(machine) || epoch == "" {
		return errors.New("invalid enrollment identity")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next := p.copyLocked()
	next.Machines[machine] = DefaultMachinePolicy()
	next.Epochs[machine] = epoch
	if err := p.saveLocked(next); err != nil {
		return err
	}
	p.invalidateLocked(machine)
	return nil
}
func (p *PermissionStore) Remove(machine string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := p.copyLocked()
	delete(next.Machines, machine)
	delete(next.Epochs, machine)
	if err := p.saveLocked(next); err != nil {
		return err
	}
	p.invalidateLocked(machine)
	return nil
}
func (p *PermissionStore) Configure(machine string, policy MachinePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.state.Machines[machine]; !ok {
		return errors.New("machine is not enrolled")
	}
	next := p.copyLocked()
	next.Machines[machine] = policy
	if err := p.saveLocked(next); err != nil {
		return err
	}
	p.invalidateLocked(machine)
	return nil
}
func (p *PermissionStore) Policy(machine string) (MachinePolicy, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.state.Machines[machine]
	if !ok {
		return MachinePolicy{}, "", errors.New("machine is not enrolled")
	}
	return v, p.state.Epochs[machine], nil
}
func (r HostRequest) validate() error {
	if r.Owner == "" || r.Turn == "" || r.CallID == "" || !core.Identifier(r.MachineID) || r.EnrollmentEpoch == "" || r.Generation == 0 || r.Generation > core.MaxSafeInteger || r.Tool == "" || !json.Valid(r.Args) || len(r.Args) > 4*1024*1024 {
		return errors.New("invalid exact execution request")
	}
	return nil
}

// Authorize returns persistent=true only for current configured execution access.
// A temporary allow_once covers this exact operation; the dispatcher must still
// inspect its receipt and executor-owned session before doing any work.
var ErrHostDenied = errors.New("Execution operation denied")

func (p *PermissionStore) Authorize(ctx context.Context, r HostRequest) (bool, error) {
	return p.authorize(ctx, r, nil)
}

func (p *PermissionStore) authorize(ctx context.Context, r HostRequest, pending func() error) (persistent bool, err error) {
	r.Args = append(json.RawMessage(nil), r.Args...)
	if err = r.validate(); err != nil {
		return false, err
	}
	hash, err := core.HashJSON(r)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	policy, ok := p.state.Machines[r.MachineID]
	if !ok || p.state.Epochs[r.MachineID] != r.EnrollmentEpoch {
		p.mu.Unlock()
		return false, errors.New("machine enrollment changed")
	}
	// A later broad grant must not reverse an explicit denial of this exact call.
	if previous := p.pending[hash]; previous != nil && previous.resolved && !previous.allow {
		p.mu.Unlock()
		return false, ErrHostDenied
	}
	review := r.Tool == "update_agent_instructions"
	if !review {
		switch policy.ExecutionAccess {
		case "allow":
			p.mu.Unlock()
			return true, nil
		case "deny":
			p.mu.Unlock()
			return false, ErrHostDenied
		}
	}
	// Output polling/cancellation does not create a new command, but the executor
	// must bind the session to this owner/turn. Sending input requires persistence.
	if r.Tool == "write_stdin" {
		var args struct {
			SessionID string `json:"session_id"`
			Chars     string `json:"chars"`
		}
		if err = json.Unmarshal(r.Args, &args); err != nil {
			p.mu.Unlock()
			return false, err
		}
		if args.Chars == "" {
			p.mu.Unlock()
			return false, nil
		}
		p.mu.Unlock()
		return false, errors.New("interactive Host input requires persistent execution access")
	}
	if r.Tool == "exec_command" {
		var args struct {
			TTY bool `json:"tty"`
		}
		if err = json.Unmarshal(r.Args, &args); err != nil {
			p.mu.Unlock()
			return false, err
		}
		if args.TTY {
			p.mu.Unlock()
			return false, errors.New("interactive Host terminal requires persistent execution access")
		}
	}
	d := p.pending[hash]
	if d == nil {
		d = &hostDecision{approval: Approval{hash, r}, done: make(chan struct{})}
		p.pending[hash] = d
		p.Changes.Notify("permissions")
	}
	p.mu.Unlock()
	if pending != nil {
		if err := pending(); err != nil {
			return false, err
		}
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-d.done:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current, exists := p.state.Machines[r.MachineID]
	if d.invalid || !exists || p.state.Epochs[r.MachineID] != r.EnrollmentEpoch || !review && current.ExecutionAccess == "deny" {
		return false, errors.New("execution authorization expired")
	}
	if !d.allow {
		return false, ErrHostDenied
	}
	if !review && current.ExecutionAccess == "allow" {
		return true, nil
	}
	return false, nil
}
func (p *PermissionStore) Pending(owner string) []Approval {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := []Approval{}
	for _, d := range p.pending {
		if !d.resolved && !d.invalid && d.approval.Request.Owner == owner {
			a := d.approval
			a.Request.Args = append(json.RawMessage(nil), a.Request.Args...)
			result = append(result, a)
		}
	}
	return result
}
func (p *PermissionStore) Decide(owner, id, action string) error {
	if action != "allow_once" && action != "allow_always" && action != "deny" {
		return errors.New("invalid execution approval action")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.pending[id]
	if d == nil || d.approval.Request.Owner != owner || d.invalid {
		return errors.New("approval is unavailable for this owner")
	}
	if d.resolved {
		if d.action == action {
			return nil
		}
		return errors.New("approval already decided differently")
	}
	if action == "allow_always" {
		if d.approval.Request.Tool == "update_agent_instructions" {
			return errors.New("instruction changes require exact review")
		}
		r := d.approval.Request
		if p.state.Epochs[r.MachineID] != r.EnrollmentEpoch {
			return errors.New("machine enrollment changed")
		}
		next := p.copyLocked()
		next.Machines[r.MachineID] = MachinePolicy{ExecutionAccess: "allow"}
		if err := p.saveLocked(next); err != nil {
			return err
		}
		// Wake all exact requests on this machine to recheck the saved policy. Keep
		// independent instruction reviews pending; policy never approves rule edits.
		for _, pending := range p.pending {
			if pending.approval.Request.MachineID == r.MachineID && pending.approval.Request.Tool != "update_agent_instructions" && !pending.resolved {
				pending.action = "allow_always"
				pending.allow = true
				pending.resolved = true
				close(pending.done)
			}
		}
		return nil
	}
	allow := action == "allow_once"

	d.action = action
	d.allow = allow
	d.resolved = true
	close(d.done)
	p.Changes.Notify("permissions")
	return nil
}
func (p *PermissionStore) Expire(owner, turn string, generation uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, d := range p.pending {
		r := d.approval.Request
		if r.Owner == owner && r.Turn == turn && r.Generation == generation {
			d.invalid = true
			if !d.resolved {
				d.resolved = true
				close(d.done)
			}
			delete(p.pending, id)
		}
	}
	p.Changes.Notify("permissions")
}
