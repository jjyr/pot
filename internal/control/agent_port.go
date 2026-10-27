package control

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/attachments"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type agentPort struct{ s *Service }

var _ agentlink.Port = agentPort{}

type agentRouteKey struct{}

// ValidAgentKey validates a UI resource key; it grants no caller authority.
func ValidAgentKey(owner string) bool {
	if !strings.HasPrefix(owner, "agent-link:") {
		return core.Identifier(owner)
	}
	parts := strings.SplitN(strings.TrimPrefix(owner, "agent-link:"), ":", 2)
	return len(parts) == 2 && core.Identifier(parts[0]) && core.Identifier(parts[1])
}
func (s *Service) agentTarget(ctx context.Context, owner string) (context.Context, protocol.AgentRef, error) {
	if !ValidAgentKey(owner) {
		return ctx, protocol.AgentRef{}, agentlink.ErrInvalid
	}
	if !strings.HasPrefix(owner, "agent-link:") {
		return ctx, agentlink.AssistantTarget(s.options.RuntimeID, owner), nil
	}
	parts := strings.SplitN(strings.TrimPrefix(owner, "agent-link:"), ":", 2)
	if s.isPotMCP(ctx, parts[0]) {
		c, e := s.PotMCPConnection(ctx, parts[0])
		if e != nil {
			return ctx, protocol.AgentRef{}, e
		}
		target := agentlink.AssistantTarget(c.RemoteNodeID, parts[1])
		if c.Directory == nil || !c.Directory.Capabilities(target).Supports(agentlink.TaskRead) {
			return ctx, protocol.AgentRef{}, agentlink.ErrNotFound
		}
		return context.WithValue(ctx, agentRouteKey{}, c.ID), target, nil
	}
	c, err := s.agentLinks.service.Get(ctx, parts[0])
	if err != nil {
		return ctx, protocol.AgentRef{}, err
	}
	return context.WithValue(ctx, agentRouteKey{}, c.ID), agentlink.AssistantTarget(c.PeerHost.NodeID, parts[1]), nil
}
func (s *Service) agentLinkRoute(ctx context.Context, target protocol.AgentRef) (string, error) {
	if !agentlink.ValidTarget(target) {
		return "", agentlink.ErrInvalid
	}
	if pinned, ok := ctx.Value(agentRouteKey{}).(string); ok {
		if s.isPotMCP(ctx, pinned) {
			c, e := s.PotMCPConnection(ctx, pinned)
			if e != nil {
				return "", e
			}
			if c.Directory == nil {
				return "", agentlink.ErrUnavailable
			}
			for _, endpoint := range c.Directory.Targets {
				if endpoint.Address == target {
					return c.ID, nil
				}
			}
			return "", agentlink.ErrConflict
		}
		c, err := s.agentLinks.service.Get(ctx, pinned)
		if err != nil {
			return "", err
		}
		if c.PeerHost.NodeID != target.NodeID {
			return "", agentlink.ErrConflict
		}
		return c.ID, nil
	}
	if target.NodeID == s.options.RuntimeID {
		return "", nil
	}
	if c, found, e := s.potMCPRoute(ctx, target); e != nil {
		return "", e
	} else if found {
		return c.ID, nil
	}
	connections, err := s.agentLinks.service.List(ctx)
	if err != nil {
		return "", err
	}
	var route string
	for _, c := range connections {
		if c.State != "revoked" && c.PeerHost.NodeID == target.NodeID {
			if route != "" {
				return "", agentlink.ErrConflict
			}
			route = c.ID
		}
	}
	if route == "" {
		return "", agentlink.ErrNotFound
	}
	return route, nil
}

func (s *Service) AgentCapabilities(ctx context.Context, owner string) (agentlink.Description, error) {
	ctx, target, err := s.agentTarget(ctx, owner)
	if err != nil {
		return agentlink.Description{}, err
	}
	return (agentPort{s}).Describe(ctx, target)
}

