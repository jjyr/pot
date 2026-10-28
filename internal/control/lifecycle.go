package control

import (
	"context"
	"crypto/rand"
	"slices"
	"strings"
	"sync"

	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"time"
)

type lifecycleAuthority struct {
	mu     sync.Mutex
	owners map[string]hooks.Owner
}

// LifecycleAuthority is access metadata from the owning services, not a reply,
// Task or approval snapshot. No retained chat data grants event-stream access.
func (s *Service) LifecycleAuthority(ctx context.Context) hooks.Authority {
	s.lifecycleAuthority.mu.Lock()
	defer s.lifecycleAuthority.mu.Unlock()
	authority := hooks.Authority{Control: s.options.RuntimeID, Boot: s.options.BootID, Sources: map[string]string{}}
	next := map[string]hooks.Owner{}
	add := func(id, name, version string, kinds ...string) {
		boot := version + "/" + rand.Text()
		if previous, ok := s.lifecycleAuthority.owners[id]; ok && strings.HasPrefix(previous.Boot, version+"/") {
			boot = previous.Boot
		}
		o := hooks.Owner{ID: id, Name: name, Boot: boot, Control: s.options.RuntimeID, Space: s.options.RuntimeID, Kinds: kinds}
		next[id] = o
		if strings.HasPrefix(id, "source:") {
			authority.Sources[strings.TrimPrefix(id, "source:")] = o.Boot
		} else {
			authority.Owners = append(authority.Owners, o)
		}
	}
	if s.Registry != nil {
		defs, err := s.Registry.Definitions(ctx)
		if err == nil {
			for _, d := range defs {
				add(d.ID, d.Name, s.options.BootID)
			}
		}
	}
	connections, e := s.PotMCPConnections(ctx)
	if e == nil {
		incoming, e := s.inboundPotMCPConnections(ctx)
		if e == nil {
			connections = append(connections, incoming...)
		}
	}
	for _, c := range connections {
		if c.State != "active" || c.Directory == nil {
			continue
		}
		version, _ := core.HashJSON([]any{s.options.BootID, c.ID, c.Directory})
		if c.Directory.Capabilities(agentlink.NodeTarget(c.RemoteNodeID)).Supports(agentlink.Approvals) {
			add("source:"+c.ID, c.Directory.Name, version)
		}
		for _, endpoint := range c.Directory.Assistants() {
			d := endpoint.Capabilities
			if !d.Supports(agentlink.ChatRead) {
				continue
			}
			kinds := []string{"reply_completed"}
			if d.Supports(agentlink.TaskSubmit) {
				kinds = append(kinds, "task_completed", "task_interrupted", "task_failed", "task_cancelled")
			}
			if d.Supports(agentlink.Approvals) {
				kinds = append(kinds, "approval_requested", "approval_resolved", "approval_expired")
			}
			add("agent-link:"+c.ID+":"+endpoint.Address.AgentID, endpoint.Name, version, kinds...)
		}
	}
	s.lifecycleAuthority.owners = next
	return authority
}

func (s *Service) lifecycleOwner(ctx context.Context, owner string) string {
	if s.integrationDB != nil {
		var link, assistant string
		if s.integrationDB.QueryRowContext(ctx, "SELECT link_id,assistant_id FROM agent_link_chat_sessions WHERE id=?", owner).Scan(&link, &assistant) == nil {
			return "agent-link:" + link + ":" + assistant
		}
	}
	return owner
}

// StreamLifecycle carries full committed facts over the authenticated owner
// connection. Access changes refresh only authority, never scan business state.
func (s *Service) StreamLifecycle(ctx context.Context, send func(string, any) error) error {
	events, close := s.Lifecycle.Subscribe()
	defer close()
	authority := s.LifecycleAuthority(ctx)
	if err := send("authority", authority); err != nil {
		return err
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-heartbeat.C:
			if err := send("heartbeat", struct{}{}); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case e, ok := <-events:
			if !ok {
				return context.Canceled
			}
			if e.Kind == "authority_changed" {
				authority = s.LifecycleAuthority(ctx)
				if err := send("authority", authority); err != nil {
					return err
				}
				continue
			}
			e.Owner = s.lifecycleOwner(ctx, e.Owner)
			allowed := false
			for _, o := range authority.Owners {
				if o.ID == e.Owner && (len(o.Kinds) == 0 || slices.Contains(o.Kinds, e.Kind)) {
					allowed = true
					break
				}
			}
			if e.Source != "" && authority.Sources[e.Source] != e.SourceBoot {
				allowed = false
			}
			if allowed {
				if err := send("lifecycle", e); err != nil {
					return err
				}
			}
		}
	}
}
