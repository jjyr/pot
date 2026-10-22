package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/inbox"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type potLinkBackend struct{ s *Service }

func (b potLinkBackend) ChatStatus(ctx context.Context, query agentlink.ChatLookup) (agentlink.ChatStatus, error) {
	_, result, err := inbox.ReadChat(ctx, b.s.Tasks.DB(), query, "in", "out")
	if errors.Is(err, sql.ErrNoRows) {
		err = agentlink.ErrNotFound
	}
	if errors.Is(err, inbox.ErrChatBinding) {
		err = agentlink.ErrUnauthorized
	}
	return result, err
}

func (b potLinkBackend) Directory(ctx context.Context) (agentlink.Directory, error) {
	defs, e := b.s.Registry.Definitions(ctx)
	d := agentlink.Directory{NodeID: b.s.options.RuntimeID, Name: "Pot", Adapter: "pot", Assistants: []agentlink.Identity{}}
	d.Operations = map[string]agentlink.Description{}
	if e != nil {
		return d, e
	}
	for _, a := range defs {
		i, e := b.s.agentLinkIdentity(ctx, a.ID)
		if e != nil {
			return d, e
		}
		i.Avatar = a.Avatar
		d.Assistants = append(d.Assistants, i)
		d.Operations[a.ID] = agentlink.Description{Version: 1, Scope: "peer", Operations: append(agentlink.PeerOperations(), agentlink.AssistantProfile, agentlink.AssistantDelete)}
	}
	var savedDefault string
	e = b.s.integrationDB.QueryRowContext(ctx, "SELECT default_assistant_id FROM agent_link_node_settings WHERE singleton=1").Scan(&savedDefault)
	if errors.Is(e, sql.ErrNoRows) && len(defs) > 0 {
		// A competing first directory read may have already chosen a default. Read
		// the persisted winner instead of returning an uncommitted list position.
		_, e = b.s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_node_settings VALUES(1,?) ON CONFLICT(singleton) DO NOTHING", defs[0].ID)
		if e == nil {
			e = b.s.integrationDB.QueryRowContext(ctx, "SELECT default_assistant_id FROM agent_link_node_settings WHERE singleton=1").Scan(&savedDefault)
		}
	}
	if errors.Is(e, sql.ErrNoRows) && len(defs) == 0 {
		return d, nil
	}
	if e != nil {
		return d, e
	}
	// Deleting a default does not silently designate another assistant. The
	// directory remains useful for explicit targets while omission fails clearly.
	for _, a := range d.Assistants {
		if a.Address.AgentID == savedDefault {
			d.DefaultAssistantID = savedDefault
			break
		}
	}
	return d, nil
}

