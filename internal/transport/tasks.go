package transport

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/schedule"
	"github.com/jjyr/pot/internal/storage"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

type Download struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

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
		case "file", "file-download":
			if method != "GET" {
				err = errors.New("GET required")
				break
			}
			name := u.Query().Get("path")
			var data []byte
			data, err = s.ReadTaskFile(ctx, owner, id, name, 16*1024*1024)
			if err != nil {
				break
			}
			if parts[1] == "file-download" {
				result = Download{Name: filepath.Base(name), Data: data}
				break
			}
			var offset uint64
			offset, err = integer("offset")
			if err != nil {
				break
			}
			hash := sha256.Sum256(data)
			revision := hex.EncodeToString(hash[:])
			if expected := u.Query().Get("revision"); expected != "" && expected != revision {
				err = errors.New("Task file changed between chunks")
				break
			}
			if offset > uint64(len(data)) {
				err = errors.New("Task file offset exceeds size")
				break
			}
			end := min(offset+256*1024, uint64(len(data)))
			result = protocol.FileChunk{Size: uint64(len(data)), Offset: offset, Revision: revision, Data: base64.StdEncoding.EncodeToString(data[offset:end])}
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
		case "resume":
			if method != "POST" {
				err = errors.New("POST required")
				break
			}
			err = s.Tasks.ResumeWaiting(ctx, owner, id)
			result = map[string]bool{"accepted": err == nil}
		default:
			err = errors.New("Task route unavailable")
		}
	}
	return result, err, true
}
