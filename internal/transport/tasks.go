package transport

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/storage"
	"github.com/jjyr/pot/internal/usage"
	"net/url"
	"strconv"
	"strings"
)

func (a *Application) taskCall(ctx context.Context, method string, u *url.URL, owner string, r Request) (any, error, bool) {
	s := a.Control
	path := u.Path
	if path != "/api/v1/tasks" && !strings.HasPrefix(path, "/api/v1/tasks/") && path != "/api/v1/schedules" && path != protocol.AssistantMailAPI {
		return nil, nil, false
	}
	if s.Machines.Role() == "runner" {
		return nil, errors.New("execution Runner cannot own assistant APIs"), true
	}
	if _, err := s.Conversation(ctx, owner); err != nil {
		return nil, err, true
	}
	integer := func(key string) (uint64, error) {
		value := u.Query().Get(key)
		if value == "" {
			return 0, nil
		}
		return strconv.ParseUint(value, 10, 64)
	}
	var result any
	var err error
	switch {
	case method == "GET" && path == "/api/v1/tasks/query":
		var q protocol.TaskQuery
		q.Scope = u.Query().Get("scope")
		q.Q = u.Query().Get("q")
		q.ExecutionTarget = u.Query().Get("execution_target")
		q.Selected = u.Query().Get("selected")
		q.Offset, err = integer("offset")
		if err == nil {
			q.Limit, err = integer("limit")
		}
		if phase := u.Query().Get("phase"); phase != "" {
			p := protocol.TaskPhase(phase)
			q.Phase = &p
		}
		if err == nil {
			result, err = s.Tasks.Page(ctx, owner, q)
		}
	case method == "POST" && path == "/api/v1/tasks":
		var request protocol.CreateTask
		if err = decode(r.Body, &request); err == nil {
			record, e := s.Tasks.Create(ctx, owner, request)
			result, err = record.Info, e
		}
	case path == "/api/v1/schedules" && method == "GET":
		var q schedule.Query
		q.Scope = u.Query().Get("scope")
		q.Selected = u.Query().Get("selected")
		q.Offset, err = integer("offset")
		if err == nil {
			q.RunsOffset, err = integer("runs_offset")
		}
		if err == nil {
			q.Limit, err = integer("limit")
		}
		if err == nil {
			result, err = s.Schedules.Page(ctx, owner, q)
		}
	case path == "/api/v1/schedules" && method == "POST":
		var request schedule.Request
		if err = decode(r.Body, &request); err == nil {
			result, err = s.Schedules.Manage(ctx, owner, request, storage.Now())
		}
		if err == nil {
			s.Changes.Notify("schedules")
		}
	case path == protocol.AssistantMailAPI && method == "POST":
		var request protocol.RelayRequest
		if err = decode(r.Body, &request); err == nil {
			switch request.Operation {
			case "directory":
				result, err = s.MailDirectory(ctx)
			case "register":
				err = s.Mailbox.Register(ctx, *request.Directory)
				result = map[string]bool{"registered": err == nil}
			case "history":
				result, err = s.Mailbox.History(ctx, request.AssistantID, request.After)
			case "pending":
				result, err = s.Mailbox.Pending(ctx, request.TargetControlID)
			case "deliver":
				result, err = s.Mailbox.Deliver(ctx, *request.Message)
			case "acknowledge":
				err = s.Mailbox.Acknowledge(ctx, *request.Receipt)
				result = map[string]bool{"acknowledged": err == nil}
			default:
				err = errors.New("unknown relay operation")
			}
		}
	default:
		relative := strings.TrimPrefix(path, "/api/v1/tasks/")
		parts := strings.Split(relative, "/")
		if len(parts) > 2 || !core.Identifier(parts[0]) {
			return nil, errors.New("invalid Task path"), true
		}
		id := parts[0]
		if len(parts) == 1 && method == "GET" {
			result, err = s.Tasks.Get(ctx, owner, id)
			break
		}
		if len(parts) != 2 {
			err = errors.New("Task route unavailable")
			break
		}
		switch parts[1] {
		case "file":
			if method != "GET" {
				err = errors.New("GET required")
				break
			}
			name := u.Query().Get("path")
			var offset uint64
			offset, err = integer("offset")
			if err == nil {
				result, err = s.ReadTaskFileChunk(ctx, owner, id, name, offset, u.Query().Get("revision"))
			}

		case "events":
			if method != "GET" {
				err = errors.New("GET required")
				break
			}
			var after, limit uint64
			after, err = integer("after")
			if err == nil {
				limit, err = integer("limit")
			}
			if err == nil {
				result, err = s.TaskEvents(ctx, owner, id, after, limit)
			}
		case "messages":
			if method == "GET" {
				result, err = s.Tasks.Inputs(ctx, owner, id)
			} else {
				var input protocol.TaskInput
				if err = decode(r.Body, &input); err == nil {
					err = s.Tasks.Guide(ctx, owner, id, input.RequestID, input.Text)
				}
				result = map[string]bool{"accepted": err == nil}
			}
		case "interrupt", "cancel":
			if method != "POST" {
				err = errors.New("POST required")
				break
			}
			var request struct {
				ID string `json:"request_id" required:"true"`
			}
			if err = decode(r.Body, &request); err == nil {
				err = s.Tasks.Stop(ctx, owner, id, parts[1])
			}
			result = map[string]bool{"accepted": err == nil}
		case "control":
			if method != "POST" {
				err = errors.New("POST required")
				break
			}
			var request struct {
				RunID  string `json:"run_id" required:"true"`
				Action string `json:"action" required:"true"`
			}
			if err = decode(r.Body, &request); err == nil {
				err = s.Tasks.Control(ctx, owner, id, request.RunID, request.Action)
			}
			result = map[string]bool{"accepted": err == nil}
		case "recovery":
			if method != "POST" {
				err = errors.New("POST required")
				break
			}
			var request struct {
				RunID  string `json:"run_id" required:"true"`
				Action string `json:"action" required:"true"`
			}
			if err = decode(r.Body, &request); err == nil {
				result, err = s.TaskRecovery(ctx, owner, id, request.RunID, request.Action)
			}
		case "resume":
			if method != "POST" {
				err = errors.New("POST required")
				break
			}
			record, e := s.Tasks.Get(ctx, owner, id)
			err = e
			if err == nil && (record == nil || record.Launch == nil || record.Info.Phase != protocol.TaskWaiting || record.Info.RemoteWaiting) {
				err = errors.New("Task is not safely allowance-waiting")
			}
			if err == nil {
				var waiting *usage.Waiting
				waiting, err = s.Usage.Waiting(ctx, id)
				if err == nil && (waiting == nil || waiting.Turn != record.RunID) {
					err = errors.New("Task allowance wait does not match original run")
				}
			}
			if err == nil {
				err = s.Usage.ReleaseWait(ctx, id)
			}
			if err == nil {
				err = s.Tasks.ResumeWaiting(ctx, owner, id)
			}
			result = map[string]bool{"accepted": err == nil}
		default:
			err = errors.New("Task route unavailable")
		}
	}
	return result, err, true
}
