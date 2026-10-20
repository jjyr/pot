// Package linkhost supervises Agent Link connectivity plugins.
package linkhost

import (
	"context"
	"errors"
	"sync"

	"github.com/jjyr/pot/pkg/agentlink"
)

type AgentPlugin interface {
	ID() string
	Messages() *agentlink.Service
}

// Host is composed before Run. Registration does not grant access or create a
// second connection database; each plugin retains its own authoritative store.
type Host struct {
	agents map[string]AgentPlugin
}

func New() *Host {
	return &Host{agents: map[string]AgentPlugin{}}
}
func (h *Host) RegisterAgent(plugin AgentPlugin) error {
	if plugin == nil || plugin.ID() == "" || plugin.Messages() == nil || h.agents[plugin.ID()] != nil {
		return errors.New("invalid or duplicate Agent Link plugin")
	}
	h.agents[plugin.ID()] = plugin
	return nil
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
