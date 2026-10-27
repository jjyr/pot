package agenthost

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func scopeID(path, mode string) string {
	digest := sha256.Sum256([]byte(path + "\x00" + mode))
	return "scope-" + hex.EncodeToString(digest[:16])
}
func validateModelChoice(models []agentlink.ExecutionModel, model, effort string) error {
	for _, m := range models {
		if m.ID == model {
			if effort == "" {
				return nil
			}
			for _, value := range m.Efforts {
				if effort == value {
					return nil
				}
			}
			return agentlink.ErrInvalid
		}
	}
	return agentlink.ErrModelUnavailable
}
func supportsEffort(models []agentlink.ExecutionModel) bool {
	for _, model := range models {
		if len(model.Efforts) > 0 {
			return true
		}
	}
	return false
}
func (b *backend) validateModel(ctx context.Context, model, effort string) error {
	models, err := b.models(ctx)
	if err != nil {
		return agentlink.ErrModelUnavailable
	}
	// The local owner CLI retains the native Antigravity global option. Remote
	// management only accepts effort choices explicitly advertised by its catalog.
	if b.runtime().Kind == "antigravity" {
		switch effort {
		case "", "low", "medium", "high", "xhigh", "max":
		default:
			return agentlink.ErrInvalid
		}
		return validateModelChoice(models, model, "")
	}
	return validateModelChoice(models, model, effort)
}
func (b *backend) managed(a assistant, workspace string) agentlink.ManagedAssistant {
	ops := []agentlink.Operation{agentlink.AssistantProfile, agentlink.AssistantDelete}
	if a.Model != "" {
		ops = append(ops, agentlink.ChatRead, agentlink.ChatSend, agentlink.ChatReceipt, agentlink.ChatSubscribe)
	}
	return agentlink.ManagedAssistant{Avatar: a.Avatar, Capabilities: agentlink.Description{Version: 1, Scope: "peer", Operations: ops}, Address: agentlink.AssistantTarget(b.nodeID, a.ID), ID: a.ID, Name: a.Name, Model: a.Model, Effort: a.Effort, Workspace: workspace, Revision: a.Revision}
}

