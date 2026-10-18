package core

import (
	"context"
	"encoding/json"
	"io"
	"sync"
)

type Outcome struct {
	Kind  string `json:"kind"`
	Error string `json:"error,omitempty"`
}

var (
	Success   = Outcome{Kind: "success"}
	Cancelled = Outcome{Kind: "cancelled"}
	Unknown   = Outcome{Kind: "unknown"}
)

type Update struct {
	ExecutionMode string           `json:"execution_mode,omitempty"`
	ParentID      string           `json:"parent_id,omitempty"`
	AtMS          int64            `json:"at_ms,omitempty"`
	Phase         string           `json:"phase,omitempty"`
	Target        *ExecutionTarget `json:"target,omitempty"`
	Workdir       string           `json:"workdir,omitempty"`
	Type          string           `json:"type"`
	Text          string           `json:"text,omitempty"`
	Outcome       *Outcome         `json:"outcome,omitempty"`
	ID            string           `json:"id,omitempty"`
	Name          string           `json:"name,omitempty"`
	Args          json.RawMessage  `json:"args,omitempty"`
	Result        json.RawMessage  `json:"result,omitempty"`
	Failed        bool             `json:"failed,omitempty"`
	Stream        string           `json:"stream,omitempty"`
	Recovered     bool             `json:"recovered,omitempty"`
}
type AgentEvent struct {
	Generation uint64 `json:"generation"`
	TurnID     string `json:"turn_id"`
	Seq        uint64 `json:"seq"`
	Update     Update `json:"update"`
}
type EventGate struct {
	generation uint64
	turnID     string
	sequence   uint64
	terminal   *Outcome
}

func NewEventGate(generation uint64, turnID string) *EventGate {
	return &EventGate{generation: generation, turnID: turnID}
}
func (g *EventGate) Accept(event AgentEvent) bool {
	if event.Generation != g.generation || event.TurnID != g.turnID || event.Seq <= g.sequence || g.terminal != nil {
		return false
	}
	g.sequence = event.Seq
	if event.Update.Type == "turn_finished" && event.Update.Outcome != nil {
		v := *event.Update.Outcome
		g.terminal = &v
	}
	return true
}
func (g *EventGate) Finish() Outcome {
	if g.terminal == nil {
		return Unknown
	}
	return *g.terminal
}

type TurnRequest struct {
	Attachments []Attachment
	RequestID   string
	Text        string
}
type ExecutionRole string

const (
	Main    ExecutionRole = "main"
	Worker  ExecutionRole = "worker"
	Summary ExecutionRole = "summary"
)

type SkillRoot struct {
	Path     string
	Location string
	Builtin  bool
}
type SkillResources struct {
	Roots         []SkillRoot
	ReservedNames []string
	Selection     *[]string
}
type ContextSection struct {
	Key  string
	Text string
}
type ExecutionContext struct {
	Instructions    string
	ContextSections []ContextSection
	SkillResources  SkillResources
	Data            string
	Workspace       string
	Owner           string
	Role            ExecutionRole
	MainPrompt      string
}
type ModelRequest struct {
	ID           string          `json:"id" required:"true"`
	Protocol     string          `json:"protocol" required:"true"`
	Provider     string          `json:"provider" required:"true"`
	Model        string          `json:"model" required:"true"`
	Body         json.RawMessage `json:"body" required:"true"`
	CompactionID *string         `json:"compaction_id"`
}
type ExecutionServices interface {
	Model(context.Context, ModelRequest) (io.ReadCloser, error)
	RPC(context.Context, *string, string, string, json.RawMessage) (json.RawMessage, *string, error)
	Compaction(context.Context, json.RawMessage) error
}
type ToolExecutor interface {
	Execute(context.Context, ExecutionContext, ShellSettings, string, json.RawMessage, string, func(Update) error) (json.RawMessage, error)
	Finish(context.Context, ExecutionContext) error
	Abort(context.Context, ExecutionContext) error
}

// Changes coalesces dirty topics instead of queueing snapshots. Subscribe before
// reading initial state; a slow reader still observes every changed topic.
type Changes struct {
	mu        sync.Mutex
	sequence  uint64
	versions  map[string]uint64
	listeners map[*Subscription]struct{}
}
type Subscription struct {
	changes *Changes
	wake    chan struct{}
	once    sync.Once
	seen    uint64
	initial bool
}

func (c *Changes) Notify(topic string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.versions == nil {
		c.versions = map[string]uint64{}
	}
	c.sequence++
	c.versions[topic] = c.sequence
	for listener := range c.listeners {
		select {
		case listener.wake <- struct{}{}:
		default:
		}
	}
}
func (c *Changes) Subscribe() *Subscription {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &Subscription{changes: c, wake: make(chan struct{}, 1), initial: true}
	if c.listeners == nil {
		c.listeners = map[*Subscription]struct{}{}
	}
	c.listeners[s] = struct{}{}
	return s
}
func (s *Subscription) Next(ctx context.Context) (map[string]uint64, error) {
	for {
		s.changes.mu.Lock()
		if s.initial || s.seen != s.changes.sequence {
			s.initial = false
			s.seen = s.changes.sequence
			result := map[string]uint64{}
			for key, value := range s.changes.versions {
				result[key] = value
			}
			s.changes.mu.Unlock()
			return result, nil
		}
		s.changes.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.wake:
		}
	}
}
func (s *Subscription) Close() {
	s.once.Do(func() { s.changes.mu.Lock(); delete(s.changes.listeners, s); s.changes.mu.Unlock() })
}