func (s *Service) SubscribeAgent(ctx context.Context, owner, locale string) (agentlink.Subscription, error) {
	ctx, target, err := s.agentTarget(ctx, owner)
	if err != nil {
		return agentlink.Subscription{}, err
	}
	return (agentPort{s}).Subscribe(ctx, target, locale)
}

func (p agentPort) Subscribe(ctx context.Context, target protocol.AgentRef, locale string) (agentlink.Subscription, error) {
	d, err := p.Describe(ctx, target)
	if err != nil {
		return agentlink.Subscription{}, err
	}
	if !d.Supports(agentlink.ChatSubscribe) {
		return agentlink.Subscription{}, agentlink.ErrUnsupported
	}
	endpoint, err := p.chatEndpoint(ctx, target, locale)
	if err != nil {
		return agentlink.Subscription{}, err
	}
	return endpoint.Subscribe(ctx)
}

func (s *Service) CallAgent(ctx context.Context, owner string, operation agentlink.Operation, call agentlink.Call) (any, error) {
	ctx, target, err := s.agentTarget(ctx, owner)
	if err != nil {
		return nil, err
	}
	link, err := s.agentLinkRoute(ctx, target)
	if err != nil {
		return nil, err
	}
	if operation == agentlink.ChatContinue {
		var input ContinueMainCommand
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
	}
	value, err := (agentPort{s}).Call(ctx, target, operation, call)
	if err != nil {
		if link == "" && !errors.Is(err, agentlink.ErrUnsupported) {
			if errors.Is(err, agentlink.ErrInvalid) {
				return nil, errors.New(err.Error())
			}
			return nil, err
		}
		return nil, agentLinkError(err)
	}
	if info, ok := value.(protocol.TaskInfo); ok {
		return s.taskPresentation(ctx, info), nil
	}
	if operation == agentlink.TaskRead {
		task, ok := value.(*tasks.Record)
		if !ok || task == nil {
			return nil, protocol.APIError{Code: "not_found"}
		}

		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		var record map[string]any
		if err = json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		capabilities := agentlink.Description{Version: 1, Scope: "owner", Operations: agentlink.OwnerOperations()}
		if task.Execution != nil {
			capabilities = s.taskExecutionCapabilities(ctx, task)
		}

		record["capabilities"] = capabilities
		return record, nil
	}
	return value, nil
}

func (p agentPort) Describe(ctx context.Context, target protocol.AgentRef) (agentlink.Description, error) {
	link, err := p.s.agentLinkRoute(ctx, target)
	if err != nil {
		return agentlink.Description{}, err
	}
	if link == "" {
		definitions, err := p.s.Registry.Definitions(ctx)
		if err != nil {
			return agentlink.Description{}, err
		}
		found := false
		for _, definition := range definitions {
			if definition.ID == target.AgentID {
				found = true
				break
			}
		}
		if !found {
			return agentlink.Description{}, errors.New("assistant unavailable")
		}
		return agentlink.Description{Version: 1, Scope: "owner", Operations: agentlink.OwnerOperations()}, nil
	}
	if p.s.isPotMCP(ctx, link) {
		c, e := p.s.PotMCPConnection(ctx, link)
		if e != nil {
			return agentlink.Description{}, e
		}
		if c.Directory == nil {
			return agentlink.Description{}, agentlink.ErrUnavailable
		}
		d := c.Directory.Capabilities(target)
		if d.Supports(agentlink.ChatRead) {
			d.Operations = append(d.Operations, agentlink.ChatSubscribe)
		}
		if d.Supports(agentlink.TaskRead) {
			d.Operations = append(d.Operations, agentlink.TaskList, agentlink.TaskArchive, agentlink.TaskOutcomes)
		}
		return d, nil
	}
	c, err := p.s.agentLinks.service.Get(ctx, link)
	if err != nil {
		return agentlink.Description{}, err
	}
	d := agentlink.Description{}
	if c.State != "revoked" && c.Directory != nil {
		d = c.Directory.Capabilities(target)
	}
	var retained bool
	if err = p.s.integrationDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_link_chat_sessions WHERE link_id=? AND assistant_id=?)", link, target.AgentID).Scan(&retained); err != nil {
		return d, err
	}
	if d.Version != 1 && retained {
		return agentlink.Description{Version: 1, Scope: "caller", Operations: []agentlink.Operation{agentlink.ChatRead, agentlink.ChatReceipt, agentlink.ChatSubscribe, agentlink.TaskRead, agentlink.TaskList, agentlink.TaskArchive, agentlink.TaskOutcomes}}, nil
	}
	if d.Version != 1 {
		return d, agentlink.ErrNotFound
	}
	// Lists, archive and outcome reads project only this user's accepted tasks;
	// they do not grant access to another user's remote task catalogue.
	if d.Supports(agentlink.TaskSubmit) {
		d.Operations = append(append([]agentlink.Operation{}, d.Operations...), agentlink.TaskList, agentlink.TaskArchive, agentlink.TaskOutcomes)
	}
	return d, nil
}