// Management is an operator RPC. Transport binds From to the authenticated peer;
// model tools never expose this interface. Scopes only originate with the owner.
func (b *backend) ManageAssistant(ctx context.Context, r agentlink.AssistantManagementRequest) (out agentlink.AssistantManagementResult, err error) {
	if !r.From.Valid() || r.From.Kind != "user" {
		return out, agentlink.ErrUnauthorized
	}
	if r.Action == "catalog" {
		out.SchemaVersion = 1
		out.Runtime = agentlink.ManagementRuntime{Kind: b.runtime().Kind, Name: b.runtime().Name}
		out.Actions = []string{"catalog", "create", "update"}
		out.Fields = []agentlink.ManagementField{{Name: "name", Required: true, Editable: true}, {Name: "model", Required: true, Editable: true}, {Name: "workspace", Required: true, Editable: false}}
		out.Models, err = b.models(ctx)
		if err != nil {
			return out, agentlink.ErrModelUnavailable
		}
		for _, model := range out.Models {
			if len(model.Efforts) > 0 {
				out.Fields = append(out.Fields, agentlink.ManagementField{Name: "effort", Editable: true})
				break
			}
		}
		b.managementMu.Lock()
		defer b.managementMu.Unlock()
		out.Workspaces = []agentlink.ExecutionWorkspace{}
		out.Assistants = []agentlink.ManagedAssistant{}
		scopes := map[string]string{}
		rows, e := b.db.QueryContext(ctx, "SELECT id,workspace,mode FROM agent_adapter_workspaces ORDER BY rowid")
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var id, path, mode string
			if e = rows.Scan(&id, &path, &mode); e != nil {
				rows.Close()
				return out, e
			}
			scopes[path+"\x00"+mode] = id
			out.Workspaces = append(out.Workspaces, agentlink.ExecutionWorkspace{ID: id, Name: filepath.Base(path), Description: path, Mode: mode})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		list, e := b.list(ctx)
		if e != nil {
			return out, e
		}
		for _, a := range list {
			if b.runtime().Kind != "antigravity" || a.Model != "" {
				out.Assistants = append(out.Assistants, b.managed(a, scopes[a.Workspace+"\x00"+a.Mode]))
			}
		}
		if len(out.Models) > 0 {
			out.Fields[1].Default = out.Models[0].ID
		}
		if len(out.Workspaces) > 0 {
			out.Fields[2].Default = out.Workspaces[0].ID
		}
		return out, nil
	}
	if r.Action != "create" && r.Action != "update" {
		return out, agentlink.ErrUnsupported
	}
	if !validID(r.RequestID) || !validID(r.ID) {
		return out, agentlink.ErrInvalid
	}
	b.managementMu.Lock()
	defer b.managementMu.Unlock()
	// Resolve committed retries before today's model catalog or revision checks.
	var payload, prior string
	e := b.db.QueryRowContext(ctx, "SELECT payload,result FROM agent_adapter_management WHERE caller=? AND request_id=?", callerKey(r.From), r.RequestID).Scan(&payload, &prior)
	if e == nil {
		if payload != encoded(r) {
			return out, agentlink.ErrConflict
		}
		err = json.Unmarshal([]byte(prior), &out)
		if out.Assistant != nil {
			value := b.managed(assistant{ID: out.Assistant.ID, Name: out.Assistant.Name, Avatar: out.Assistant.Avatar, Model: out.Assistant.Model, Effort: out.Assistant.Effort, Revision: out.Assistant.Revision}, out.Assistant.Workspace)
			out.Assistant = &value
		}
		return out, err
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if r.Action == "create" && (r.Name == nil || r.Model == nil) {
		return out, agentlink.ErrInvalid
	}
	current := assistant{}
	if r.Action == "update" {
		current, err = b.resolve(ctx, r.ID)
		if err != nil {
			return out, err
		}
		if r.Revision != current.Revision {
			return out, agentlink.ErrAssistantChanged
		}
		if r.Workspace != "" {
			return out, agentlink.ErrUnsupported
		}
	}
	effectiveName, effectiveModel := current.Name, current.Model
	if r.Name != nil {
		effectiveName = *r.Name
	}
	if r.Model != nil {
		effectiveModel = *r.Model
	}
	if err = validateProfileName(effectiveName); err != nil {
		return out, err
	}
	// Omitted optional configuration preserves the receiver's stored value. An
	// explicit empty effort selects the native model default, only when supported.
	effectiveEffort := current.Effort
	if r.Effort != nil {
		effectiveEffort = *r.Effort
	}
	if r.Action == "create" || r.Effort != nil || effectiveModel != current.Model || effectiveEffort != current.Effort {
		models, e := b.models(ctx)
		if e != nil {
			return out, agentlink.ErrModelUnavailable
		}
		if r.Effort != nil && !supportsEffort(models) {
			return out, agentlink.ErrUnsupported
		}
		if err = validateModelChoice(models, effectiveModel, effectiveEffort); err != nil {
			return out, err
		}
	}
	err = storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		if e := tx.QueryRowContext(ctx, "SELECT payload,result FROM agent_adapter_management WHERE caller=? AND request_id=?", callerKey(r.From), r.RequestID).Scan(&payload, &prior); e == nil {
			if payload != encoded(r) {
				return agentlink.ErrConflict
			}
			if e = json.Unmarshal([]byte(prior), &out); e != nil {
				return e
			}
			if out.Assistant != nil {
				value := b.managed(assistant{ID: out.Assistant.ID, Name: out.Assistant.Name, Avatar: out.Assistant.Avatar, Model: out.Assistant.Model, Effort: out.Assistant.Effort, Revision: out.Assistant.Revision}, out.Assistant.Workspace)
				out.Assistant = &value
			}
			return nil
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		switch r.Action {
		case "create":
			var path, mode string
			if e := tx.QueryRowContext(ctx, "SELECT workspace,mode FROM agent_adapter_workspaces WHERE id=?", r.Workspace).Scan(&path, &mode); errors.Is(e, sql.ErrNoRows) {
				return agentlink.ErrWorkspaceUnavailable
			} else if e != nil {
				return e
			}
			resolved, e := filepath.EvalSymlinks(path)
			if e != nil || resolved != path {
				return agentlink.ErrWorkspaceUnavailable
			}
			info, e := os.Stat(path)
			if e != nil || !info.IsDir() {
				return agentlink.ErrWorkspaceUnavailable
			}
			var count int
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_adapter_assistants").Scan(&count); e != nil {
				return e
			}
			if count >= 256 {
				return agentlink.ErrCapacity
			}
			character := assistantavatar.ForName(effectiveName, r.ID)
			avatar, e := assistantavatar.Render(character, r.ID)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO agent_adapter_assistants(id,name,workspace,mode,avatar,character,model,effort) VALUES(?,?,?,?,?,?,?,?)", r.ID, effectiveName, path, mode, avatar, character, effectiveModel, effectiveEffort); e != nil {
				return agentlink.ErrConflict
			}
		case "update":
			// Each execution captures its Session before launch. Save the next
			// execution's model/effort without waiting for or changing that Session.
			res, e := tx.ExecContext(ctx, "UPDATE agent_adapter_assistants SET name=?,model=?,effort=?,revision=revision+1 WHERE id=? AND revision=? AND deleted=0", effectiveName, effectiveModel, effectiveEffort, r.ID, r.Revision)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return agentlink.ErrAssistantChanged
			}
		}
		a := assistant{ID: r.ID}
		var workspace string
		if e := tx.QueryRowContext(ctx, "SELECT a.name,a.model,a.effort,a.revision,w.id,a.avatar FROM agent_adapter_assistants a JOIN agent_adapter_workspaces w ON w.workspace=a.workspace AND w.mode=a.mode WHERE a.id=?", r.ID).Scan(&a.Name, &a.Model, &a.Effort, &a.Revision, &workspace, &a.Avatar); e != nil {
			return e
		}
		value := b.managed(a, workspace)
		out.Assistant = &value
		// Private durable receipt format is independent of the wire catalog schema.
		receipt := map[string]any{"assistant": map[string]any{"id": value.ID, "name": value.Name, "model": value.Model, "effort": value.Effort, "workspace": value.Workspace, "revision": value.Revision, "avatar": value.Avatar}}
		_, e := tx.ExecContext(ctx, "INSERT INTO agent_adapter_management(caller,request_id,payload,result) VALUES(?,?,?,?)", callerKey(r.From), r.RequestID, encoded(r), encoded(receipt))
		return e
	})
	return out, err
}

func (b *backend) ChatStatus(ctx context.Context, q agentlink.ChatLookup) (out agentlink.ChatStatus, err error) {
	out.Messages = []protocol.Envelope{}
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
