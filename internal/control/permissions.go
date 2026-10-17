package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/instance"
)

// ask means protected execution, allow explicitly trusts the machine, and deny
// blocks new execution. The owner setting is separate from command grants.
type MachinePolicy struct {
	ExecutionAccess string `json:"execution_access"`
	Revision        uint64 `json:"revision"`
}

func DefaultMachinePolicy() MachinePolicy { return MachinePolicy{ExecutionAccess: "ask", Revision: 1} }
func (p MachinePolicy) Validate() error {
	if p.ExecutionAccess != "ask" && p.ExecutionAccess != "allow" && p.ExecutionAccess != "deny" {
		return errors.New("invalid execution access policy")
	}
	return nil
}

type CommandContext = executor.CommandContext
type CommandRule struct {
	ID              string         `json:"id"`
	MachineID       string         `json:"machine_id"`
	EnrollmentEpoch string         `json:"enrollment_epoch"`
	Context         CommandContext `json:"context"`
	Policy          string         `json:"policy"`
	UpdatedBy       string         `json:"updated_by"`
	UpdatedAt       string         `json:"updated_at"`
}
type TaskGrant struct {
	ID              string         `json:"id"`
	MachineID       string         `json:"machine_id"`
	EnrollmentEpoch string         `json:"enrollment_epoch"`
	Owner           string         `json:"owner"`
	Task            string         `json:"task,omitempty"`
	Turn            string         `json:"turn"`
	Generation      uint64         `json:"generation"`
	Context         CommandContext `json:"context"`
}
type permissionFile struct {
	Version      int                      `json:"version"`
	Machines     map[string]MachinePolicy `json:"machines"`
	Epochs       map[string]string        `json:"epochs"`
	Rules        map[string]CommandRule   `json:"rules"`
	Applications map[string]ApplyState    `json:"applications"`
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
	Command         *CommandContext `json:"command_context,omitempty"`
	Host            bool            `json:"host_execution"`
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
type ApplyState struct {
	Revision uint64 `json:"revision"`
	State    string `json:"state"`
	Error    string `json:"error,omitempty"`
}
type PermissionStore struct {
	denied  map[string]HostRequest
	mu      sync.Mutex
	path    string
	state   permissionFile
	pending map[string]*hostDecision
	grants  map[string]TaskGrant
	applied map[string]ApplyState
	// Apply drains affected execution and synchronizes Runner admission. Failed
	// application leaves admission fenced and is visible to owner settings.
	Apply   func(string, uint64) error
	Changes core.Changes
}

func OpenPermissions(path string) (*PermissionStore, error) {
	p := &PermissionStore{path: path, state: permissionFile{6, map[string]MachinePolicy{}, map[string]string{}, map[string]CommandRule{}, map[string]ApplyState{}}, denied: map[string]HostRequest{}, pending: map[string]*hostDecision{}, grants: map[string]TaskGrant{}}
	p.applied = p.state.Applications
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var stored permissionFile
	if err = core.DecodeJSON(body, &stored); err != nil {
		return nil, err
	}
	p.state = stored
	if p.state.Version != 6 || p.state.Machines == nil || p.state.Epochs == nil || p.state.Rules == nil || p.state.Applications == nil {
		return nil, errors.New("unsupported permission format; preserve the file and explicitly reconfigure machine policies")
	}
	p.applied = p.state.Applications
	for machine, application := range p.applied {
		if policy, ok := p.state.Machines[machine]; !ok || application.Revision != policy.Revision || (application.State != "applied" && application.State != "pending" && application.State != "applying") {
			return nil, errors.New("invalid permission application state")
		}
	}
	for id, policy := range p.state.Machines {
		if !core.Identifier(id) || p.state.Epochs[id] == "" || policy.Revision == 0 {
			return nil, errors.New("invalid machine policy enrollment")
		}
		if err = policy.Validate(); err != nil {
			return nil, err
		}
	}
	for id, rule := range p.state.Rules {
		if err = p.validateRule(rule); err != nil || id != rule.ID {
			return nil, errors.New("invalid stored command rule")
		}
	}
	return p, nil
}
func (p *PermissionStore) saveLocked(next permissionFile) error {
	if err := instance.WriteJSON(p.path, next); err != nil {
		return err
	}
	p.state = next
	p.applied = next.Applications
	p.Changes.Notify("permissions")
	return nil
}
func (p *PermissionStore) copyLocked() permissionFile {
	n := permissionFile{6, map[string]MachinePolicy{}, map[string]string{}, map[string]CommandRule{}, map[string]ApplyState{}}
	for k, v := range p.state.Machines {
		n.Machines[k] = v
	}
	for k, v := range p.state.Epochs {
		n.Epochs[k] = v
	}
	for k, v := range p.state.Rules {
		n.Rules[k] = v
	}
	for k, v := range p.applied {
		n.Applications[k] = v
	}
	return n
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
	for id, g := range p.grants {
		if g.MachineID == machine {
			delete(p.grants, id)
		}
	}
}
func (p *PermissionStore) apply(machine string, revision uint64) error {
	var err error
	if p.Apply != nil {
		err = p.Apply(machine, revision)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if current := p.applied[machine]; current.Revision == revision {
		state := ApplyState{Revision: revision, State: "applied"}
		if err != nil {
			state.State = "pending"
			state.Error = err.Error()
		}
		next := p.copyLocked()
		next.Applications[machine] = state
		if saveErr := p.saveLocked(next); saveErr != nil {
			return errors.Join(err, saveErr)
		}
	}
	p.Changes.Notify("permissions")
	return err
}
func (p *PermissionStore) Enroll(machine, epoch string) error {
	if !core.Identifier(machine) || epoch == "" {
		return errors.New("invalid enrollment identity")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next := p.copyLocked()
	next.Machines[machine] = DefaultMachinePolicy()
	next.Epochs[machine] = epoch
	delete(next.Applications, machine)
	for id, r := range next.Rules {
		if r.MachineID == machine {
			delete(next.Rules, id)
		}
	}
	if err := p.saveLocked(next); err != nil {
		return err
	}
	p.invalidateLocked(machine)
	delete(p.applied, machine)
	return nil
}
func (p *PermissionStore) Remove(machine string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := p.copyLocked()
	delete(next.Machines, machine)
	delete(next.Epochs, machine)
	delete(next.Applications, machine)
	for id, r := range next.Rules {
		if r.MachineID == machine {
			delete(next.Rules, id)
		}
	}
	if err := p.saveLocked(next); err != nil {
		return err
	}
	p.invalidateLocked(machine)
	delete(p.applied, machine)
	return nil
}
func (p *PermissionStore) Configure(machine string, policy MachinePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	p.mu.Lock()
	current, ok := p.state.Machines[machine]
	if !ok {
		p.mu.Unlock()
		return errors.New("machine is not enrolled")
	}
	if policy.Revision != 0 && policy.Revision != current.Revision {
		p.mu.Unlock()
		return errors.New("permission policy changed; reload settings")
	}
	next := p.copyLocked()
	policy.Revision = current.Revision + 1
	next.Machines[machine] = policy
	next.Applications[machine] = ApplyState{Revision: policy.Revision, State: "applying"}
	if err := p.saveLocked(next); err != nil {
		p.mu.Unlock()
		return err
	}
	p.invalidateLocked(machine)
	p.mu.Unlock()
	return p.apply(machine, policy.Revision)
}
func (p *PermissionStore) Policy(machine string) (MachinePolicy, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.state.Machines[machine]
	if !ok {
		return v, "", errors.New("machine is not enrolled")
	}
	return v, p.state.Epochs[machine], nil
}
func ruleID(machine, epoch string, c CommandContext) string {
	key, _ := core.HashJSON(struct {
		Machine, Epoch string
		Context        CommandContext
	}{machine, epoch, c})
	return key
}
func grantID(r HostRequest) string {
	key, _ := core.HashJSON(struct {
		Machine, Epoch, Owner, Task, Turn string
		Generation                        uint64
		Context                           *CommandContext
	}{r.MachineID, r.EnrollmentEpoch, r.Owner, r.Task, r.Turn, r.Generation, r.Command})
	return key
}
func denialID(r HostRequest) string {
	key, _ := core.HashJSON(struct {
		Machine, Epoch, Owner, Task, Turn, Call string
		Generation                              uint64
	}{r.MachineID, r.EnrollmentEpoch, r.Owner, r.Task, r.Turn, r.CallID, r.Generation})
	return key
}
func (p *PermissionStore) validateRule(r CommandRule) error {
	if p.state.Epochs[r.MachineID] != r.EnrollmentEpoch || r.EnrollmentEpoch == "" {
		return errors.New("rule enrollment changed")
	}
	if err := r.Context.Validate(); err != nil {
		return err
	}
	if r.Policy != "allow_host" && r.Policy != "default" && r.Policy != "deny_host" {
		return errors.New("invalid command rule policy")
	}
	if r.ID != ruleID(r.MachineID, r.EnrollmentEpoch, r.Context) {
		return errors.New("command rule identity differs")
	}
	return nil
}
func (p *PermissionStore) SaveRule(owner string, r CommandRule, oldID string, revision uint64) error {
	p.mu.Lock()
	r.EnrollmentEpoch = p.state.Epochs[r.MachineID]
	r.ID = ruleID(r.MachineID, r.EnrollmentEpoch, r.Context)
	if err := p.validateRule(r); err != nil {
		p.mu.Unlock()
		return err
	}
	policy := p.state.Machines[r.MachineID]
	if revision != policy.Revision {
		p.mu.Unlock()
		return errors.New("permission policy changed; reload settings")
	}
	next := p.copyLocked()
	if oldID != "" {
		old, ok := next.Rules[oldID]
		if !ok || old.MachineID != r.MachineID {
			p.mu.Unlock()
			return errors.New("command rule no longer exists")
		}
		delete(next.Rules, oldID)
	}
	r.UpdatedBy = owner
	r.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	next.Rules[r.ID] = r
	policy.Revision++
	next.Machines[r.MachineID] = policy
	next.Applications[r.MachineID] = ApplyState{Revision: policy.Revision, State: "applying"}
	if err := p.saveLocked(next); err != nil {
		p.mu.Unlock()
		return err
	}
	p.invalidateLocked(r.MachineID)
	p.mu.Unlock()
	return p.apply(r.MachineID, policy.Revision)
}
func (p *PermissionStore) DeleteRule(machine, id string, revision uint64) error {
	p.mu.Lock()
	r, ok := p.state.Rules[id]
	policy := p.state.Machines[machine]
	if !ok || r.MachineID != machine || policy.Revision != revision {
		p.mu.Unlock()
		return errors.New("command rule changed; reload settings")
	}
	next := p.copyLocked()
	delete(next.Rules, id)
	policy.Revision++
	next.Machines[machine] = policy
	next.Applications[machine] = ApplyState{Revision: policy.Revision, State: "applying"}
	if err := p.saveLocked(next); err != nil {
		p.mu.Unlock()
		return err
	}
	p.invalidateLocked(machine)
	p.mu.Unlock()
	return p.apply(machine, policy.Revision)
}
func (p *PermissionStore) RemoveGrant(id string) error {
	p.mu.Lock()
	g, ok := p.grants[id]
	p.mu.Unlock()
	if !ok {
		return errors.New("task grant no longer exists")
	}
	policy, _, err := p.Policy(g.MachineID)
	if err != nil {
		return err
	}
	return p.Configure(g.MachineID, policy)
}
func (p *PermissionStore) Snapshot() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	rules := []CommandRule{}
	for _, r := range p.state.Rules {
		rules = append(rules, r)
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	grants := []TaskGrant{}
	for _, g := range p.grants {
		grants = append(grants, g)
	}
	machines := map[string]MachinePolicy{}
	for k, v := range p.state.Machines {
		machines[k] = v
	}
	applied := map[string]ApplyState{}
	for k, v := range p.applied {
		applied[k] = v
	}
	return map[string]any{"machines": machines, "rules": rules, "task_grants": grants, "application": applied}
}
func (r HostRequest) validate() error {
	if r.Owner == "" || r.Turn == "" || r.CallID == "" || !core.Identifier(r.MachineID) || r.EnrollmentEpoch == "" || r.Generation == 0 || r.Tool == "" || !json.Valid(r.Args) {
		return errors.New("invalid exact execution request")
	}
	if r.Command != nil {
		return r.Command.Validate()
	}
	return nil
}

var ErrHostDenied = errors.New("Execution operation denied")

func (p *PermissionStore) Authorize(ctx context.Context, r HostRequest) (string, error) {
	return p.authorize(ctx, r, nil)
}
func (p *PermissionStore) authorize(ctx context.Context, r HostRequest, pending func() error) (string, error) {
	r.Args = append(json.RawMessage(nil), r.Args...)
	if err := r.validate(); err != nil {
		return "", err
	}
	hash, err := core.HashJSON(r)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	policy, ok := p.state.Machines[r.MachineID]
	if !ok || p.state.Epochs[r.MachineID] != r.EnrollmentEpoch {
		p.mu.Unlock()
		return "", errors.New("machine enrollment changed")
	}
	review := r.Tool == "update_agent_instructions"
	var stdin struct {
		Chars string `json:"chars"`
	}
	_ = json.Unmarshal(r.Args, &stdin)
	if r.Tool == "write_stdin" && stdin.Chars == "" {
		p.mu.Unlock()
		return "session", nil
	}
	if _, denied := p.denied[denialID(r)]; denied {
		p.mu.Unlock()
		return "", ErrHostDenied
	}
	if previous := p.pending[hash]; previous != nil && previous.resolved && !previous.allow {
		p.mu.Unlock()
		return "", ErrHostDenied
	}
	if !review {
		if policy.ExecutionAccess == "deny" {
			p.mu.Unlock()
			return "", ErrHostDenied
		}
		if state, ok := p.applied[r.MachineID]; ok && state.State != "applied" {
			p.mu.Unlock()
			return "", fmt.Errorf("permission update %s: %s", state.State, state.Error)
		}
		if r.Tool == "write_stdin" {
			p.mu.Unlock()
			return "session", nil
		}
		rule := CommandRule{}
		if r.Command != nil {
			rule = p.state.Rules[ruleID(r.MachineID, r.EnrollmentEpoch, *r.Command)]
		}
		if rule.Policy == "deny_host" {
			p.mu.Unlock()
			if r.Host {
				return "", ErrHostDenied
			}
			return "sandbox", nil
		}
		if policy.ExecutionAccess == "allow" || rule.Policy == "allow_host" {
			p.mu.Unlock()
			return "host", nil
		}
		if r.Command != nil {
			if _, ok := p.grants[grantID(r)]; ok {
				p.mu.Unlock()
				return "host", nil
			}
		}
		if !r.Host {
			p.mu.Unlock()
			return "sandbox", nil
		}
		if r.Command == nil {
			p.mu.Unlock()
			return "", errors.New("host exception requires an exact shell command; plugin host exceptions are not supported")
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
			return "", err
		}
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-d.done:
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	current, exists := p.state.Machines[r.MachineID]
	if d.invalid || !exists || p.state.Epochs[r.MachineID] != r.EnrollmentEpoch || !review && current.ExecutionAccess == "deny" {
		return "", errors.New("execution authorization expired")
	}
	if !d.allow {
		return "", ErrHostDenied
	}
	return "host", nil
}
func (p *PermissionStore) Pending(owner string) []Approval {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []Approval{}
	for _, d := range p.pending {
		if !d.resolved && !d.invalid && d.approval.Request.Owner == owner {
			out = append(out, d.approval)
		}
	}
	return out
}
func (p *PermissionStore) Decide(owner, id, action string) error {
	if action != "allow_once" && action != "allow_task" && action != "allow_always" && action != "deny" {
		return errors.New("invalid approval action")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.pending[id]
	if d == nil || d.invalid || d.approval.Request.Owner != owner {
		return errors.New("approval is unavailable for this owner")
	}
	if d.resolved {
		if d.action == action {
			return nil
		}
		return errors.New("approval already decided differently")
	}
	r := d.approval.Request
	review := r.Tool == "update_agent_instructions"
	if review && (action == "allow_task" || action == "allow_always") {
		return errors.New("instruction changes require exact review")
	}
	if !review && action == "allow_once" {
		return errors.New("command authorization requires task or permanent scope")
	}
	if action == "allow_always" {
		if r.Command == nil {
			return errors.New("command context required")
		}
		next := p.copyLocked()
		rule := CommandRule{ID: ruleID(r.MachineID, r.EnrollmentEpoch, *r.Command), MachineID: r.MachineID, EnrollmentEpoch: r.EnrollmentEpoch, Context: *r.Command, Policy: "allow_host", UpdatedBy: owner, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
		next.Rules[rule.ID] = rule
		if err := p.saveLocked(next); err != nil {
			return err
		}
	}
	if action == "allow_task" {
		if r.Command == nil {
			return errors.New("command context required")
		}
		key := grantID(r)
		p.grants[key] = TaskGrant{ID: key, MachineID: r.MachineID, EnrollmentEpoch: r.EnrollmentEpoch, Owner: r.Owner, Task: r.Task, Turn: r.Turn, Generation: r.Generation, Context: *r.Command}
	}
	if action == "deny" {
		p.denied[denialID(r)] = r
	}
	d.action = action
	d.allow = action != "deny"
	d.resolved = true
	close(d.done)
	if action == "allow_always" || action == "allow_task" {
		for key, waiting := range p.pending {
			q := waiting.approval.Request
			if waiting.resolved || q.Command == nil || q.Tool != "exec_command" {
				continue
			}
			same := ruleID(q.MachineID, q.EnrollmentEpoch, *q.Command) == ruleID(r.MachineID, r.EnrollmentEpoch, *r.Command)
			if action == "allow_task" {
				same = same && grantID(q) == grantID(r)
			}
			if same {
				waiting.action = action
				waiting.allow = true
				waiting.resolved = true
				close(waiting.done)
				_ = key
			}
		}
	}
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
	for id, r := range p.denied {
		if r.Owner == owner && r.Turn == turn && r.Generation == generation {
			delete(p.denied, id)
		}
	}
	for id, g := range p.grants {
		if g.Owner == owner && g.Turn == turn && g.Generation == generation {
			delete(p.grants, id)
		}
	}
	p.Changes.Notify("permissions")
}

// CheckRevision is the final local admission check after preparation. Policy
// edits close existing Engines, so an already-preparing launch cannot slip in.
func (p *PermissionStore) CheckRevision(machine, epoch string, revision uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.state.Machines[machine]
	if !ok || p.state.Epochs[machine] != epoch || v.Revision != revision {
		return errors.New("execution permission revision changed")
	}
	if state, ok := p.applied[machine]; ok && state.State != "applied" {
		return errors.New("permission update is not yet applied")
	}
	return nil
}

// Admit holds policy serialization only across the actual process start.
func (p *PermissionStore) Admit(machine string, revision uint64) (func(), error) {
	p.mu.Lock()
	v, ok := p.state.Machines[machine]
	state := p.applied[machine]
	if !ok || v.Revision != revision || v.ExecutionAccess == "deny" || state.State != "" && state.State != "applied" {
		p.mu.Unlock()
		return nil, errors.New("execution authority changed before process start")
	}
	return p.mu.Unlock, nil
}

func (p *PermissionStore) Retry(machine string, revision uint64) error {
	p.mu.Lock()
	policy, ok := p.state.Machines[machine]
	if !ok || policy.Revision != revision {
		p.mu.Unlock()
		return errors.New("permission policy changed")
	}
	next := p.copyLocked()
	next.Applications[machine] = ApplyState{Revision: revision, State: "applying"}
	if err := p.saveLocked(next); err != nil {
		p.mu.Unlock()
		return err
	}
	p.mu.Unlock()
	return p.apply(machine, revision)
}