func (p agentPort) Call(ctx context.Context, target protocol.AgentRef, operation agentlink.Operation, call agentlink.Call) (any, error) {
	link, err := p.s.agentLinkRoute(ctx, target)
	if err != nil {
		return nil, err
	}
	if link != "" && operation == agentlink.TaskSubmit {
		var request protocol.CreateTask
		if err := protocol.DecodeStrict(call.Body, &request); err != nil {
			return nil, err
		}
		var accepted bool
		if err := p.s.Tasks.DB().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM task_writes WHERE assistant_id=? AND request_id=?)", linkChatID(link, target.AgentID), request.RequestID).Scan(&accepted); err != nil {
			return nil, err
		}
		if accepted {
			return p.linkedCall(ctx, target, link, operation, call)
		}
	}
	d, err := p.Describe(ctx, target)
	if err != nil {
		return nil, err
	}
	if !d.Supports(operation) {
		return nil, agentlink.ErrUnsupported
	}
	if len(call.Body) == 0 {
		call.Body = json.RawMessage(`{}`)
	}
	if operation == agentlink.ChatRead || operation == agentlink.ChatSend || operation == agentlink.ChatReceipt {
		endpoint, err := p.chatEndpoint(ctx, target, call.Query["locale"])
		if err != nil {
			return nil, err
		}
		switch operation {
		case agentlink.ChatRead:
			return endpoint.Read(ctx)
		case agentlink.ChatReceipt:
			query := protocol.SendReceiptQuery{RequestID: call.Query["request_id"], ConversationID: call.Query["conversation_id"], UserMessageID: call.Query["user_message_id"], RetryUserMessageID: call.Query["retry_user_message_id"]}
			if !core.Identifier(query.RequestID) || !core.Identifier(query.ConversationID) || query.UserMessageID != "" && !core.Identifier(query.UserMessageID) || query.RetryUserMessageID != "" && (!core.Identifier(query.RetryUserMessageID) || query.UserMessageID != "") {
				return nil, agentlink.ErrInvalid
			}
			if query.UserMessageID != "" && !d.Supports(agentlink.ChatSteer) || query.RetryUserMessageID != "" && !d.Supports(agentlink.ChatRetry) {
				return nil, agentlink.ErrUnsupported
			}
			return endpoint.Receipt(ctx, query)
		case agentlink.ChatSend:
			var input protocol.SendMessage
			if err := protocol.DecodeStrict(call.Body, &input); err != nil {
				return nil, err
			}
			if err := input.Validate(); err != nil {
				return nil, err
			}
			return endpoint.Send(ctx, input)
		}
	}
	if link != "" {
		return p.linkedCall(ctx, target, link, operation, call)
	}
	s := p.s
	assistantID := target.AgentID
	switch operation {
	case agentlink.ChatAttachments:
		if call.Action == "read" {
			if !core.Identifier(call.ID) {
				return nil, agentlink.ErrInvalid
			}
			offset, err := queryUint(call.Query, "offset")
			if err != nil {
				return nil, err
			}
			return s.Attachments.Chunk(assistantID, call.ID, offset)
		}
		var input attachments.Upload
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return s.UploadAttachment(ctx, assistantID, input)
	case agentlink.AssistantDelete:
		var input struct {
			Revision uint64 `json:"revision" required:"true"`
		}
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		err := s.DeleteAssistant(ctx, assistantID, input.Revision)
		return map[string]bool{"deleted": err == nil}, err
	case agentlink.AssistantProfile:
		if call.Action == "get" {
			definitions, err := s.Registry.Definitions(ctx)
			if err != nil {
				return nil, err
			}
			for _, d := range definitions {
				if d.ID == assistantID {
					return agentlink.Profile{ID: d.ID, Name: d.Name, Avatar: d.Avatar, Revision: d.Revision}, nil
				}
			}
			return nil, agentlink.ErrNotFound
		}
		if call.Action == "read" {
			avatar, err := s.Registry.DefaultAvatar(ctx, assistantID)
			return map[string]any{"avatar": avatar}, err
		}

		var input struct {
			Name     *string `json:"name,omitempty"`
			Avatar   *string `json:"avatar,omitempty"`
			Revision uint64  `json:"revision" required:"true"`
		}
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		definition, err := s.Registry.UpdateProfile(ctx, assistantID, assistant.ProfileUpdate{Name: input.Name, Avatar: input.Avatar}, input.Revision)
		if err == nil {
			s.Changes.Notify("assistants")
		}
		return definition, err
	case agentlink.Approvals:
		if call.Action == "read" {
			return s.ApprovalView(ctx, assistantID), nil
		}
		return s.ManageApproval(ctx, assistantID, call.Body)
	case agentlink.AssistantUsage:
		days, offset := 0, 0
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
		q := usage.Query{Days: days, Start: call.Query["start"], End: call.Query["end"], Timezone: zone, Scope: call.Query["scope"], Offset: offset}
		if call.ID != "summary" {
			if q.Start != "" || q.End != "" {
				return nil, errors.New("date ranges require usage summary")
			}
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
		return summary, nil
	}
	return p.localCall(ctx, assistantID, operation, call)
}

