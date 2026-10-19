// Package execution owns destination-local Agent capacity and lifecycle. Business
// adapters provide a fixed invocation; they never bypass the shared Manager.
package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Phase string

const (
	Queued      Phase = "queued"
	Running     Phase = "running"
	Succeeded   Phase = "succeeded"
	Failed      Phase = "failed"
	Cancelled   Phase = "cancelled"
	Interrupted Phase = "interrupted"
)

var (
	ErrStopping    = errors.New("Runtime is stopping")
	ErrQueueFull   = errors.New("Runtime Agent queue is full")
	ErrDuplicate   = errors.New("Agent execution already registered")
	ErrUnconfirmed = errors.New("Runtime has an unconfirmed Agent execution")
)

type Limits struct {
	MaxActive int `json:"max_active_agents"`
	MaxQueued int `json:"max_queued_agents"`
}

func DefaultLimits() Limits { return Limits{8, 256} }

type Identity struct {
	Owner      string `json:"owner"`
	RequestID  string `json:"request_id"`
	Generation uint64 `json:"generation"`
	Role       string `json:"role"`
}
type Execution struct {
	Identity
	ID        string `json:"execution_id"`
	RuntimeID string `json:"runtime_id"`
	BootID    string `json:"boot_id"`
	Phase     Phase  `json:"phase"`
}

// Result.Unknown fences capacity until the destination has reconciled cleanup.
// Cancellation alone is never evidence that a child operation stopped.
type Result struct {
	Phase Phase
	Err   error
}
type Invocation func(context.Context) Result
type entry struct {
	execution Execution
	ctx       context.Context
	cancel    context.CancelFunc
	start     chan struct{}
	held      bool
}
type Manager struct {
	mu                sync.Mutex
	runtimeID, bootID string
	limits            Limits
	active            map[string]*entry
	recent            []Execution
	queue             []*entry
	held              int
	stopped           bool
	maintenance       func() bool
	changed           chan struct{}
}

func New(runtimeID, bootID string, limits Limits) (*Manager, error) {
	if runtimeID == "" || bootID == "" || limits.MaxActive < 1 || limits.MaxQueued < 1 {
		return nil, errors.New("invalid Runtime execution configuration")
	}
	return &Manager{runtimeID: runtimeID, bootID: bootID, limits: limits, active: make(map[string]*entry), changed: make(chan struct{})}, nil
}
func (m *Manager) notifyLocked() { close(m.changed); m.changed = make(chan struct{}) }
func (m *Manager) pumpLocked() {
	for !m.stopped && m.held < m.limits.MaxActive && len(m.queue) > 0 {
		e := m.queue[0]
		m.queue = m.queue[1:]
		if e.ctx.Err() != nil {
			continue
		}
		e.held = true
		m.held++
		e.execution.Phase = Running
		close(e.start)
	}
}
func (m *Manager) register(ctx context.Context, id Identity) (*entry, error) {
	if id.Owner == "" || id.RequestID == "" || (id.Role != "assistant" && id.Role != "task" && id.Role != "summary") {
		return nil, errors.New("invalid execution identity")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, ErrStopping
	}
	for _, e := range m.active {
		if e.execution.Identity.Owner == id.Owner && e.execution.Identity.RequestID == id.RequestID && e.execution.Identity.Generation == id.Generation {
			return nil, ErrDuplicate
		}
	}
	queued := 0
	for _, e := range m.active {
		if e.execution.Phase == Queued {
			queued++
		}
	}
	if queued >= m.limits.MaxQueued {
		return nil, ErrQueueFull
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, err
	}
	runctx, cancel := context.WithCancel(ctx)
	e := &entry{execution: Execution{id, hex.EncodeToString(entropy[:]), m.runtimeID, m.bootID, Queued}, ctx: runctx, cancel: cancel, start: make(chan struct{})}
	m.active[e.execution.ID] = e
	m.queue = append(m.queue, e)
	m.pumpLocked()
	m.notifyLocked()
	return e, nil
}

