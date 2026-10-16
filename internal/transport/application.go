// Package transport adapts authenticated clients to application services. Product
// decisions remain in their owning domain packages; adapters never execute tools.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/resourcecontrol"
	"github.com/jjyr/pot/internal/storage"
	"github.com/jjyr/pot/internal/usage"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Application struct {
	Control           *control.Service
	Desktop           http.Handler
	Resources         *resourcecontrol.Manager
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
	if method != "GET" && method != "POST" {
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
	if result, err, handled := a.resourceCall(ctx, method, u, r); handled {
		return result, err
	}
	if result, err, handled := a.machineCall(ctx, method, u, r); handled {
		return result, err
	}
	if s.Machines.Role() == "runner" && path != "/api/v1/runtime" && path != "/api/v1/control" {
		return nil, protocol.APIError{Code: "runner_role", Message: "execution Runtime cannot own assistants or provider credentials"}
	}
	if result, err, handled := a.taskCall(ctx, method, u, owner, r); handled {
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
		case "/api/v1/runtime":
			return map[string]any{"api_version": protocol.APIVersion, "runtime_id": a.RuntimeID, "boot_id": a.BootID, "implementation": "go", "control_id": a.RuntimeID, "name": a.Name, "capabilities": map[string]bool{"host": true, "sandbox": s.HasSandbox()}}, nil
		case "/api/v1/control":
			return map[string]any{"id": a.RuntimeID, "name": a.Name, "runtime_id": a.RuntimeID, "connected": true, "role": s.ControlRole()}, nil
		case "/api/v1/assistants":
			definitions, err := s.Registry.Definitions(ctx)
			if err != nil {
				return nil, err
			}
			items := []map[string]any{}
			for _, d := range definitions {
				item := map[string]any{"id": d.ID, "name": d.Name, "revision": d.Revision, "control_id": a.RuntimeID}
				c, err := s.Conversation(ctx, d.ID)
				if err != nil {
					return nil, err
				}
				snapshot, err := c.Snapshot(ctx)
				if err != nil {
					return nil, err
				}
				item["phase"] = snapshot.Agent.Phase
				if len(snapshot.Messages) > 0 {
					item["preview"] = snapshot.Messages[len(snapshot.Messages)-1].Text
				}
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
			return c.Snapshot(ctx)
		case "/api/v1/permissions":
			policy, _, err := s.Permissions.Policy(a.RuntimeID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"policy": map[string]any{"machines": map[string]any{a.RuntimeID: policy}}, "pending": s.Permissions.Pending(owner)}, nil
		case "/api/v1/usage/summary":
			days := 7
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
			return s.Usage.Summary(ctx, usage.Query{Days: days, Timezone: zone, Scope: u.Query().Get("scope"), Offset: offset}, storage.Now())
		case "/api/v1/spaces":
			return map[string]any{"active_space_id": "local", "items": []any{map[string]any{"id": "local", "kind": "local", "name": a.Name, "control_id": a.RuntimeID, "runtime_id": a.RuntimeID, "connected": true}}}, nil
		}
	}
	if method == "POST" {
		switch path {
		case "/api/v1/assistant-notifications":
			var body struct {
				After      map[string]int64 `json:"after"`
				Initialize bool             `json:"initialize"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			return s.Notifications(ctx, body.After, body.Initialize)
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
			err = c.Cancel(ctx, body)
			return map[string]bool{"accepted": err == nil}, err
		case "/api/v1/permissions":
			var body struct {
				ID            string                 `json:"id,omitempty"`
				Action        string                 `json:"action" required:"true"`
				MachineID     string                 `json:"machine_id,omitempty"`
				MachinePolicy *control.MachinePolicy `json:"machine_policy,omitempty"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			if body.Action == "configure" {
				if body.MachinePolicy == nil {
					return nil, errors.New("machine policy required")
				}
				err = s.Permissions.Configure(body.MachineID, *body.MachinePolicy)
			} else {
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
			c, err := conversation()
			if err != nil {
				return nil, err
			}
			if err = s.Usage.ReleaseWait(ctx, owner); err != nil {
				return nil, err
			}
			err = c.Resume(ctx)
			return map[string]bool{"accepted": err == nil}, err
		}
		if strings.HasPrefix(path, "/api/v1/assistants/") {
			id := strings.TrimPrefix(path, "/api/v1/assistants/")
			if !core.Identifier(id) {
				return nil, errors.New("invalid assistant path")
			}
			var body struct {
				Name     string `json:"name" required:"true"`
				Revision uint64 `json:"revision" required:"true"`
			}
			if err = decode(r.Body, &body); err != nil {
				return nil, err
			}
			definition, err := s.Registry.Rename(ctx, id, body.Name, body.Revision)
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
