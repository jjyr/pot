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
	if !control.ValidAgentKey(owner) {
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
	if result, err, handled := a.agentOperation(ctx, method, u, owner, r); handled {
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
	if result, err, handled := a.providerAllowanceCall(ctx, method, path, r); handled {
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
		case "/api/v1/runtime":
			return map[string]any{"api_version": protocol.APIVersion, "runtime_id": a.RuntimeID, "boot_id": a.BootID, "implementation": "go", "version": buildinfo.Version, "control_id": a.RuntimeID, "name": a.Name}, nil
		case "/api/v1/control":
			return map[string]any{"id": a.RuntimeID, "name": a.Name, "runtime_id": a.RuntimeID, "connected": true, "role": s.ControlRole()}, nil
		case "/api/v1/assistants":
			definitions, err := s.Registry.Definitions(ctx)
			if err != nil {
				return nil, err
			}
			items := []map[string]any{}
			for _, d := range definitions {
				item := map[string]any{"id": d.ID, "name": d.Name, "revision": d.Revision, "request_id": d.RequestID, "avatar": d.Avatar, "control_id": a.RuntimeID}
				capabilities, err := s.AgentCapabilities(ctx, d.ID)
				if err != nil {
					return nil, err
				}
				item["capabilities"] = capabilities
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
		case "/api/v1/permissions":
			return s.ApprovalView(ctx, owner), nil
		case "/api/v1/usage/summary", "/api/v1/usage/models", "/api/v1/usage/requests", "/api/v1/usage/tools", "/api/v1/usage/compactions":
			days := 0
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
				if u.Query().Get("start") != "" || u.Query().Get("end") != "" {
					return nil, errors.New("date ranges require usage summary")
				}
				return s.Usage.Page(ctx, strings.TrimPrefix(path, "/api/v1/usage/"), usage.Query{Days: days, Timezone: zone, Scope: u.Query().Get("scope"), Offset: offset}, storage.Now())
			}
			summary, err := s.Usage.Summary(ctx, usage.Query{Days: days, Start: u.Query().Get("start"), End: u.Query().Get("end"), Timezone: zone, Scope: u.Query().Get("scope"), Offset: offset}, storage.Now())
			if err != nil {
				return nil, err
			}
			return summary, nil
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
		case "/api/v1/permissions":
			return s.ManageApproval(ctx, owner, r.Body)
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
