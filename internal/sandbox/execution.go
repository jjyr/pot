package sandbox

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
)

type scopeRecord struct {
	ID                 string             `json:"id"`
	Owner              string             `json:"owner"`
	Target             string             `json:"target"`
	Workspace          string             `json:"workspace"`
	Policy             core.ShellSettings `json:"policy"`
	ThreadGeneration   uint64             `json:"thread_generation,string"`
	ProviderGeneration uint64             `json:"provider_generation,string"`
	ManagerEpoch       uint64             `json:"manager_epoch,string"`
	State              string             `json:"state"`
}

func (m *Manager) scopes(ctx context.Context, condition string, args ...any) ([]scopeRecord, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT body,state FROM sandbox_scopes WHERE "+condition, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scopeRecord{}
	for rows.Next() {
		var body []byte
		var state string
		var scope scopeRecord
		if err := rows.Scan(&body, &state); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &scope); err != nil {
			return nil, err
		}
		scope.State = state
		out = append(out, scope)
	}
	return out, rows.Err()
}
func (m *Manager) closeScope(ctx context.Context, scope scopeRecord) error {
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE sandbox_scopes SET state='closed' WHERE id=?", scope.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sandbox_leases WHERE id=?", "scope-"+scope.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM sandbox_sessions WHERE scope_id=?", scope.ID)
		return err
	})
}
func (m *Manager) cancelScope(ctx context.Context, scope scopeRecord) error {
	if scope.State == "closed" {
		return nil
	}
	_, provider, instance, err := m.provider(ctx, scope.Target)
	if err != nil {
		return m.quarantine(context.WithoutCancel(ctx), scope.Target, err)
	}
	state, err := provider.Lifecycle(ctx, instance, "status")
	if err != nil {
		return m.quarantine(context.WithoutCancel(ctx), scope.Target, err)
	}
	if state.Phase != "stopped" && !(state.Phase == "running" && state.Generation != scope.ProviderGeneration) {
		request, _ := json.Marshal(map[string]any{"id": scope.ID})
		reply, err := provider.Request(ctx, instance, "finish", request, 15*time.Second)
		var response map[string]any
		if err != nil || json.Unmarshal(reply, &response) != nil || !provider.CleanupPolicy().Confirmed(response) {
			_, _ = m.db.ExecContext(context.WithoutCancel(ctx), "UPDATE sandbox_scopes SET state='unknown' WHERE id=?", scope.ID)
			return m.quarantine(context.WithoutCancel(ctx), scope.Target, errors.Join(errors.New("guest scope cleanup unconfirmed"), err))
		}
	}
	return m.closeScope(ctx, scope)
}
func (m *Manager) scope(ctx context.Context, grant AccessGrant, definition Definition, provider Provider, state Status) (scopeRecord, error) {
	release, err := m.gate(ctx, "instance:"+definition.ID)
	if err != nil {
		return scopeRecord{}, err
	}
	defer release()
	latest, err := m.definition(ctx, definition.ID)
	if err != nil {
		return scopeRecord{}, err
	}
	if latest.Quarantined || latest.ManagerEpoch != definition.ManagerEpoch {
		return scopeRecord{}, errors.Join(core.ErrCleanupUnconfirmed, errors.New("Sandbox generation changed before scope dispatch"))
	}
	if !provider.Descriptor().Capabilities.SharedExecution {
		others, err := m.scopes(ctx, "target=? AND owner!=? AND state!='closed'", definition.ID, grant.Owner)
		if err != nil {
			return scopeRecord{}, err
		}
		if len(others) > 0 {
			return scopeRecord{}, errors.New("Sandbox requires one exclusive execution owner")
		}
	}
	candidates, err := m.scopes(ctx, "owner=? AND target=? AND state!='closed'", grant.Owner, definition.ID)
	if err != nil {
		return scopeRecord{}, err
	}
	cwd := guestCWD(provider, grant)
	for _, scope := range candidates {
		if scope.State != "unknown" && scope.ThreadGeneration == grant.ThreadGeneration && scope.ManagerEpoch == definition.ManagerEpoch && scope.ProviderGeneration == state.Generation && scope.Workspace == cwd && scope.Policy == grant.Policy {
			if scope.State == "parked" {
				if err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
					if _, err := tx.ExecContext(ctx, "UPDATE sandbox_scopes SET state='active' WHERE id=?", scope.ID); err != nil {
						return err
					}
					_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO sandbox_leases VALUES (?,?,?,'scope')", "scope-"+scope.ID, scope.Target, scope.Owner)
					return err
				}); err != nil {
					return scopeRecord{}, err
				}
				scope.State = "active"
			}
			return scope, nil
		}
		if err := m.cancelScope(ctx, scope); err != nil {
			return scopeRecord{}, err
		}
	}
	scope := scopeRecord{ID: storage.ID(), Owner: grant.Owner, Target: definition.ID, Workspace: cwd, Policy: grant.Policy, ThreadGeneration: grant.ThreadGeneration, ProviderGeneration: state.Generation, ManagerEpoch: definition.ManagerEpoch, State: "active"}
	body, _ := json.Marshal(scope)
	err = storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO sandbox_scopes VALUES (?,?,?,?,?)", scope.ID, scope.Owner, scope.Target, string(body), scope.State); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO sandbox_leases VALUES (?,?,?,'scope')", "scope-"+scope.ID, scope.Target, scope.Owner)
		return err
	})
	return scope, err
}
func (m *Manager) SessionTarget(ctx context.Context, owner string, epoch uint64, session string) (core.ExecutionTarget, error) {
	var body []byte
	var state string
	if err := m.db.QueryRowContext(ctx, `SELECT s.body,s.state FROM sandbox_sessions h JOIN sandbox_scopes s ON s.id=h.scope_id WHERE h.id=? AND s.owner=?`, session, owner).Scan(&body, &state); err != nil {
		return core.ExecutionTarget{}, errors.New("process/plugin session expired or belongs to another owner")
	}
	var scope scopeRecord
	if err := json.Unmarshal(body, &scope); err != nil {
		return core.ExecutionTarget{}, err
	}
	if scope.ThreadGeneration != epoch || state == "closed" || state == "unknown" {
		return core.ExecutionTarget{}, errors.New("session Agent epoch expired")
	}
	return core.SandboxTarget(m.runtimeID, scope.Target), nil
}
func decodeArgs(data json.RawMessage) (map[string]any, error) {
	var args map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&args); err != nil {
		return nil, err
	}
	if args == nil {
		return nil, errors.New("tool args must be an object")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing tool argument data")
	}
	return args, nil
}
func (m *Manager) PrepareOperation(ctx context.Context, grant AccessGrant, name string, args json.RawMessage) (json.RawMessage, error) {
	if err := grant.validate(m.runtimeID); err != nil {
		return nil, err
	}
	switch name {
	case "exec_command", "write_stdin", "apply_patch", "plugin_call":
	default:
		return nil, errors.New("unsupported Sandbox execution tool")
	}
	if _, err := decodeArgs(args); err != nil {
		return nil, err
	}
	ctx, done, err := m.beginCall(ctx, grant.Owner)
	if err != nil {
		return nil, err
	}
	defer done()
	lease, err := m.Acquire(ctx, grant.Target.Target.SandboxID, grant.Owner)
	if err != nil {
		return nil, err
	}
	defer lease.Release(context.WithoutCancel(ctx))
	_, provider, instance, _, err := m.ready(ctx, grant.Target.Target.SandboxID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"tool": name, "args": args, "cwd": guestCWD(provider, grant), "role": role(grant), "policy": grant.Policy})
	if err != nil {
		return nil, err
	}
	reply, err := provider.Request(ctx, instance, "prepare", body, 15*time.Second)
	if err != nil {
		return nil, err
	}
	var response struct {
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(reply, &response); err != nil {
		return nil, err
	}
	if _, err := decodeArgs(response.Args); err != nil {
		return nil, err
	}
	return response.Args, nil
}
func role(grant AccessGrant) string {
	if grant.Worker {
		return "task"
	}
	return "main"
}
func (m *Manager) beginCall(ctx context.Context, owner string) (context.Context, func(), error) {
	callCtx, cancel := context.WithCancel(ctx)
	id := storage.ID()
	m.mu.Lock()
	if m.closed || m.stopping {
		m.mu.Unlock()
		cancel()
		return nil, nil, errors.New("Sandbox Manager closed")
	}
	if m.calls[owner] == nil {
		m.calls[owner] = map[string]context.CancelFunc{}
	}
	m.calls[owner][id] = cancel
	m.operations.Add(1)
	m.mu.Unlock()
	return callCtx, func() {
		cancel()
		m.mu.Lock()
		delete(m.calls[owner], id)
		if len(m.calls[owner]) == 0 {
			delete(m.calls, owner)
		}
		m.mu.Unlock()
		m.operations.Done()
	}, nil
}
func (m *Manager) Execute(ctx context.Context, grant AccessGrant, callID, name string, args json.RawMessage, emit func(core.Update) error) (json.RawMessage, error) {
	if err := grant.validate(m.runtimeID); err != nil {
		return nil, err
	}
	if callID == "" || len(callID) > 256 {
		return nil, errors.New("invalid call ID")
	}
	switch name {
	case "exec_command", "write_stdin", "apply_patch", "plugin_call":
	default:
		return nil, errors.New("unsupported Sandbox execution tool")
	}
	values, err := decodeArgs(args)
	if err != nil {
		return nil, err
	}
	ctx, done, err := m.beginCall(ctx, grant.Owner)
	if err != nil {
		return nil, err
	}
	defer done()
	release, err := m.gate(ctx, "owner:"+grant.Owner)
	if err != nil {
		return nil, err
	}
	defer release()
	key, err := core.HashJSON([]any{grant.Owner, grant.Turn, strconv.FormatUint(grant.ThreadGeneration, 10), callID})
	if err != nil {
		return nil, err
	}
	digest, err := core.HashJSON([]any{grant.Target, grant.WorkspaceOwner, grant.Worker, grant.Policy, name, args})
	if err != nil {
		return nil, err
	}
	var oldDigest, oldState string
	var oldResult []byte
	err = m.db.QueryRowContext(ctx, "SELECT digest,state,result FROM sandbox_receipts WHERE key=?", key).Scan(&oldDigest, &oldState, &oldResult)
	if err == nil {
		if digest != oldDigest {
			return nil, errors.New("operation request identity conflict")
		}
		if oldState == "complete" {
			return json.RawMessage(oldResult), nil
		}
		return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("prior Sandbox operation outcome unknown; never replay"))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if session, ok := values["session_id"].(string); ok {
		target, err := m.SessionTarget(ctx, grant.Owner, grant.ThreadGeneration, session)
		if err != nil || target != grant.Target {
			return nil, errors.Join(errors.New("session target/owner binding mismatch"), err)
		}
	}
	lease, err := m.Acquire(ctx, grant.Target.Target.SandboxID, grant.Owner)
	if err != nil {
		return nil, err
	}
	defer lease.Release(context.WithoutCancel(ctx))
	definition, provider, instance, state, err := m.ready(ctx, grant.Target.Target.SandboxID)
	if err != nil {
		return nil, err
	}
	if session, ok := values["session_id"].(string); ok {
		var scopeBody []byte
		if err := m.db.QueryRowContext(ctx, "SELECT s.body FROM sandbox_sessions h JOIN sandbox_scopes s ON s.id=h.scope_id WHERE h.id=?", session).Scan(&scopeBody); err != nil {
			return nil, err
		}
		var previous scopeRecord
		if json.Unmarshal(scopeBody, &previous) != nil || previous.ManagerEpoch != definition.ManagerEpoch || previous.ProviderGeneration != state.Generation {
			return nil, errors.New("session VM generation expired")
		}
	}
	scope, err := m.scope(ctx, grant, definition, provider, state)
	if err != nil {
		return nil, err
	}
	operationID := storage.ID()
	if _, err := m.db.ExecContext(ctx, "INSERT INTO sandbox_receipts VALUES (?,?,?,?,?,'started',NULL)", key, definition.ID, grant.Owner, digest, operationID); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{"id": operationID, "scope": scope.ID, "operation": name, "args": args, "cwd": scope.Workspace, "role": role(grant), "policy": grant.Policy})
	if err != nil {
		return nil, err
	}
	raw, err := provider.Request(ctx, instance, "execute", body, 660*time.Second)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cleanup := m.cancelScope(cleanupCtx, scope)
		if ctx.Err() != nil && cleanup == nil {
			result, _ := json.Marshal(map[string]any{"isError": true, "error": "execution cancelled; prior effects are not rolled back", "cancelled": true, "sandbox_id": definition.ID})
			_, save := m.db.ExecContext(cleanupCtx, "UPDATE sandbox_receipts SET state='complete',result=? WHERE key=?", []byte(result), key)
			return nil, errors.Join(ctx.Err(), save)
		}
		_, _ = m.db.ExecContext(cleanupCtx, "UPDATE sandbox_receipts SET state='unknown' WHERE key=?", key)
		return nil, m.quarantine(cleanupCtx, definition.ID, errors.Join(err, cleanup))
	}
	var response struct {
		Result map[string]any    `json:"result"`
		Events []json.RawMessage `json:"events"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil || response.Result == nil {
		_, _ = m.db.ExecContext(context.WithoutCancel(ctx), "UPDATE sandbox_receipts SET state='unknown' WHERE key=?", key)
		return nil, m.quarantine(context.WithoutCancel(ctx), definition.ID, errors.New("invalid guest execution response; outcome unknown"))
	}
	if response.Result["cleanup_unconfirmed"] == true {
		_, _ = m.db.ExecContext(context.WithoutCancel(ctx), "UPDATE sandbox_receipts SET state='unknown' WHERE key=?", key)
		return nil, m.quarantine(context.WithoutCancel(ctx), definition.ID, errors.New("guest executor cleanup unconfirmed"))
	}
	response.Result["sandbox_id"] = definition.ID
	result, err := json.Marshal(response.Result)
	if err != nil {
		return nil, err
	}
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer persistCancel()
	err = storage.Transaction(persistCtx, m.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(persistCtx, "UPDATE sandbox_receipts SET state='complete',result=? WHERE key=?", []byte(result), key); err != nil {
			return err
		}
		if session, ok := response.Result["session_id"].(string); ok && session != "" {
			var bound string
			err := tx.QueryRowContext(persistCtx, "SELECT scope_id FROM sandbox_sessions WHERE id=?", session).Scan(&bound)
			if err == nil && bound != scope.ID {
				return errors.New("guest returned a session belonging to another scope")
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			_, err = tx.ExecContext(persistCtx, "INSERT OR IGNORE INTO sandbox_sessions VALUES (?,?)", session, scope.ID)
			return err
		}
		if session, ok := values["session_id"].(string); ok {
			_, err := tx.ExecContext(persistCtx, "DELETE FROM sandbox_sessions WHERE id=?", session)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, m.quarantine(context.WithoutCancel(ctx), definition.ID, err)
	}
	remaining := int(grant.Policy.MaxOutputBytes * 2)
	for _, event := range response.Events {
		var wire struct {
			Output struct {
				ID     string `json:"id"`
				Stream string `json:"stream"`
				Text   string `json:"text"`
			} `json:"ToolOutput"`
		}
		if json.Unmarshal(event, &wire) == nil && wire.Output.Text != "" && emit != nil && remaining > 0 && (wire.Output.Stream == "stdout" || wire.Output.Stream == "stderr") {
			if len(wire.Output.Text) > remaining {
				wire.Output.Text = strings.ToValidUTF8(wire.Output.Text[:remaining], "")
			}
			remaining -= len(wire.Output.Text)
			if err := emit(core.Update{Type: "tool_output", ID: callID, Stream: wire.Output.Stream, Text: wire.Output.Text}); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}
func (m *Manager) Finish(ctx context.Context, owner string) error { return m.finish(ctx, owner, false) }
func (m *Manager) Cancel(ctx context.Context, owner string) error { return m.finish(ctx, owner, true) }
func (m *Manager) finish(ctx context.Context, owner string, cancelCalls bool) error {
	if cancelCalls {
		m.mu.Lock()
		for _, cancel := range m.calls[owner] {
			cancel()
		}
		m.mu.Unlock()
	}
	release, err := m.gate(ctx, "owner:"+owner)
	if err != nil {
		return err
	}
	defer release()
	scopes, err := m.scopes(ctx, "owner=? AND state!='closed'", owner)
	if err != nil {
		return err
	}
	var result error
	for _, scope := range scopes {
		result = errors.Join(result, m.cancelScope(ctx, scope))
	}
	var pending int
	if err := m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sandbox_receipts WHERE owner=? AND state IN ('started','unknown')", owner).Scan(&pending); err != nil {
		return errors.Join(result, err)
	}
	if pending > 0 {
		result = errors.Join(result, core.ErrCleanupUnconfirmed, errors.New("owner has uncertain Sandbox operations"))
	}
	return result
}
