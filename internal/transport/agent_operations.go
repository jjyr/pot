package transport

import (
	"context"
	"net/url"
	"strings"

	"github.com/jjyr/pot/pkg/agentlink"
)

// HTTP/native routes adapt to the same Agent Link port for every assistant.
// Neither an HTTP path nor the presentation key chooses operator authority.
func (a *Application) agentOperation(ctx context.Context, method string, u *url.URL, owner string, r Request) (any, error, bool) {
	op := agentlink.Operation("")
	path := u.Path
	call := agentlink.Call{Action: "read", Body: r.Body, Query: map[string]string{}}
	for key, values := range u.Query() {
		if key != "assistant_id" && key != "space_id" && len(values) == 1 {
			call.Query[key] = values[0]
		}
	}
	if method == "GET" {
		switch path {
		case "/api/v1/snapshot":
			op = agentlink.ChatRead
		case "/api/v1/agent":
			op = agentlink.AssistantConfigure
		case "/api/v1/instructions":
			op = agentlink.AssistantRules
		case "/api/v1/completions":
			op = agentlink.ChatCompletions
		case "/api/v1/assistants/default-avatar":
			op = agentlink.AssistantProfile
		case "/api/v1/assistants/profile":
			op = agentlink.AssistantProfile
			call.Action = "get"
		case "/api/v1/permissions":
			if u.Query().Get("assistant_id") != "" {
				op = agentlink.Approvals
			}
		case "/api/v1/history":
			op = agentlink.ChatHistory
		case "/api/v1/messages/receipt":
			op = agentlink.ChatReceipt
		case "/api/v1/tasks/query":
			op = agentlink.TaskList
		case "/api/v1/tasks/outcomes":
			op = agentlink.TaskOutcomes
		case "/api/v1/schedules":
			op = agentlink.Schedules
		}
	} else if method == "POST" {
		call.Action = "write"
		switch path {
		case "/api/v1/messages":
			op = agentlink.ChatSend
		case "/api/v1/agent":
			op = agentlink.AssistantConfigure
		case "/api/v1/instructions":
			op = agentlink.AssistantRules
		case "/api/v1/attachments":
			op = agentlink.ChatAttachments
		case "/api/v1/permissions":
			if u.Query().Get("assistant_id") != "" {
				op = agentlink.Approvals
			}
		case "/api/v1/turn/cancel":
			op = agentlink.ChatCancel
		case "/api/v1/turn/steer":
			op = agentlink.ChatSteer
		case "/api/v1/turn/control":
			op = agentlink.ChatControl
		case "/api/v1/turn/retry":
			op = agentlink.ChatRetry
		case "/api/v1/turn/continue":
			op = agentlink.ChatContinue
		case "/api/v1/tasks":
			op = agentlink.TaskSubmit
		case "/api/v1/tasks/dispositions":
			op = agentlink.TaskArchive
		case "/api/v1/schedules":
			op = agentlink.Schedules
		}
	}
	if method == "GET" && strings.HasPrefix(path, "/api/v1/attachments/") {
		op = agentlink.ChatAttachments
		call.ID = strings.TrimPrefix(path, "/api/v1/attachments/")
	}
	if method == "GET" && u.Query().Get("assistant_id") != "" {
		switch path {
		case "/api/v1/usage/summary", "/api/v1/usage/models", "/api/v1/usage/requests", "/api/v1/usage/tools", "/api/v1/usage/compactions":
			op = agentlink.AssistantUsage
			call.ID = strings.TrimPrefix(path, "/api/v1/usage/")
		}
	}
	if method == "POST" && strings.HasPrefix(path, "/api/v1/assistants/") && path != "/api/v1/assistants/bootstrap" {
		id := strings.TrimPrefix(path, "/api/v1/assistants/")
		if strings.HasSuffix(id, "/delete") {
			id = strings.TrimSuffix(id, "/delete")
			call.Action = "delete"
		}
		if u.Query().Get("assistant_id") != "" && owner != id {
			return nil, agentlink.ErrConflict, true
		}
		owner = id
		op = agentlink.AssistantProfile
		if call.Action == "delete" {
			op = agentlink.AssistantDelete
		}
	}
	if op == "" && strings.HasPrefix(path, "/api/v1/tasks/") && path != "/api/v1/tasks/catalog" {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/tasks/"), "/")
		if len(parts) == 1 && method == "GET" {
			op = agentlink.TaskRead
			call.ID = parts[0]
		}
		if len(parts) == 2 {
			call.ID = parts[0]
			if method == "GET" {
				op = map[string]agentlink.Operation{"events": agentlink.TaskEvents, "messages": agentlink.TaskInput, "file": agentlink.TaskFiles}[parts[1]]
			} else if method == "POST" {
				op = map[string]agentlink.Operation{"messages": agentlink.TaskInput, "cancel": agentlink.TaskCancel, "interrupt": agentlink.TaskInterrupt, "control": agentlink.TaskControl, "resume": agentlink.TaskResume, "recovery": agentlink.TaskRecovery}[parts[1]]
			}
		}
	}
	if op == "" {
		return nil, nil, false
	}
	value, err := a.Control.CallAgent(ctx, owner, op, call)
	return value, err, true
}