func (p agentPort) linkedCall(ctx context.Context, target protocol.AgentRef, link string, op agentlink.Operation, call agentlink.Call) (any, error) {
	s := p.s
	if op == agentlink.Approvals {
		owner := "agent-link:" + link + ":" + target.AgentID
		if call.Action == "read" {
			return s.reviewView(ctx, owner, nil, []any{}), nil
		}
		var input struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		if !isLinkedApproval(input.ID) {
			return nil, agentlink.ErrInvalid
		}
		return s.ManageApproval(ctx, owner, call.Body)
	}
	if op == agentlink.AssistantDelete {
		var input struct {
			Revision uint64 `json:"revision" required:"true"`
		}
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return s.agentLinks.service.DeleteAssistant(ctx, link, agentlink.DeleteAssistantRequest{From: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: linkChatID(link, target.AgentID)}, AssistantID: target.AgentID, Revision: input.Revision})
	}
	if op == agentlink.AssistantProfile {
		var input struct {
			Name     *string `json:"name,omitempty"`
			Avatar   *string `json:"avatar,omitempty"`
			Revision uint64  `json:"revision"`
		}
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		action := call.Action
		if action == "write" {
			action = "update"
		}
		return s.agentLinks.service.Profile(ctx, link, agentlink.ProfileRequest{From: protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: linkChatID(link, target.AgentID)}, AssistantID: target.AgentID, Action: action, Name: input.Name, Avatar: input.Avatar, Revision: input.Revision})
	}
	owner := linkChatID(link, target.AgentID)
	if op == agentlink.TaskSubmit {
		var input protocol.CreateTask
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		if input.Target == nil {
			input.Target = &target
		}
		// Persist a user route even when the conversation has not sent text yet.
		if _, err := s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat_sessions(link_id,assistant_id,id) VALUES(?,?,?) ON CONFLICT DO NOTHING", link, target.AgentID, owner); err != nil {
			return nil, err
		}
		return s.createTaskFrom(ctx, owner, protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: owner}, input)
	}
	return p.taskCall(ctx, owner, op, call)
}

