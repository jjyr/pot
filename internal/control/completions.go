package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"sort"
	"strings"
)

func (s *Service) Completions(ctx context.Context, owner, query string) (map[string]any, error) {
	if len(query) > 256 || strings.ContainsAny(query, "\x00\r\n") {
		return nil, errors.New("invalid completion query")
	}
	if _, err := s.Conversation(ctx, owner); err != nil {
		return nil, err
	}
	candidates := []protocol.Completion{}
	tags := map[string]bool{}
	addMachine := func(id string, labels []string, detail string) {
		candidates = append(candidates, protocol.Completion{Token: "/machine:" + id, Label: id, Detail: detail})
		for _, label := range labels {
			if protocol.ValidLabel(label) {
				tags[label] = true
			}
		}
	}
	addMachine(s.options.RuntimeID, s.Machines.LocalLabels(), "Local machine")
	for label := range tags {
		candidates = append(candidates, protocol.Completion{Token: "/tag:" + label, Label: label, Detail: "Machine label"})
	}
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
	for _, skill := range skills.Skills {
		candidates = append(candidates, protocol.Completion{Token: "/skill:" + skill.Name, Label: skill.Name, Detail: skill.Description})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Token < candidates[j].Token })
	items := []protocol.Completion{}
	seen := map[string]bool{}
	for _, item := range candidates {
		if !seen[item.Token] && item.Matches(strings.TrimPrefix(query, "/")) {
			items = append(items, item)
			seen[item.Token] = true
			if len(items) == 50 {
				break
			}
		}
	}
	return map[string]any{"items": items}, nil
}
