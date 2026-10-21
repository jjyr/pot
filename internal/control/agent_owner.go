package control

import (
	"context"
	"errors"
	"strconv"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/attachments"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Owner capabilities remain implementation-side. Peer directories and the
// message/task transport cannot select this adapter or supply its authority.
func (p agentPort) ownerCall(ctx context.Context, owner string, op agentlink.Operation, call agentlink.Call) (any, error) {
	s := p.s
	switch op {
	case agentlink.ChatAttachments:
		if call.Action == "read" {
			if !core.Identifier(call.ID) {
				return nil, agentlink.ErrInvalid
			}
			offset, err := queryUint(call.Query, "offset")
			if err != nil {
				return nil, err
			}
			return s.Attachments.Chunk(owner, call.ID, offset)
		}
		var input attachments.Upload
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return s.UploadAttachment(ctx, owner, input)
	case agentlink.AssistantProfile:
		if call.Action == "read" {
			avatar, err := s.Registry.DefaultAvatar(ctx, owner)
			return map[string]any{"avatar": avatar}, err
		}
		if call.Action == "delete" {
			var input struct {
				Revision uint64 `json:"revision" required:"true"`
			}
			if err := protocol.DecodeStrict(call.Body, &input); err != nil {
				return nil, err
			}
			err := s.DeleteAssistant(ctx, owner, input.Revision)
			return map[string]bool{"deleted": err == nil}, err
		}
		var input struct {
			Name     *string `json:"name,omitempty"`
			Avatar   *string `json:"avatar,omitempty"`
			Revision uint64  `json:"revision" required:"true"`
		}
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		definition, err := s.Registry.UpdateProfile(ctx, owner, assistant.ProfileUpdate{Name: input.Name, Avatar: input.Avatar}, input.Revision)
		if err == nil {
			s.Changes.Notify("assistants")
		}
		return definition, err
	case agentlink.Approvals:
		if call.Action == "read" {
			return s.ApprovalView(ctx, owner), nil
		}
		return s.ManageApproval(ctx, owner, call.Body)
	case agentlink.AssistantUsage:
		days, offset := 365, 0
		var err error
		if call.Query["days"] != "" {
			days, err = strconv.Atoi(call.Query["days"])
			if err != nil {
				return nil, err
			}
		}
		if call.Query["offset"] != "" {
			offset, err = strconv.Atoi(call.Query["offset"])
			if err != nil {
				return nil, err
			}
		}
		zone := call.Query["timezone"]
		if zone == "" {
			zone = "Asia/Shanghai"
		}
		q := usage.Query{Days: days, Timezone: zone, Scope: call.Query["scope"], Offset: offset}
		if call.ID != "summary" {
			switch call.ID {
			case "models", "requests", "tools", "compactions":
				return s.Usage.Page(ctx, call.ID, q, storage.Now())
			default:
				return nil, agentlink.ErrUnsupported
			}
		}
		summary, err := s.Usage.Summary(ctx, q, storage.Now())
		if err != nil {
			return nil, err
		}
		mail, err := s.MailAllowanceWaits(ctx, owner)
		if err != nil {
			return nil, err
		}
		return struct {
			usage.Summary
			MailWaits []usage.Waiting `json:"mail_waits"`
		}{summary, mail}, nil
	}
	return nil, agentlink.ErrUnsupported
}

func (s *Service) ApprovalView(ctx context.Context, owner string) map[string]any {
	type pendingApproval struct {
		Approval
		TaskTitle string `json:"task_title,omitempty"`
	}
	pending := []pendingApproval{}
	for _, approval := range s.Permissions.Pending(owner) {
		item := pendingApproval{Approval: approval}
		if approval.Request.Task != "" {
			if task, err := s.Tasks.Get(ctx, owner, approval.Request.Task); err == nil && task != nil {
				item.TaskTitle = task.Info.Title
			}
		}
		pending = append(pending, item)
	}
	return map[string]any{"policy": s.Permissions.Snapshot(), "pending": pending}
}

func (s *Service) ManageApproval(ctx context.Context, owner string, body []byte) (any, error) {
	var input struct {
		ID               string         `json:"id,omitempty"`
		Action           string         `json:"action" required:"true"`
		MachineID        string         `json:"machine_id,omitempty"`
		MachinePolicy    *MachinePolicy `json:"machine_policy,omitempty"`
		Rule             *CommandRule   `json:"rule,omitempty"`
		OldID            string         `json:"old_id,omitempty"`
		ExpectedRevision uint64         `json:"expected_revision,omitempty"`
	}
	if err := protocol.DecodeStrict(body, &input); err != nil {
		return nil, err
	}
	var err error
	switch input.Action {
	case "save_rule":
		if input.Rule == nil {
			return nil, errors.New("command rule required")
		}
		err = s.SaveCommandRule(ctx, "owner", *input.Rule, input.OldID, input.ExpectedRevision)
	case "delete_rule":
		err = s.Permissions.DeleteRule(input.MachineID, input.ID, input.ExpectedRevision)
	case "remove_task_grant":
		err = s.Permissions.RemoveGrant(input.ID)
	case "retry":
		err = s.Permissions.Retry(input.MachineID, input.ExpectedRevision)
	case "configure":
		if input.MachinePolicy == nil || input.MachinePolicy.Revision == 0 {
			return nil, errors.New("machine policy required")
		}
		err = s.Permissions.Configure(input.MachineID, *input.MachinePolicy)
	default:
		err = s.Permissions.Decide(owner, input.ID, input.Action)
	}
	return map[string]bool{"accepted": err == nil}, err
}