func callerKey(a protocol.AgentRef) string { raw, _ := json.Marshal(a); return string(raw) }
func (b potLinkBackend) Submit(ctx context.Context, r agentlink.TaskRequest) (agentlink.Task, error) {
	if r.Executor != "" {
		return agentlink.Task{}, agentlink.ErrUnsupported
	}
	if r.Configuration != nil {
		if r.From.NodeID != b.s.options.RuntimeID || r.From.Kind != "assistant" || r.AssistantID != r.From.AgentID || r.Configuration.RequestID != r.RequestID || r.Configuration.Text != r.Text || r.Configuration.Target != nil || r.Configuration.LinkID != "" {
			return agentlink.Task{}, agentlink.ErrUnauthorized
		}
		record, err := b.s.Tasks.Create(ctx, r.AssistantID, *r.Configuration)
		if err != nil {
			return agentlink.Task{}, err
		}
		b.s.wakeTasks()
		identity, err := b.s.agentLinkIdentity(ctx, r.AssistantID)
		if err != nil {
			return agentlink.Task{}, err
		}
		return agentlink.Task{ID: record.Info.ID, RequestID: r.RequestID, Assistant: identity, State: string(record.Info.Phase), CreatedAt: time.UnixMilli(record.Info.CreatedAtMS), UpdatedAt: time.UnixMilli(record.Info.UpdatedAtMS)}, nil
	}
	b.s.linkMu.Lock()
	defer b.s.linkMu.Unlock()
	payload, _ := json.Marshal(r)
	caller := callerKey(r.From)
	id, _ := core.HashJSON([]string{caller, r.RequestID})
	var owner, old string
	e := b.s.integrationDB.QueryRowContext(ctx, "SELECT assistant_id,payload FROM agent_link_received_tasks WHERE id=?", id).Scan(&owner, &old)
	if e == nil && old != string(payload) {
		return agentlink.Task{}, agentlink.ErrConflict
	}
	if errors.Is(e, sql.ErrNoRows) {
		d, err := b.Directory(ctx)
		if err != nil {
			return agentlink.Task{}, err
		}
		owner = r.AssistantID
		if owner == "" {
			owner = d.DefaultAssistantID
		}
		found := false
		for _, a := range d.Assistants {
			if a.Address.AgentID == owner {
				found = true
			}
		}
		if !found {
			return agentlink.Task{}, agentlink.ErrNotFound
		}
		if !d.Operations[owner].Supports(agentlink.TaskSubmit) {
			return agentlink.Task{}, agentlink.ErrUnsupported
		}
		_, e = b.s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_received_tasks(id,caller,request_id,assistant_id,payload)VALUES(?,?,?,?,?)", id, caller, r.RequestID, owner, string(payload))
	}
	if e != nil {
		return agentlink.Task{}, e
	}
	request := protocol.CreateTask{RequestID: id, Text: r.Text}
	if r.Workspace != "" {
		request.Workspace = protocol.TaskWorkspace{Kind: "directory", ProjectDir: r.Workspace}
	}
	record, e := b.s.Tasks.Create(ctx, owner, request)
	if e != nil {
		return agentlink.Task{}, e
	}
	_, e = b.s.integrationDB.ExecContext(ctx, "UPDATE agent_link_received_tasks SET task_id=? WHERE id=?", record.Info.ID, id)
	if e != nil {
		return agentlink.Task{}, e
	}
	b.s.wakeTasks()
	return b.task(ctx, r.From, id)
}
func (b potLinkBackend) task(ctx context.Context, from protocol.AgentRef, id string) (agentlink.Task, error) {
	var owner, task, request string
	e := b.s.integrationDB.QueryRowContext(ctx, "SELECT assistant_id,task_id,request_id FROM agent_link_received_tasks WHERE id=? AND caller=?", id, callerKey(from)).Scan(&owner, &task, &request)
	if errors.Is(e, sql.ErrNoRows) {
		if from.NodeID == b.s.options.RuntimeID && from.Kind == "assistant" {
			record, err := b.s.Tasks.Get(ctx, from.AgentID, id)
			if err != nil {
				return agentlink.Task{}, err
			}
			if record != nil {
				identity, err := b.s.agentLinkIdentity(ctx, from.AgentID)
				if err != nil {
					return agentlink.Task{}, err
				}
				result := agentlink.Task{ID: id, RequestID: record.Request.RequestID, Assistant: identity, State: string(record.Info.Phase), Error: record.Error, CreatedAt: time.UnixMilli(record.Info.CreatedAtMS), UpdatedAt: time.UnixMilli(record.Info.UpdatedAtMS)}
				if record.Outcome != nil {
					result.Result = record.Outcome.Text
				}
				return result, nil
			}
		}
		return agentlink.Task{}, agentlink.ErrNotFound
	}
	if e != nil {
		return agentlink.Task{}, e
	}
	if task == "" {
		return agentlink.Task{}, agentlink.ErrUnavailable
	}
	record, e := b.s.Tasks.Get(ctx, owner, task)
	if e != nil {
		return agentlink.Task{}, e
	}
	if record == nil {
		return agentlink.Task{}, agentlink.ErrNotFound
	}
	assistant, e := b.s.agentLinkIdentity(ctx, owner)
	if e != nil {
		// Historical task lookup never depends on the current public directory.
		// Deleted assistants remain the original accepted task owner.
		var unavailable protocol.APIError
		if !errors.As(e, &unavailable) || unavailable.Code != "agent_link_assistant_unavailable" {
			return agentlink.Task{}, e
		}
		assistant = agentlink.Identity{Address: protocol.AgentRef{NodeID: b.s.options.RuntimeID, Kind: "assistant", AgentID: owner}, Name: owner, Adapter: "pot"}
	}
	t := agentlink.Task{ID: id, RequestID: request, Assistant: assistant, State: string(record.Info.Phase), Error: record.Error, CreatedAt: time.UnixMilli(record.Info.CreatedAtMS), UpdatedAt: time.UnixMilli(record.Info.UpdatedAtMS)}
	if record.Outcome != nil {
		t.Result = record.Outcome.Text
	}
	return t, nil
}
func (b potLinkBackend) Status(ctx context.Context, from protocol.AgentRef, id string) (agentlink.Task, error) {
	return b.task(ctx, from, id)
}
func (b potLinkBackend) Cancel(ctx context.Context, from protocol.AgentRef, id string) (agentlink.Task, error) {
	var owner, task string
	e := b.s.integrationDB.QueryRowContext(ctx, "SELECT assistant_id,task_id FROM agent_link_received_tasks WHERE id=? AND caller=?", id, callerKey(from)).Scan(&owner, &task)
	if e != nil {
		if errors.Is(e, sql.ErrNoRows) && from.NodeID == b.s.options.RuntimeID && from.Kind == "assistant" {
			if _, err := b.task(ctx, from, id); err != nil {
				return agentlink.Task{}, err
			}
			if err := b.s.Tasks.Stop(ctx, from.AgentID, id, "cancel"); err != nil {
				return agentlink.Task{}, err
			}
			return b.task(ctx, from, id)
		}
		return agentlink.Task{}, agentlink.ErrNotFound
	}
	if task == "" {
		return agentlink.Task{}, agentlink.ErrUnavailable
	}
	e = b.s.Tasks.Stop(ctx, owner, task, "cancel")
	if e != nil {
		return agentlink.Task{}, e
	}
	b.s.wakeTasks()
	return b.task(ctx, from, id)
}
func terminalLinkTask(state string) bool {
	return strings.Contains("|complete|completed|failed|cancelled|interrupted|unknown|", "|"+state+"|")
}
