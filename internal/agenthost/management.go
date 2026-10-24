package agenthost

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func (b *backend) validateModel(ctx context.Context, model, effort string) error {
	if effort != "" && effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" && effort != "max" {
		return agentlink.ErrInvalid
	}
	models, err := b.models(ctx)
	if err != nil {
		return err
	}
	for _, m := range models {
		if m.ID == model {
			return nil
		}
	}
	return errors.New("antigravity_model_unavailable")
}
func managed(a assistant) agentlink.ManagedAssistant {
	return agentlink.ManagedAssistant{ID: a.ID, Name: a.Name, Model: a.Model, Effort: a.Effort, Workspace: a.ID, Revision: a.Revision}
}

// Pairing authorizes this narrow user-only surface. Workspaces come from local
// owner-created assistant scopes, never arbitrary paths supplied by a peer.
func (b *backend) ManageAssistant(ctx context.Context, r agentlink.AssistantManagementRequest) (out agentlink.AssistantManagementResult, err error) {
	if b.runtime().Kind != "antigravity" {
		return out, agentlink.ErrUnsupported
	}
	if !r.From.Valid() || r.From.Kind != "user" {
		return out, agentlink.ErrUnauthorized
	}
	if r.Action == "catalog" {
		out.Models, err = b.models(ctx)
		if err != nil {
			return out, err
		}
		list, e := b.list(ctx)
		if e != nil {
			return out, e
		}
		out.Assistants = []agentlink.ManagedAssistant{}
		out.Workspaces = []agentlink.ExecutionWorkspace{}
		for _, a := range list {
			out.Workspaces = append(out.Workspaces, agentlink.ExecutionWorkspace{ID: a.ID, Name: a.Name + " (" + a.Mode + ")"})
			if a.Model != "" {
				out.Assistants = append(out.Assistants, managed(a))
			}
		}
		return out, nil
	}
	if r.Action != "create" && r.Action != "update" {
		return out, agentlink.ErrInvalid
	}
	if !validID(r.RequestID) || !validID(r.ID) || len(r.Name) > 80 || strings.TrimSpace(r.Name) == "" {
		return out, agentlink.ErrInvalid
	}
	// Retry lookup precedes today's catalog and revision checks.
	var payload, prior string
	e := b.db.QueryRowContext(ctx, "SELECT payload,result FROM agent_adapter_management WHERE caller=? AND request_id=?", callerKey(r.From), r.RequestID).Scan(&payload, &prior)
	if e == nil {
		if payload != encoded(r) {
			return out, agentlink.ErrConflict
		}
		return out, json.Unmarshal([]byte(prior), &out)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if err = b.validateModel(ctx, r.Model, r.Effort); err != nil {
		return out, err
	}
	err = storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		if e := tx.QueryRowContext(ctx, "SELECT payload,result FROM agent_adapter_management WHERE caller=? AND request_id=?", callerKey(r.From), r.RequestID).Scan(&payload, &prior); e == nil {
			if payload != encoded(r) {
				return agentlink.ErrConflict
			}
			return json.Unmarshal([]byte(prior), &out)
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		switch r.Action {
		case "create":
			if !validID(r.Workspace) {
				return agentlink.ErrInvalid
			}
			var workspace, mode string
			if e := tx.QueryRowContext(ctx, "SELECT workspace,mode FROM agent_adapter_assistants WHERE id=? AND deleted=0", r.Workspace).Scan(&workspace, &mode); e != nil {
				return agentlink.ErrNotFound
			}
			var count int
			if e := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_adapter_assistants").Scan(&count); e != nil {
				return e
			}
			if count >= 256 {
				return agentlink.ErrCapacity
			}
			character := assistantavatar.ForName(r.Name, r.ID)
			avatar, e := assistantavatar.Render(character, r.ID)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, "INSERT INTO agent_adapter_assistants(id,name,workspace,mode,avatar,character,model,effort) VALUES(?,?,?,?,?,?,?,?)", r.ID, r.Name, workspace, mode, avatar, character, r.Model, r.Effort)
			if e != nil {
				return agentlink.ErrConflict
			}
			// First explicit model selection also initializes the independent executor.
			if _, e = tx.ExecContext(ctx, "UPDATE agent_adapter_executor SET model=?,effort=? WHERE singleton=1 AND model=''", r.Model, r.Effort); e != nil {
				return e
			}
		case "update":
			var busy bool
			if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_adapter_jobs WHERE assistant_id=? AND (json_extract(data,'$.task.state') NOT IN ('completed','failed','cancelled') OR json_extract(data,'$.cleanup_uncertain')=1)) OR EXISTS(SELECT 1 FROM agent_messages WHERE direction='incoming' AND state IN ('queued','read') AND json_extract(envelope,'$.to.agent_id')=?)`, r.ID, r.ID).Scan(&busy); e != nil {
				return e
			}
			if busy {
				return agentlink.ErrAssistantBusy
			}
			res, e := tx.ExecContext(ctx, "UPDATE agent_adapter_assistants SET name=?,model=?,effort=?,revision=revision+1 WHERE id=? AND revision=? AND deleted=0", r.Name, r.Model, r.Effort, r.ID, r.Revision)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return agentlink.ErrAssistantChanged
			}
		default:
			return agentlink.ErrInvalid
		}
		a := assistant{ID: r.ID}
		if e := tx.QueryRowContext(ctx, "SELECT name,model,effort,revision FROM agent_adapter_assistants WHERE id=?", r.ID).Scan(&a.Name, &a.Model, &a.Effort, &a.Revision); e != nil {
			return e
		}
		value := managed(a)
		out.Assistant = &value
		_, e := tx.ExecContext(ctx, "INSERT INTO agent_adapter_management(caller,request_id,payload,result) VALUES(?,?,?,?)", callerKey(r.From), r.RequestID, encoded(r), encoded(out))
		return e
	})
	return out, err
}

func (b *backend) ChatStatus(ctx context.Context, q agentlink.ChatLookup) (out agentlink.ChatStatus, err error) {
	out.Messages = []protocol.Envelope{}
	if b.runtime().Kind != "antigravity" {
		return out, agentlink.ErrUnsupported
	}
	var raw string
	err = b.db.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=? AND direction='incoming'", q.MessageID).Scan(&raw)
	if err != nil {
		return out, agentlink.ErrNotFound
	}
	var parent protocol.Envelope
	if json.Unmarshal([]byte(raw), &parent) != nil {
		return out, agentlink.ErrConflict
	}
	if parent.From != q.From || parent.To != agentlink.AssistantTarget(b.nodeID, q.AssistantID) {
		return out, agentlink.ErrUnauthorized
	}
	out.ChatReply = protocol.ChatReply{InputID: q.MessageID, State: "queued"}
	err = b.db.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE json_extract(data,'$.message.id')=? AND caller=?", q.MessageID, callerKey(q.From)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var j job
	if err = json.Unmarshal([]byte(raw), &j); err != nil {
		return out, err
	}
	out.State = j.Task.State
	out.Error = j.Task.Error
	out.Uncertain = j.CleanupUncertain
	if out.State == "waiting_approval" {
		out.State = "waiting"
	}
	if j.Task.State == "completed" {
		if reply := messageReply(j); reply != nil {
			out.Messages = append(out.Messages, *reply)
		}
	}
	return out, nil
}
