package transport

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"net/url"
	"strconv"
)

// Assistant-scoped task and schedule calls use the Agent Link operation port.
// These node services only aggregate tasks, preview calendars or inspect mail.
func (a *Application) taskCall(ctx context.Context, method string, u *url.URL, owner string, r Request) (any, error, bool) {
	s := a.Control
	path := u.Path
	catalog := method == "GET" && path == "/api/v1/tasks/catalog"
	if !catalog && path != "/api/v1/schedules/preview" && path != protocol.AssistantMailAPI {
		return nil, nil, false
	}
	if path == protocol.AssistantMailAPI {
		if _, err := s.Conversation(ctx, owner); err != nil {
			return nil, err, true
		}
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
	case catalog:
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
			result, err = s.TaskPage(ctx, "", q)
		}
	case path == "/api/v1/schedules/preview" && method == "POST":
		var request schedule.PreviewRequest
		if err = decode(r.Body, &request); err == nil {
			result = schedule.Preview(request, storage.Now())
		}
	case method == "POST" && path == protocol.AssistantMailAPI:
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
				result, err = s.Mailbox.Pending(ctx, request.TargetNodeID)
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
		return nil, nil, false
	}
	return result, err, true
}