// Execute retains capacity until fn has returned after cleanup. A panic or invalid
// outcome is uncertain and fences the slot. fn must honor ctx; shutdown never
// pretends that abandoning a goroutine confirms cleanup.
func (m *Manager) Execute(ctx context.Context, id Identity, fn Invocation) (execution Execution, result Result) {
	if fn == nil {
		return Execution{}, Result{Failed, errors.New("missing Agent invocation")}
	}
	for {
		m.mu.Lock()
		maintenance := m.maintenance
		stopped := m.stopped
		m.mu.Unlock()
		if stopped {
			return Execution{}, Result{Cancelled, ErrStopping}
		}
		if maintenance == nil || !maintenance() {
			break
		}
		select {
		case <-ctx.Done():
			return Execution{}, Result{Cancelled, ctx.Err()}
		case <-time.After(100 * time.Millisecond):
		}
	}
	e, err := m.register(ctx, id)
	if err != nil {
		return Execution{}, Result{Failed, err}
	}
	result = Result{Phase: Interrupted, Err: ErrUnconfirmed}
	defer func() {
		if value := recover(); value != nil {
			result = Result{Interrupted, fmt.Errorf("Agent invocation panicked: %v", value)}
		}
		e.cancel()
		m.mu.Lock()
		defer m.mu.Unlock()
		switch result.Phase {
		case Succeeded, Failed, Cancelled:
		default:
			result.Phase = Interrupted
		}
		e.execution.Phase = result.Phase
		execution = e.execution
		if result.Phase != Interrupted {
			delete(m.active, e.execution.ID)
			for i, q := range m.queue {
				if q == e {
					m.queue = append(m.queue[:i], m.queue[i+1:]...)
					break
				}
			}
			if e.held {
				m.held--
			}
			m.recent = append(m.recent, e.execution)
			if len(m.recent) > 256 {
				m.recent = m.recent[len(m.recent)-256:]
			}
		}
		m.pumpLocked()
		m.notifyLocked()
	}()
	select {
	case <-e.ctx.Done():
		result = Result{Cancelled, e.ctx.Err()}
		return
	case <-e.start:
	}
	if e.ctx.Err() != nil {
		result = Result{Cancelled, e.ctx.Err()}
		return
	}
	result = fn(e.ctx)
	return
}
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.active[id]
	if ok {
		e.cancel()
	}
	return ok
}
func (m *Manager) Snapshot() []Execution {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := append([]Execution(nil), m.recent...)
	for _, e := range m.active {
		result = append(result, e.execution)
	}
	return result
}
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	for _, e := range m.active {
		e.cancel()
	}
	m.notifyLocked()
}
func (m *Manager) Drain(ctx context.Context) error {
	for {
		m.mu.Lock()
		if len(m.active) == 0 {
			m.mu.Unlock()
			return nil
		}
		for _, e := range m.active {
			if e.execution.Phase == Interrupted {
				m.mu.Unlock()
				return ErrUnconfirmed
			}
		}
		changed := m.changed
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

// ReconcileInterrupted releases capacity only after the owning service has
// reconciled original receipts and cleanup. It never invokes or resumes an Agent.
func (m *Manager) ReconcileInterrupted(id Identity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, e := range m.active {
		if e.execution.Identity == id {
			if e.execution.Phase != Interrupted {
				return errors.New("original execution is still active")
			}
			delete(m.active, key)
			if e.held {
				m.held--
			}
			m.recent = append(m.recent, e.execution)
			if len(m.recent) > 256 {
				m.recent = m.recent[len(m.recent)-256:]
			}
			m.pumpLocked()
			m.notifyLocked()
			return nil
		}
	}
	return nil
}

// SetMaintenance holds new Agent invocations while an installation is restarting.
func (m *Manager) SetMaintenance(check func() bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maintenance = check
}
