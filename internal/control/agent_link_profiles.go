package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func (b potLinkBackend) Profile(ctx context.Context, r agentlink.ProfileRequest) (agentlink.Profile, error) {
	if r.Action != "get" && r.Action != "read" && r.Action != "update" {
		return agentlink.Profile{}, agentlink.ErrUnsupported
	}
	body, _ := json.Marshal(struct {
		Name     *string `json:"name,omitempty"`
		Avatar   *string `json:"avatar,omitempty"`
		Revision uint64  `json:"revision"`
	}{r.Name, r.Avatar, r.Revision})
	action := r.Action
	if action == "update" {
		action = "write"
	}
	value, err := b.s.CallAgent(ctx, r.AssistantID, agentlink.AssistantProfile, agentlink.Call{Action: action, Body: body})
	if err != nil {
		return agentlink.Profile{}, err
	}
	// Default-avatar reads have only an avatar; preserve the saved profile identity.
	if r.Action == "read" {
		profile, err := b.Profile(ctx, agentlink.ProfileRequest{AssistantID: r.AssistantID, Action: "get"})
		if err == nil {
			profile.Avatar = value.(map[string]any)["avatar"].(string)
		}
		return profile, err
	}
	raw, err := json.Marshal(value)
	var profile agentlink.Profile
	if err == nil {
		err = json.Unmarshal(raw, &profile)
	}
	return profile, err
}

func (b potLinkBackend) DeleteAssistant(ctx context.Context, r agentlink.DeleteAssistantRequest) error {
	err := b.s.DeleteAssistant(ctx, r.AssistantID, r.Revision)
	var api protocol.APIError
	if errors.As(err, &api) {
		switch api.Code {
		case "assistant_busy":
			return agentlink.ErrAssistantBusy
		case "assistant_changed":
			return agentlink.ErrAssistantChanged
		}
	}
	return err
}