func (p agentPort) localCall(ctx context.Context, assistantID string, op agentlink.Operation, call agentlink.Call) (any, error) {
	s := p.s
	if strings.HasPrefix(string(op), "task.") {
		return p.taskCall(ctx, assistantID, op, call)
	}
	if op == agentlink.Schedules {
		if call.Action == "read" {
			q, err := scheduleQuery(call.Query)
			if err != nil {
				return nil, err
			}
			return s.Schedules.Page(ctx, assistantID, q)
		}
		var input schedule.Request
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		value, err := s.Schedules.Manage(ctx, assistantID, input, storage.Now())
		if err == nil {
			s.Changes.Notify("schedules")
			s.wakeTasks()
		}
		return value, err
	}
	c, err := s.Conversation(ctx, assistantID)
	if err != nil {
		return nil, err
	}
	switch op {
	case agentlink.AssistantConfigure:
		if call.Action == "read" {
			return s.AssistantConfiguration(ctx, assistantID)
		}
		var input SaveAssistantConfiguration
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return s.SaveAssistantConfiguration(ctx, assistantID, input)
	case agentlink.AssistantRules:
		if call.Action == "read" {
			return c.Instructions(ctx)
		}
		var input struct {
			Text     string `json:"text" required:"true"`
			Revision string `json:"revision" required:"true"`
		}
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return c.SaveInstructions(ctx, input.Text, input.Revision)
	case agentlink.ChatCompletions:
		return s.Completions(ctx, assistantID, call.Query["q"])
	case agentlink.ChatHistory:
		before, e := queryUint(call.Query, "before")
		if e != nil {
			return nil, e
		}
		after, e := queryUint(call.Query, "after")
		if e != nil {
			return nil, e
		}
		limit, e := queryUint(call.Query, "limit")
		if e != nil || limit > storage.UIMaxPageSize {
			return nil, agentlink.ErrInvalid
		}
		around := call.Query["around"]
		if before > 0 && after > 0 || around != "" && (before > 0 || after > 0 || !core.Identifier(around)) {
			return nil, agentlink.ErrInvalid
		}
		page, e := c.UIHistory(ctx, int64(before), int64(after), around, int(limit))
		if storage.IsMissingUIMessage(e) {
			return nil, protocol.APIError{Code: "not_found"}
		}
		return page, e
	case agentlink.ChatCancel:
		var input protocol.CancelTurn
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		waiting, e := s.Usage.Waiting(ctx, assistantID)
		if e != nil {
			return nil, e
		}
		err = c.Cancel(ctx, input)
		if err == nil && waiting != nil {
			err = s.Usage.ClearWaitForTurn(ctx, assistantID, waiting.Turn)
		}
	case agentlink.ChatSteer:
		var input assistant.SteerRequest
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return c.Steer(ctx, input)
	case agentlink.ChatControl:
		var input assistant.TurnControl
		if err = protocol.DecodeStrict(call.Body, &input); err == nil {
			err = c.Control(ctx, input)
		}
	case agentlink.ChatRetry:
		var input struct {
			RequestID     string `json:"request_id"`
			UserMessageID string `json:"user_message_id"`
		}
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return c.RetryFailed(ctx, input.RequestID, input.UserMessageID)
	case agentlink.ChatContinue:
		var input ContinueMainCommand
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return s.ContinueMain(ctx, assistantID, input)
	default:
		return nil, agentlink.ErrUnsupported
	}
	return map[string]bool{"accepted": err == nil}, err
}

