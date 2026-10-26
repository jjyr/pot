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
	"github.com/jjyr/pot/pkg/protocol"
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
	if s.agentLinks != nil && s.agentLinks.service != nil {
		links, err := s.agentLinks.service.List(ctx)
		if err == nil {
			for _, c := range links {
				if c.State == "revoked" || c.State == "disconnected" || c.Directory == nil {
					continue
				}
				sourceVersion, _ := core.HashJSON([]any{s.options.BootID, c.ID, c.Directory})
				if c.Directory.Capabilities(agentlink.NodeTarget(c.PeerHost.NodeID)).Supports(agentlink.Approvals) {
					add("source:"+c.ID, c.Peer.Name, sourceVersion)
				}
				for _, a := range c.Directory.Assistants() {
					d := c.Directory.Capabilities(a.Address)
					if !d.Supports(agentlink.ChatRead) {
						continue
					}
					version, _ := core.HashJSON([]any{s.options.BootID, c.ID, a.Address, d})
					kinds := []string{"reply_completed"}
					if d.Supports(agentlink.TaskSubmit) {
						kinds = append(kinds, "task_completed", "task_interrupted", "task_failed", "task_cancelled")
					}
					if d.Supports(agentlink.Approvals) {
						kinds = append(kinds, "approval_requested", "approval_resolved", "approval_expired")
					}
					add("agent-link:"+c.ID+":"+a.Address.AgentID, a.Name, version, kinds...)
				}
			}
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

// The receiving business boundary binds the authenticated peer event to this
// caller's already committed delegation, even before its receipt arrives.
func (s *Service) receiveApprovalEvent(ctx context.Context, link string, e agentlink.ApprovalEvent) error {
	c, err := s.agentLinks.service.Get(ctx, link)
	if err != nil {
		return err
	}
	if c.State == "revoked" || c.State == "disconnected" || c.Directory == nil || !c.Directory.Capabilities(agentlink.NodeTarget(c.PeerHost.NodeID)).Supports(agentlink.Approvals) {
		return agentlink.ErrUnauthorized
	}
	var task, title string
	err = s.Tasks.DB().QueryRowContext(ctx, `SELECT id,json_extract(body,'$.info.title') FROM tasks WHERE assistant_id=? AND json_extract(body,'$.info.agent_link.link_id')=? AND json_extract(body,'$.execution.request.request_id')=?`, e.To.AgentID, link, e.RequestID).Scan(&task, &title)
	if err != nil {
		return agentlink.ErrUnauthorized
	}
	id := linkedApprovalID(linkedReview{LinkID: link}, e.ID)
	authority := s.LifecycleAuthority(ctx)
	if authority.Sources[link] == "" {
		return agentlink.ErrUnauthorized
	}
	s.Lifecycle.Publish(protocol.LifecycleEvent{ID: id, Kind: "approval_requested", Owner: e.To.AgentID, Task: task, Name: title, Source: link, SourceBoot: authority.Sources[link]})
	return nil
}
