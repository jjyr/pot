package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"net/url"
	"sort"
	"strings"
)

func (s *Service) Completions(ctx context.Context, owner, query string) (map[string]any, error) {
	if len(query) > 512 || strings.ContainsAny(query, "\x00\r\n") {
		return nil, errors.New("invalid completion query")
	}
	if _, err := s.Conversation(ctx, owner); err != nil {
		return nil, err
	}
	candidates := []protocol.Completion{}
	if strings.HasPrefix(query, "@") {
		var err error
		candidates, err = s.assistantCompletions(ctx, owner)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		candidates, err = s.skillCompletions(ctx, owner)
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Token < candidates[j].Token })
	items := []protocol.Completion{}
	seen := map[string]bool{}
	for _, item := range candidates {
		if !seen[item.Token] && item.Matches(strings.TrimLeft(query, "/@")) {
			items = append(items, item)
			seen[item.Token] = true
			if len(items) == 50 {
				break
			}
		}
	}
	return map[string]any{"items": items}, nil
}

func (s *Service) skillCompletions(ctx context.Context, owner string) ([]protocol.Completion, error) {
	target := core.RuntimeTarget(s.options.RuntimeID)
	_, _, err := s.Permissions.Policy(s.options.RuntimeID)
	if err != nil {
		return nil, err
	}

	prepared, err := s.prepareExecution(ctx, owner, target, false, "")
	if err != nil {
		return nil, err
	}
	skills, err := agent.DiscoverSkills(prepared.SkillResources)
	if err != nil {
		return nil, err
	}
	candidates := []protocol.Completion{}
	for _, skill := range skills.Skills {
		candidates = append(candidates, protocol.Completion{Token: "/skill:" + skill.Name, Label: skill.Name, Detail: skill.Description})
	}
	return candidates, nil
}

// Mentions are references for the current assistant, never a chat destination.
// Read directory snapshots only; opening a picker must not connect to a peer.
func (s *Service) assistantCompletions(ctx context.Context, owner string) ([]protocol.Completion, error) {
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return nil, err
	}
	items := []protocol.Completion{}
	add := func(address protocol.AgentRef, name, image, source string, offline bool) {
		if address.Kind != "assistant" || !address.Valid() || address.NodeID == s.options.RuntimeID && address.AgentID == owner {
			return
		}
		items = append(items, protocol.Completion{Token: "@assistant:" + address.NodeID + "/" + address.AgentID, Label: name, Detail: source, Avatar: image, Offline: offline, Local: address.NodeID == s.options.RuntimeID})
	}
	for _, definition := range definitions {
		add(protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "assistant", AgentID: definition.ID}, definition.Name, definition.Avatar, s.options.RuntimeID, false)
	}
	if s.agentLinks != nil {
		links, err := s.agentLinks.service.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			if link.State == "revoked" || link.Directory == nil {
				continue
			}
			source := link.Directory.Name
			if endpoint, err := url.Parse(link.Endpoint); err == nil && endpoint.Host != "" {
				source += " · " + endpoint.Host
			}
			for _, assistant := range link.Directory.Assistants {
				if assistant.Address.NodeID != link.Peer.Address.NodeID {
					continue
				}
				add(assistant.Address, assistant.Name, assistant.Avatar, source, link.State == "disconnected" || link.LastSeen == nil)
			}
		}
	}
	return items, nil
}