func queryUint(query map[string]string, key string) (uint64, error) {
	if query[key] == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(query[key], 10, 64)
	if err != nil || v > core.MaxSafeInteger {
		return 0, agentlink.ErrInvalid
	}
	return v, nil
}

func scheduleQuery(query map[string]string) (schedule.Query, error) {
	q := schedule.Query{Scope: query["scope"], Selected: query["selected"]}
	var err error
	if q.Offset, err = queryUint(query, "offset"); err != nil {
		return q, err
	}
	if q.RunsOffset, err = queryUint(query, "runs_offset"); err != nil {
		return q, err
	}
	q.Limit, err = queryUint(query, "limit")
	return q, err
}

func (p agentPort) taskCall(ctx context.Context, owner string, op agentlink.Operation, call agentlink.Call) (any, error) {
	s := p.s
	switch op {
	case agentlink.TaskSubmit:
		var input protocol.CreateTask
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		return s.CreateTask(ctx, owner, input)
	case agentlink.TaskRead:
		return s.GetTask(ctx, owner, call.ID)
	case agentlink.TaskList:
		q := protocol.TaskQuery{Scope: call.Query["scope"], Q: call.Query["q"], ExecutionTarget: call.Query["execution_target"], Selected: call.Query["selected"]}
		var err error
		q.Offset, err = queryUint(call.Query, "offset")
		if err != nil {
			return nil, err
		}
		q.Limit, err = queryUint(call.Query, "limit")
		if err != nil {
			return nil, err
		}
		if phase := call.Query["phase"]; phase != "" {
			value := protocol.TaskPhase(phase)
			q.Phase = &value
		}
		return s.TaskPage(ctx, owner, q)
	case agentlink.TaskOutcomes:
		var after *uint64
		if _, ok := call.Query["after"]; ok {
			if call.Query["after"] == "" {
				return nil, agentlink.ErrInvalid
			}
			value, err := queryUint(call.Query, "after")
			if err != nil {
				return nil, err
			}
			after = &value
		}
		return s.Tasks.Outcomes(ctx, owner, after)
	case agentlink.TaskArchive:
		var input protocol.TaskDispositions
		if err := protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		items, err := s.Tasks.SetDispositions(ctx, owner, input.Decisions)
		return map[string]any{"items": items}, err
	}
	if !core.Identifier(call.ID) {
		return nil, agentlink.ErrInvalid
	}
	record, err := s.Tasks.Get(ctx, owner, call.ID)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, agentlink.ErrNotFound
	}
	if record.Execution != nil {
		if op == agentlink.TaskEvents {
			return map[string]any{"events": []any{}, "cursor": 0, "has_more": false, "unavailable": true}, nil
		}
		if op == agentlink.TaskInput && call.Action == "write" {
			var input protocol.TaskInput
			if err := protocol.DecodeStrict(call.Body, &input); err != nil {
				return nil, err
			}
			return s.guideTaskExecution(ctx, owner, call.ID, input)
		}
		if op == agentlink.TaskInput && call.Action == "read" {
			return []any{}, nil
		}
		if op != agentlink.TaskCancel {
			return nil, agentlink.ErrUnsupported
		}
	}
	switch op {
	case agentlink.TaskCancel, agentlink.TaskInterrupt:
		var input struct {
			RequestID string `json:"request_id" required:"true"`
		}
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		mode := "cancel"
		if op == agentlink.TaskInterrupt {
			mode = "interrupt"
		}
		err = s.StopTask(ctx, owner, call.ID, mode)
	case agentlink.TaskEvents:
		after, e := queryUint(call.Query, "after")
		if e != nil {
			return nil, e
		}
		limit, e := queryUint(call.Query, "limit")
		if e != nil {
			return nil, e
		}
		return s.TaskEvents(ctx, owner, call.ID, after, limit)
	case agentlink.TaskInput:
		if call.Action == "read" {
			return s.Tasks.Inputs(ctx, owner, call.ID)
		}
		var input protocol.TaskInput
		if err = protocol.DecodeStrict(call.Body, &input); err == nil {
			sender := protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "user", AgentID: owner}
			if from, ok := ctx.Value(taskInputSenderKey{}).(protocol.AgentRef); ok {
				if from.NodeID != s.options.RuntimeID {
					return nil, agentlink.ErrUnauthorized
				}
				switch from.Kind {
				case "assistant":
					if from.AgentID != owner {
						return nil, agentlink.ErrUnauthorized
					}
				case "task":
					// A Task input is peer reference, never the owner's guidance.
					// Keep the existing assistant-scoped catalogue isolation.
					if record.NodeID != s.options.RuntimeID || (record.Info.Address != nil && *record.Info.Address != (protocol.AgentRef{NodeID: s.options.RuntimeID, Kind: "task", AgentID: call.ID})) {
						return nil, agentlink.ErrUnauthorized
					}
					source, e := s.Tasks.Get(ctx, owner, from.AgentID)
					if e != nil {
						return nil, e
					}
					run, bound := ctx.Value(taskMessageRunKey{}).(taskMessageRun)
					if !bound || source == nil || source.Execution != nil || source.NodeID != s.options.RuntimeID || source.RunID != run.ID || source.Generation != run.Generation || source.Info.Phase.Scope() != "active" || source.StopMode != "" {
						return nil, agentlink.ErrUnauthorized
					}
				default:
					return nil, agentlink.ErrUnauthorized
				}
				sender = from
			}
			var parent *protocol.Envelope
			if value, ok := ctx.Value(mailParentKey{}).(protocol.Envelope); ok {
				if value.To != sender {
					return nil, agentlink.ErrUnauthorized
				}
				parent = &value
			}
			err = s.Tasks.SendMessageWithParent(ctx, owner, call.ID, input.RequestID, sender, input.Text, parent)
			if err == nil {
				state, err := s.Tasks.InputState(ctx, owner, call.ID, input.RequestID)
				if err != nil {
					return nil, err
				}
				messageID, _ := core.HashJSON([]string{call.ID, input.RequestID})
				return map[string]any{"accepted": true, "message_id": messageID, "input_state": state}, nil
			}
		}
	case agentlink.TaskRetry:
		var input struct {
			Instructions string `json:"instructions" required:"true"`
		}
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		record, err := s.Tasks.Retry(ctx, owner, call.ID, input.Instructions)
		if err == nil {
			s.wakeTasks()
		}
		return record.Info, err
	case agentlink.TaskControl, agentlink.TaskRecovery:
		var input struct {
			RunID  string `json:"run_id" required:"true"`
			Action string `json:"action" required:"true"`
		}
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		if op == agentlink.TaskRecovery {
			return s.TaskRecovery(ctx, owner, call.ID, input.RunID, input.Action)
		}
		err = s.Tasks.Control(ctx, owner, call.ID, input.RunID, input.Action)
	case agentlink.TaskResume:
		var input ResumeAllowanceCommand
		if err = protocol.DecodeStrict(call.Body, &input); err != nil {
			return nil, err
		}
		if input.Scope != "" && input.Scope != call.ID {
			return nil, errors.New("Task allowance scope changed")
		}
		input.Scope = call.ID
		err = s.ResumeAllowance(ctx, owner, input)
	case agentlink.TaskFiles:
		offset, e := queryUint(call.Query, "offset")
		if e != nil {
			return nil, e
		}
		return s.ReadTaskFileChunk(ctx, owner, call.ID, call.Query["path"], offset, call.Query["revision"])
	default:
		return nil, agentlink.ErrUnsupported
	}
	return map[string]bool{"accepted": err == nil}, err
}
