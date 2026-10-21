// Package transport adapts authenticated clients to application services. Product
// decisions remain in their owning domain packages; adapters never execute tools.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"net/url"
	"strconv"
	"strings"
)

type Application struct {
	RuntimeResources func(context.Context, string, json.RawMessage) (any, error)
	About            func(context.Context) (any, error)
	Updates          func(context.Context, string, json.RawMessage) (any, error)
	Maintenance      func() bool
	Control          *control.Service

	RuntimeID, BootID string
	Name              string
}
type Request struct {
	Method, Path string
	Body         json.RawMessage
}

func decode(data json.RawMessage, destination any) error {
	if len(data) == 0 {
		data = json.RawMessage(`{}`)
	}
	return protocol.DecodeStrict(data, destination)
}
func (a *Application) Call(ctx context.Context, r Request) (any, error) {
	if a.Control == nil {
		return nil, errors.New("Control unavailable")
	}
	u, err := url.ParseRequestURI(r.Path)
	if err != nil || u.IsAbs() || u.Host != "" {
		return nil, protocol.APIError{Code: "invalid_path", Message: "invalid application path"}
	}
	method := strings.ToUpper(r.Method)
	if method != "GET" && method != "POST" && !(method == "DELETE" && strings.HasPrefix(u.Path, "/api/v1/integrations/")) {
		return nil, protocol.APIError{Code: "method_not_allowed", Message: "method not allowed"}
	}
	owner := u.Query().Get("assistant_id")
	if owner == "" {
		owner = "main"
	}
	if !core.Identifier(owner) {
		return nil, errors.New("invalid assistant identity")
	}
	path := u.Path
	s := a.Control
	if path == "/api/v1/updates" {
		if a.Updates == nil {
			return map[string]bool{"supported": false}, nil
		}
		return a.Updates(ctx, method, r.Body)
	}
	if a.Maintenance != nil && a.Maintenance() && path != "/api/v1/runtime" && path != "/api/v1/control" {
		return nil, protocol.APIError{Code: "update_maintenance"}
	}
	if path == "/api/v1/about" && method == "GET" {
		if a.About == nil {
			return nil, errors.New("product information unavailable")
		}
		return a.About(ctx)
	}
	if path == "/api/v1/runtime/resources" {
		if a.RuntimeResources == nil {
			return nil, errors.New("Runtime resource port unavailable")
		}
		return a.RuntimeResources(ctx, method, r.Body)
	}
	if path == "/api/v1/runtime/recovery" {
		if method != "POST" {
			return nil, errors.New("POST required")
		}
		var command control.RecoveryCommand
		if err := decode(r.Body, &command); err != nil {
			return nil, err
		}
		return s.RecoverRuntimeRun(ctx, command)
	}
	if result, err, handled := a.machineCall(ctx, method, u, r); handled {
		return result, err
	}
	if result, err, handled := a.agentLinkCall(ctx, method, path, r); handled {
		return result, err
	}
	if result, err, handled := a.extensionCall(ctx, method, path, owner, r); handled {
		return result, err
	}
	if result, err, handled := a.pluginNotificationsCall(ctx, method, path, r); handled {
		return result, err
	}
	if result, err, handled := a.integrationCall(ctx, method, path, r); handled {
		return result, err
	}
	if result, err, handled := a.attachmentCall(ctx, method, u, owner, r); handled {
		return result, err
	}
	if result, err, handled := a.resourceCall(ctx, method, u, owner); handled {
		return result, err
	}
	if result, err, handled := a.taskCall(ctx, method, u, owner, r); handled {
		return result, err
	}
	if result, err, handled := a.terminalCall(ctx, method, u, owner, r); handled {
		return result, err
	}
	if path == "/api/v1/turn/recovery" {
		if method != "POST" {
			return nil, errors.New("POST required")
		}
		var command control.MainRecoveryCommand
		if err := decode(r.Body, &command); err != nil {
			return nil, err
		}
		return s.RecoverMain(ctx, owner, command)
	}
	if result, err, handled := a.turnControlCall(ctx, method, path, owner, r); handled {
		return result, err
	}
	conversation := func() (*assistant.Conversation, error) { return s.Conversation(ctx, owner) }
	if path == "/api/v1/agent/routing" {
		if method == "GET" {
			return s.Routing(ctx)
		}
		var policy control.RoutingPolicy
		if err := decode(r.Body, &policy); err != nil {
			return nil, err
		}
		return s.SaveRouting(ctx, policy)
	}
	if method == "GET" {
		switch path {
		case "/api/v1/usage/compaction-record":
			return s.CompactionRecord(ctx, owner, u.Query().Get("scope"), u.Query().Get("id"))
		case "/api/v1/messages/receipt":
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			return c.SendReceipt(ctx, assistant.SendReceiptQuery{RequestID: u.Query().Get("request_id"), ConversationID: u.Query().Get("conversation_id"), UserMessageID: u.Query().Get("user_message_id"), RetryUserMessageID: u.Query().Get("retry_user_message_id")})
		case "/api/v1/agent/diagnostics":
			if task := u.Query().Get("task_id"); task != "" {
				after, err := strconv.ParseUint(u.Query().Get("after"), 10, 64)
				if u.Query().Get("after") == "" {
					after = 0
					err = nil
				}
				if err != nil {
					return nil, err
				}
				return s.TaskDiagnostics(ctx, owner, task, after)
			}
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			return c.Diagnostics(ctx)
		case "/api/v1/completions":
			return s.Completions(ctx, owner, u.Query().Get("q"))
		case "/api/v1/runtime":
			return map[string]any{"api_version": protocol.APIVersion, "runtime_id": a.RuntimeID, "boot_id": a.BootID, "implementation": "go", "version": buildinfo.Version, "control_id": a.RuntimeID, "name": a.Name}, nil
		case "/api/v1/control":
			return map[string]any{"id": a.RuntimeID, "name": a.Name, "runtime_id": a.RuntimeID, "connected": true, "role": s.ControlRole()}, nil
		case "/api/v1/assistants/default-avatar":
			avatar, err := s.Registry.DefaultAvatar(ctx, owner)
			return map[string]any{"avatar": avatar}, err
		case "/api/v1/assistants":
			definitions, err := s.Registry.Definitions(ctx)
			if err != nil {
				return nil, err
			}
			items := []map[string]any{}
			for _, d := range definitions {
				item := map[string]any{"id": d.ID, "name": d.Name, "revision": d.Revision, "request_id": d.RequestID, "avatar": d.Avatar, "control_id": a.RuntimeID}
				c, err := s.Conversation(ctx, d.ID)
				if err != nil {
					return nil, err
				}
				state, err := c.Catalogue(ctx)
				if err != nil {
					return nil, err
				}
				item["reply_id"], item["reply_count"], item["preview"] = state.ReplyID, state.ReplyCount, state.Preview
				item["responding"], item["running"], item["failed"], item["needs_configuration"] = state.Responding, state.Running, state.Failed, state.NeedsConfiguration
				item["phase"] = "ready"
				if state.NeedsConfiguration {
					item["phase"] = "needs_configuration"
				}
				if state.Failed {
					item["phase"] = "failed"
				}
				page, err := s.Tasks.Page(ctx, d.ID, protocol.TaskQuery{Scope: "active", Limit: 1})
				if err != nil {
					return nil, err
				}
				item["working"] = page.Counts.Active
				executing, err := s.Tasks.Executing(ctx, d.ID)
				if err != nil {
					return nil, err
				}
				item["executing"] = state.Executing || executing
				item["pending_approvals"] = len(s.Permissions.Pending(d.ID))
				items = append(items, item)
			}
			return items, nil
		case "/api/v1/providers":
			return s.Profiles.Snapshot(), nil
		case "/api/v1/agent":
			return s.AssistantConfiguration(ctx, owner)
		case "/api/v1/instructions":
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			return c.Instructions(ctx)
		case "/api/v1/snapshot":
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			return c.UIFrame(ctx, 0, true)
		case "/api/v1/history":
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			q := u.Query()
			parse := func(key string) (int64, error) {
				if q.Get(key) == "" {
					return 0, nil
				}
				n, e := strconv.ParseInt(q.Get(key), 10, 64)
				if e != nil || n <= 0 || uint64(n) > core.MaxSafeInteger {
					return 0, errors.New("invalid history cursor")
				}
				return n, nil
			}
			before, err := parse("before")
			if err != nil {
				return nil, err
			}
			after, err := parse("after")
			if err != nil {
				return nil, err
			}
			limit := 0
			if q.Get("limit") != "" {
				limit, err = strconv.Atoi(q.Get("limit"))
				if err != nil || limit < 1 || limit > storage.UIMaxPageSize {
					return nil, errors.New("invalid history page size")
				}
			}
			around := q.Get("around")
			if before > 0 && after > 0 || around != "" && (before > 0 || after > 0 || !core.Identifier(around)) {
				return nil, errors.New("invalid history query")
			}
			page, err := c.UIHistory(ctx, before, after, around, limit)
			if storage.IsMissingUIMessage(err) {
				return nil, protocol.APIError{Code: "not_found", Message: "Message unavailable"}
			}
			return page, err

		case "/api/v1/permissions":
			type pendingApproval struct {
				control.Approval
				TaskTitle string `json:"task_title,omitempty"`
			}
			pending := []pendingApproval{}
			for _, approval := range s.Permissions.Pending(owner) {
				item := pendingApproval{Approval: approval}
				if approval.Request.Task != "" {
					// The name is optional presentation context. A missing Task
					// must never hide the authoritative pending decision.
					if task, err := s.Tasks.Get(ctx, owner, approval.Request.Task); err == nil && task != nil {
						item.TaskTitle = task.Info.Title
					}
				}
				pending = append(pending, item)
			}
			return map[string]any{"policy": s.Permissions.Snapshot(), "pending": pending}, nil
		case "/api/v1/usage/summary", "/api/v1/usage/models", "/api/v1/usage/requests", "/api/v1/usage/tools", "/api/v1/usage/compactions":
			days := 365
			if text := u.Query().Get("days"); text != "" {
				days, err = strconv.Atoi(text)
				if err != nil {
					return nil, err
				}
			}
			offset := 0
			if text := u.Query().Get("offset"); text != "" {
				offset, err = strconv.Atoi(text)
				if err != nil {
					return nil, err
				}
			}
			zone := u.Query().Get("timezone")
			if zone == "" {
				zone = "Asia/Shanghai"
			}
			if path != "/api/v1/usage/summary" {
				return s.Usage.Page(ctx, strings.TrimPrefix(path, "/api/v1/usage/"), usage.Query{Days: days, Timezone: zone, Scope: u.Query().Get("scope"), Offset: offset}, storage.Now())
			}
			summary, err := s.Usage.Summary(ctx, usage.Query{Days: days, Timezone: zone, Scope: u.Query().Get("scope"), Offset: offset}, storage.Now())
			if err != nil {
				return nil, err
			}
			mailWaits, err := s.MailAllowanceWaits(ctx, owner)
			if err != nil {
				return nil, err
			}
			return struct {
				usage.Summary
				MailWaits []usage.Waiting `json:"mail_waits"`
			}{summary, mailWaits}, nil
		case "/api/v1/providers/allowances":
			return s.ConnectionAllowances(ctx, u.Query().Get("profile_id"))
		case "/api/v1/spaces":
			return map[string]any{"active_space_id": "local", "items": []any{map[string]any{"id": "local", "kind": "local", "name": a.Name, "control_id": a.RuntimeID, "runtime_id": a.RuntimeID, "connected": true}}}, nil
		}
	}
	if method == "POST" {
		switch path {
		case "/api/v1/providers/allowances":
			var body struct {
				ProfileID string  `json:"profile_id"`
				Action    string  `json:"action"`
				Enabled   bool    `json:"enabled"`
				Threshold float64 `json:"threshold"`
				Assistant string  `json:"assistant"`
				Revision  int64   `json:"revision"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			switch body.Action {
			case "refresh":
				return s.RefreshConnectionAllowances(ctx, body.ProfileID)
			case "configure":
				if err = s.ConfigureConnectionAllowance(ctx, body.ProfileID, control.ProviderAllowanceMonitorRequest{Enabled: body.Enabled, Threshold: body.Threshold, Assistant: body.Assistant, Revision: body.Revision}); err != nil {
					return nil, err
				}
				return s.ConnectionAllowances(ctx, body.ProfileID)
			default:
				return nil, errors.New("invalid provider allowance action")
			}
		case "/api/v1/providers/resolve":
			var body control.CheckProvider
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			return s.Profiles.ResolveProviderEndpoint(ctx, body)
		case "/api/v1/providers/test":
			var body control.TestProvider
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			return s.Profiles.Test(ctx, body)
		case "/api/v1/providers/check":
			var body control.CheckProvider
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			return s.Profiles.Check(ctx, body)
		case "/api/v1/providers":
			var body control.SaveProfile
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			result, err := s.Profiles.Save(ctx, body)
			if err == nil {
				s.Changes.Notify("providers")
			}
			return result, err
		case "/api/v1/providers/default":
			var body struct {
				ProviderID string `json:"provider_id" required:"true"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			err = s.Profiles.SetDefault(body.ProviderID)
			if err == nil {
				s.Changes.Notify("providers")
			}
			return map[string]bool{"saved": err == nil}, err
		case "/api/v1/assistants":
			var body control.CreateAssistant
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			return s.CreateAssistant(ctx, body)
		case "/api/v1/assistants/bootstrap":
			var body struct {
				Locale string `json:"locale" required:"true"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			return s.BootstrapAssistant(ctx, body.Locale)
		case "/api/v1/agent":
			var body control.SaveAssistantConfiguration
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			return s.SaveAssistantConfiguration(ctx, owner, body)
		case "/api/v1/instructions":
			var body struct {
				Text     string `json:"text" required:"true"`
				Revision string `json:"revision" required:"true"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			return c.SaveInstructions(ctx, body.Text, body.Revision)
		case "/api/v1/turn/retry":
			var body struct {
				RequestID     string `json:"request_id"`
				UserMessageID string `json:"user_message_id"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			return c.RetryFailed(ctx, body.RequestID, body.UserMessageID)
		case "/api/v1/turn/continue":
			var body control.ContinueMainCommand
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			return s.ContinueMain(ctx, owner, body)
		case "/api/v1/messages":
			var body protocol.SendMessage
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			return c.Send(ctx, body)
		case "/api/v1/turn/cancel":
			var body protocol.CancelTurn
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			waiting, waitErr := s.Usage.Waiting(ctx, owner)
			if waitErr != nil {
				return nil, waitErr
			}
			err = c.Cancel(ctx, body)
			if err == nil && waiting != nil {
				err = s.Usage.ClearWaitForTurn(ctx, owner, waiting.Turn)
			}
			return map[string]bool{"accepted": err == nil}, err
		case "/api/v1/permissions":
			var body struct {
				ID               string                 `json:"id,omitempty"`
				Action           string                 `json:"action" required:"true"`
				MachineID        string                 `json:"machine_id,omitempty"`
				MachinePolicy    *control.MachinePolicy `json:"machine_policy,omitempty"`
				Rule             *control.CommandRule   `json:"rule,omitempty"`
				OldID            string                 `json:"old_id,omitempty"`
				ExpectedRevision uint64                 `json:"expected_revision,omitempty"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			switch body.Action {
			case "save_rule":
				if body.Rule == nil {
					return nil, errors.New("command rule required")
				}
				err = s.SaveCommandRule(ctx, "owner", *body.Rule, body.OldID, body.ExpectedRevision)
			case "delete_rule":
				err = s.Permissions.DeleteRule(body.MachineID, body.ID, body.ExpectedRevision)
			case "remove_task_grant":
				err = s.Permissions.RemoveGrant(body.ID)
			case "retry":
				err = s.Permissions.Retry(body.MachineID, body.ExpectedRevision)
			case "configure":
				if body.MachinePolicy == nil || body.MachinePolicy.Revision == 0 {
					return nil, errors.New("machine policy required")
				}
				err = s.Permissions.Configure(body.MachineID, *body.MachinePolicy)
			default:
				err = s.Permissions.Decide(owner, body.ID, body.Action)
			}
			return map[string]bool{"accepted": err == nil}, err
		case "/api/v1/usage/policy":
			var body usage.Policy
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			err = s.Usage.SetPolicy(ctx, body)
			s.Changes.Notify("usage")
			return map[string]bool{"saved": err == nil}, err
		case "/api/v1/usage/resume":
			var body control.ResumeAllowanceCommand
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			err = s.ResumeAllowance(ctx, owner, body)
			return map[string]bool{"accepted": err == nil}, err
		}
		if strings.HasPrefix(path, "/api/v1/assistants/") {
			id := strings.TrimPrefix(path, "/api/v1/assistants/")
			if strings.HasSuffix(id, "/delete") {
				id = strings.TrimSuffix(id, "/delete")
				if !core.Identifier(id) {
					return nil, errors.New("invalid assistant path")
				}
				var body struct {
					Revision uint64 `json:"revision" required:"true"`
				}
				if err := decode(r.Body, &body); err != nil {
					return nil, err
				}
				err := s.DeleteAssistant(ctx, id, body.Revision)
				return map[string]bool{"deleted": err == nil}, err
			}
			if !core.Identifier(id) {
				return nil, errors.New("invalid assistant path")
			}
			var body struct {
				Name     *string `json:"name,omitempty"`
				Avatar   *string `json:"avatar,omitempty"`
				Revision uint64  `json:"revision" required:"true"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			definition, err := s.Registry.UpdateProfile(ctx, id, assistant.ProfileUpdate{Name: body.Name, Avatar: body.Avatar}, body.Revision)
			if err == nil {
				s.Changes.Notify("assistants")
			}
			return definition, err
		}
	}
	return nil, protocol.APIError{Code: "not_found", Message: "application route unavailable"}
}
func (a *Application) JSON(ctx context.Context, method, path, body string) (string, error) {
	result, err := a.Call(ctx, Request{method, path, json.RawMessage(body)})
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	return string(encoded), err
}
