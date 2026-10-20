// Package linkhost supervises typed connectivity plugins. Agent plugins only
// carry peer messages; execution plugins retain original Runner authorities.
package linkhost

import (
	"context"
	"errors"
	"sync"

	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/agentlink"
)

type AgentPlugin interface {
	ID() string
	Messages() *agentlink.Service
}

type ExecutionPlugin interface {
	ID() string
	Connect(context.Context) (*controlplane.Peer, error)
	Target(string) runner.Port
}

// Host is composed before Run. Registration does not grant access or create a
// second connection database; each plugin retains its own authoritative store.
type Host struct {
	agents    map[string]AgentPlugin
	execution map[string]ExecutionPlugin
}

func New() *Host {
	return &Host{agents: map[string]AgentPlugin{}, execution: map[string]ExecutionPlugin{}}
}
func (h *Host) RegisterAgent(plugin AgentPlugin) error {
	if plugin == nil || plugin.ID() == "" || plugin.Messages() == nil || h.agents[plugin.ID()] != nil || h.execution[plugin.ID()] != nil {
		return errors.New("invalid or duplicate Agent Link plugin")
	}
	h.agents[plugin.ID()] = plugin
	return nil
}
func (h *Host) RegisterExecution(plugin ExecutionPlugin) error {
	if plugin == nil || plugin.ID() == "" || h.execution[plugin.ID()] != nil || h.agents[plugin.ID()] != nil {
		return errors.New("invalid or duplicate Execution Link plugin")
	}
	h.execution[plugin.ID()] = plugin
	return nil
}
func (h *Host) Execution(id string) (ExecutionPlugin, error) {
	p := h.execution[id]
	if p == nil {
		return nil, errors.New("execution plugin unavailable")
	}
	return p, nil
}
func (h *Host) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, p := range h.agents {
		wg.Add(1)
		go func(p AgentPlugin) { defer wg.Done(); p.Messages().Run(ctx) }(p)
	}
	wg.Wait()
}

type Messages struct {
	Name    string
	Service *agentlink.Service
}

func (p Messages) ID() string                   { return p.Name }
func (p Messages) Messages() *agentlink.Service { return p.Service }

type Runner struct {
	Directory *controlplane.Directory
	Connector controlplane.Connector
}

func (p Runner) ID() string { return "runner" }
func (p Runner) Connect(ctx context.Context) (*controlplane.Peer, error) {
	return p.Connector.Connect(ctx)
}
func (p Runner) Target(id string) runner.Port {
	return controlplane.RemotePort{Directory: p.Directory, RuntimeID: id}
}
