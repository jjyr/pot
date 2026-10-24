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
	"sync"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type assistant struct {
	ID, Name, Workspace, Mode, ThreadID string
	Model, Effort                       string
	Avatar, Character                   string
	Revision                            uint64
	Default                             bool
}
type job struct {
	Task             agentlink.Task        `json:"task"`
	Request          agentlink.TaskRequest `json:"request"`
	Message          *protocol.Envelope    `json:"message,omitempty"`
	ThreadID         string                `json:"thread_id,omitempty"`
	TurnID           string                `json:"turn_id,omitempty"`
	Files            []string              `json:"files,omitempty"`
	CleanupUncertain bool                  `json:"cleanup_uncertain,omitempty"`
}

// backend owns accepted work independently of link lifecycle. Its work queue is
// independent for explicit tasks and serialized per assistant for messages.
type backend struct {
	adapter              Runtime
	db                   *sql.DB
	nodeID, binary, root string
	worker               []string
	mu                   sync.Mutex
	active               map[string]context.CancelFunc
	workers              map[string]bool
	reviews              map[string]*pendingReview
}

func newBackend(db *sql.DB, nodeID, root string, runtimes ...Runtime) (*backend, error) {
	adapter := runtimeOrDefault(runtimes)
	binary := adapter.Binary
	worker, err := os.Executable()
	if err != nil {
		return nil, err
	}
	b := &backend{adapter: adapter, db: db, nodeID: nodeID, root: root, binary: binary, worker: []string{worker}, active: map[string]context.CancelFunc{}, workers: map[string]bool{}, reviews: map[string]*pendingReview{}}
	// Fresh installations have an isolated local read-only workspace. Owners can
	// add assistants pointing at their own directories through the local command.
	workspace := filepath.Join(root, "workspace")
	if err = os.MkdirAll(workspace, 0700); err != nil {
		return nil, err
	}
	name := assistantavatar.RandomName(nil)
	character := assistantavatar.ForName(name, adapter.Kind)
	avatar, err := assistantavatar.Render(character, adapter.Kind)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`INSERT INTO agent_adapter_assistants(id,name,workspace,mode,is_default,avatar,character) SELECT ?,?,?,'read-only',1,?,? WHERE NOT EXISTS(SELECT 1 FROM agent_adapter_assistants)`, adapter.Kind, name, workspace, avatar, character)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec("INSERT INTO agent_adapter_executor(singleton,workspace,mode) VALUES(1,?,'read-only') ON CONFLICT DO NOTHING", workspace); err != nil {
		return nil, err
	}
	return b, nil
}
func (b *backend) list(ctx context.Context) ([]assistant, error) {
	rows, err := b.db.QueryContext(ctx, "SELECT id,name,workspace,mode,thread_id,is_default,avatar,character,revision,model,effort FROM agent_adapter_assistants WHERE deleted=0 ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []assistant{}
	for rows.Next() {
		var a assistant
		if err = rows.Scan(&a.ID, &a.Name, &a.Workspace, &a.Mode, &a.ThreadID, &a.Default, &a.Avatar, &a.Character, &a.Revision, &a.Model, &a.Effort); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
func (b *backend) identity(a assistant) agentlink.Identity {
	return agentlink.Identity{Address: protocol.AgentRef{NodeID: b.nodeID, Kind: "assistant", AgentID: a.ID}, Name: a.Name, Adapter: b.runtime().Kind}
}
func (b *backend) Directory(ctx context.Context) (agentlink.Directory, error) {
	list, err := b.list(ctx)
	d := agentlink.Directory{NodeID: b.nodeID, Name: b.runtime().Name, Adapter: b.runtime().Kind, Targets: []agentlink.Endpoint{}}
	nodeOps := []agentlink.Operation{agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput, agentlink.Approvals}
	if b.runtime().Kind == "antigravity" {
		nodeOps = []agentlink.Operation{agentlink.AssistantManagement, agentlink.TaskSubmit, agentlink.TaskRead, agentlink.TaskCancel, agentlink.TaskInput}
	}
	d.Targets = append(d.Targets, agentlink.Endpoint{Identity: agentlink.Identity{Address: agentlink.NodeTarget(b.nodeID), Name: d.Name, Adapter: d.Adapter}, Capabilities: agentlink.Description{Version: 1, Scope: "caller", Operations: nodeOps}})
	for _, a := range list {
		if b.runtime().Kind == "antigravity" && a.Model == "" {
			continue
		}
		identity := b.identity(a)
		identity.Avatar = a.Avatar
		assistantOps := []agentlink.Operation{agentlink.AssistantProfile, agentlink.AssistantDelete}
		if b.runtime().Kind == "antigravity" {
			assistantOps = append(assistantOps, agentlink.ChatRead, agentlink.ChatSend, agentlink.ChatReceipt, agentlink.ChatSubscribe)
		}
		d.Targets = append(d.Targets, agentlink.Endpoint{Identity: identity, Capabilities: agentlink.Description{Version: 1, Scope: "peer", Operations: assistantOps}})
		if a.Default {
			ref := identity.Address
			d.DefaultTarget = &ref
		}
	}
	return d, err
}
func (b *backend) resolve(ctx context.Context, id string) (assistant, error) {
	var a assistant
	query := "SELECT id,name,workspace,mode,thread_id,is_default,avatar,character,revision,model,effort FROM agent_adapter_assistants WHERE id=? AND deleted=0"
	var err error
	if id == "" {
		err = b.db.QueryRowContext(ctx, "SELECT id,name,workspace,mode,thread_id,is_default,avatar,character,revision,model,effort FROM agent_adapter_assistants WHERE is_default=1 AND deleted=0").Scan(&a.ID, &a.Name, &a.Workspace, &a.Mode, &a.ThreadID, &a.Default, &a.Avatar, &a.Character, &a.Revision, &a.Model, &a.Effort)
	} else {
		err = b.db.QueryRowContext(ctx, query, id).Scan(&a.ID, &a.Name, &a.Workspace, &a.Mode, &a.ThreadID, &a.Default, &a.Avatar, &a.Character, &a.Revision, &a.Model, &a.Effort)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = agentlink.ErrNotFound
	}
	return a, err
}
func (b *backend) ownerConfigure(ctx context.Context, a assistant) error {
	if !validID(a.ID) || !filepath.IsAbs(a.Workspace) || (a.Mode != "read-only" && a.Mode != "workspace-write") {
		return agentlink.ErrInvalid
	}
	path, err := filepath.EvalSymlinks(a.Workspace)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return agentlink.ErrInvalid
	}
	a.Workspace = path
	if b.runtime().Kind == "antigravity" {
		if err := b.validateModel(ctx, a.Model, a.Effort); err != nil {
			return err
		}
	}
	return storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		var workspace, mode, existingName, existingModel, existingEffort string
		var retired bool
		if err := tx.QueryRowContext(ctx, "SELECT deleted FROM agent_adapter_assistants WHERE id=?", a.ID).Scan(&retired); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if retired {
			return agentlink.ErrNotFound
		}
		err := tx.QueryRowContext(ctx, "SELECT workspace,mode,name,model,effort FROM agent_adapter_assistants WHERE id=? AND deleted=0", a.ID).Scan(&workspace, &mode, &existingName, &existingModel, &existingEffort)
		// A session's workspace/permission scope is immutable. Create another virtual
		// assistant to use a different scope; accepted work keeps its original owner.
		if err == nil && (workspace != a.Workspace || mode != a.Mode) {
			return agentlink.ErrConflict
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (a.Model != existingModel || a.Effort != existingEffort) {
			var busy bool
			if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_adapter_jobs WHERE assistant_id=? AND (json_extract(data,'$.task.state') NOT IN ('completed','failed','cancelled') OR json_extract(data,'$.cleanup_uncertain')=1)) OR EXISTS(SELECT 1 FROM agent_messages WHERE direction='incoming' AND state IN ('queued','read') AND json_extract(envelope,'$.to.agent_id')=?)`, a.ID, a.ID).Scan(&busy); e != nil {
				return e
			}
			if busy {
				return agentlink.ErrAssistantBusy
			}
		}
		if a.Name == "" {
			a.Name = existingName
		}
		if a.Name == "" {
			rows, err := tx.QueryContext(ctx, "SELECT name FROM agent_adapter_assistants")
			if err != nil {
				return err
			}
			var used []string
			for rows.Next() {
				var name string
				if err = rows.Scan(&name); err != nil {
					rows.Close()
					return err
				}
				used = append(used, name)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			a.Name = assistantavatar.RandomName(used)
		}
		if err := validateProfileName(a.Name); err != nil {
			return err
		}
		a.Character = assistantavatar.ForName(a.Name, a.ID)
		a.Avatar, err = assistantavatar.Render(a.Character, a.ID)
		if err != nil {
			return err
		}
		if a.Default {
			if _, err = tx.ExecContext(ctx, "UPDATE agent_adapter_assistants SET is_default=0"); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO agent_adapter_assistants(id,name,workspace,mode,is_default,avatar,character,model,effort) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,model=excluded.model,effort=excluded.effort,revision=agent_adapter_assistants.revision+CASE WHEN agent_adapter_assistants.name<>excluded.name OR agent_adapter_assistants.model<>excluded.model OR agent_adapter_assistants.effort<>excluded.effort THEN 1 ELSE 0 END,is_default=CASE WHEN excluded.is_default=1 THEN 1 ELSE agent_adapter_assistants.is_default END`, a.ID, a.Name, a.Workspace, a.Mode, a.Default, a.Avatar, a.Character, a.Model, a.Effort)
		return err
	})
}
func callerKey(from protocol.AgentRef) string { return encoded(from) }
func jobID(from protocol.AgentRef, request string) string {
	sum := sha256.Sum256([]byte(callerKey(from) + "\x00" + request))
	return hex.EncodeToString(sum[:])
}
func (b *backend) Submit(ctx context.Context, r agentlink.TaskRequest) (agentlink.Task, error) {
	if r.Target != agentlink.NodeTarget(b.nodeID) || r.Configuration != nil {
		return agentlink.Task{}, agentlink.ErrUnsupported
	}
	if !validID(r.RequestID) || !r.From.Valid() || r.Text == "" || len(r.Text) > 24000 {
		return agentlink.Task{}, agentlink.ErrInvalid
	}
	return b.submitTask(ctx, r)
}

// submitMessage admits one addressed inbox envelope. It never resolves a node
// default or accepts task workspace/configuration overrides.
func (b *backend) submitMessage(ctx context.Context, r agentlink.TaskRequest, message protocol.Envelope) (agentlink.Task, error) {
	if r.Target.Kind != "assistant" || r.Target.NodeID != b.nodeID || r.Configuration != nil || r.Workspace != "" {
		return agentlink.Task{}, agentlink.ErrUnsupported
	}
	if !validID(r.RequestID) || message.Validate() != nil || message.Kind != "message" ||
		(message.From.Kind != "assistant" && message.From.Kind != "task" && !(b.runtime().Kind == "antigravity" && message.From.Kind == "user")) || message.To.NodeID != b.nodeID || message.To.Kind != "assistant" ||
		message.From != r.From || message.To.AgentID != r.Target.AgentID || message.Text != r.Text {
		return agentlink.Task{}, agentlink.ErrInvalid
	}
	payload := encoded(r)
	// Exact retries retain their accepted explicit recipient.
	var oldPayload, oldData string
	priorErr := b.db.QueryRowContext(ctx, "SELECT payload,data FROM agent_adapter_jobs WHERE caller=? AND request_id=?", callerKey(r.From), r.RequestID).Scan(&oldPayload, &oldData)
	if priorErr == nil {
		if oldPayload != payload {
			return agentlink.Task{}, agentlink.ErrConflict
		}
		var previous job
		if err := json.Unmarshal([]byte(oldData), &previous); err != nil {
			return agentlink.Task{}, err
		}
		return previous.Task, nil
	}
	if !errors.Is(priorErr, sql.ErrNoRows) {
		return agentlink.Task{}, priorErr
	}
	a, err := b.resolve(ctx, r.Target.AgentID)
	if err != nil {
		return agentlink.Task{}, err
	}
	var accepted job
	err = storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		var data, old string
		err := tx.QueryRowContext(ctx, "SELECT payload,data FROM agent_adapter_jobs WHERE caller=? AND request_id=?", callerKey(r.From), r.RequestID).Scan(&old, &data)
		if err == nil {
			if old != payload {
				return agentlink.ErrConflict
			}
			return json.Unmarshal([]byte(data), &accepted)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = activeAssistantTx(ctx, tx, a.ID); err != nil {
			return err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_adapter_jobs WHERE json_extract(data,'$.task.state') IN ('queued','running','cancelling','waiting_approval')`).Scan(&count); err != nil {
			return err
		}
		if count >= 32 {
			return agentlink.ErrCapacity
		}
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM agent_adapter_jobs").Scan(&count); err != nil {
			return err
		}
		if count >= 256 {
			return agentlink.ErrCapacity
		}
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_adapter_jobs WHERE assistant_id=? AND json_extract(data,'$.cleanup_uncertain')=1`, a.ID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return agentlink.ErrFenced
		}
		now := time.Now().UTC()
		accepted = job{Task: agentlink.Task{ID: jobID(r.From, r.RequestID), RequestID: r.RequestID, Receiver: b.identity(a), State: "queued", CreatedAt: now, UpdatedAt: now}, Request: r, Message: &message}
		_, err = tx.ExecContext(ctx, "INSERT INTO agent_adapter_jobs(id,caller,request_id,assistant_id,payload,data) VALUES(?,?,?,?,?,?)", accepted.Task.ID, callerKey(r.From), r.RequestID, a.ID, payload, encoded(accepted))
		return err
	})
	return accepted.Task, err
}
func (b *backend) read(ctx context.Context, from protocol.AgentRef, id string) (job, error) {
	var raw string
	var j job
	err := b.db.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE caller=? AND id=?", callerKey(from), id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return j, agentlink.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &j)
	}
	return j, err
}
func (b *backend) Status(ctx context.Context, from protocol.AgentRef, id string) (agentlink.Task, error) {
	j, err := b.read(ctx, from, id)
	return j.Task, err
}
func terminal(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled"
}
func (b *backend) update(ctx context.Context, id string, fn func(*job) error) error {
	return storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE id=?", id).Scan(&raw); err != nil {
			return err
		}
		var j job
		if err := json.Unmarshal([]byte(raw), &j); err != nil {
			return err
		}
		if err := fn(&j); err != nil {
			return err
		}
		j.Task.UpdatedAt = time.Now().UTC()
		_, err := tx.ExecContext(ctx, "UPDATE agent_adapter_jobs SET data=? WHERE id=?", encoded(j), id)
		return err
	})
}
func (b *backend) Cancel(ctx context.Context, from protocol.AgentRef, id string) (agentlink.Task, error) {
	if _, err := b.read(ctx, from, id); err != nil {
		return agentlink.Task{}, err
	}
	err := b.update(ctx, id, func(j *job) error {
		if terminal(j.Task.State) {
			return nil
		}
		if j.Task.State == "queued" {
			j.Task.State = "cancelled"
		} else {
			j.Task.State = "cancelling"
		}
		return nil
	})
	if err != nil {
		return agentlink.Task{}, err
	}
	b.mu.Lock()
	cancel := b.active[id]
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return b.Status(ctx, from, id)
}

// recover fences every accepted unfinished turn. Even a queued task is not
// silently replayed after a daemon restart. Historical IDs remain retryable.
func (b *backend) recover(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, "SELECT id,data FROM agent_adapter_jobs WHERE json_extract(data,'$.task.state') NOT IN ('completed','failed','cancelled')")
	if err != nil {
		return err
	}
	var jobs []job
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var j job
		if err = json.Unmarshal([]byte(raw), &j); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err = b.update(ctx, j.Task.ID, func(j *job) error {
			if j.Task.State == "queued" {
				j.Task.Error = b.runtime().Kind + "_host_restarted_before_execution"
			} else {
				j.Task.Error = b.runtime().Kind + "_host_restarted_outcome_unknown"
				j.CleanupUncertain = true
			}
			j.Task.State = "failed"
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
func (b *backend) run(ctx context.Context) {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		rows, queryErr := b.db.QueryContext(ctx, `SELECT data FROM agent_adapter_jobs WHERE assistant_id IS NULL AND json_extract(data,'$.task.state')='queued'`)
		var direct []job
		if queryErr == nil {
			for rows.Next() {
				var raw string
				var j job
				if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &j) == nil {
					direct = append(direct, j)
				}
			}
			rows.Close()
		}
		for _, j := range direct {
			key := "task:" + j.Task.ID
			b.mu.Lock()
			running := b.workers[key]
			if !running {
				b.workers[key] = true
			}
			b.mu.Unlock()
			if !running {
				wg.Add(1)
				go func(j job, key string) {
					defer wg.Done()
					defer func() { b.mu.Lock(); delete(b.workers, key); b.mu.Unlock() }()
					b.runTask(ctx, j)
				}(j, key)
			}
		}
		list, err := b.list(ctx)
		if err == nil {
			for _, a := range list {
				b.mu.Lock()
				running := b.workers[a.ID]
				if !running {
					b.workers[a.ID] = true
				}
				b.mu.Unlock()
				if !running {
					wg.Add(1)
					go func(id string) { defer wg.Done(); b.runAssistant(ctx, id) }(a.ID)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (b *backend) runAssistant(ctx context.Context, id string) {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := b.resolve(ctx, id); errors.Is(err, agentlink.ErrNotFound) {
			return
		}
		var raw string
		err := b.db.QueryRowContext(ctx, "SELECT data FROM agent_adapter_jobs WHERE assistant_id=? AND json_extract(data,'$.task.state')='queued' AND NOT EXISTS(SELECT 1 FROM agent_adapter_jobs fenced WHERE fenced.assistant_id=? AND json_extract(fenced.data,'$.cleanup_uncertain')=1) ORDER BY sequence LIMIT 1", id, id).Scan(&raw)
		if err == nil {
			var j job
			if json.Unmarshal([]byte(raw), &j) == nil {
				b.execute(ctx, j)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
func (b *backend) execute(ctx context.Context, selected job) {
	a, err := b.resolve(ctx, selected.Task.Receiver.Address.AgentID)
	if err != nil {
		return
	}
	b.executeSession(ctx, selected, Session{AssistantID: a.ID, JobID: selected.Task.ID, Binary: b.binary, Workspace: a.Workspace, Mode: a.Mode, ThreadID: a.ThreadID, Worker: b.worker, Model: a.Model, Effort: a.Effort}, selected.Request.Text, func(thread string) error {
		_, err := b.db.ExecContext(context.Background(), "UPDATE agent_adapter_assistants SET thread_id=? WHERE id=? AND (thread_id='' OR thread_id=?)", thread, a.ID, thread)
		return err
	})
}

func (b *backend) executeSession(ctx context.Context, selected job, session Session, text string, bind func(string) error) {
	var launch bool
	err := b.update(ctx, selected.Task.ID, func(j *job) error {
		if j.Task.State == "queued" {
			j.Task.State = "running"
			launch = true
		}
		return nil
	})
	if err != nil || !launch {
		return
	}
	turnCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.active[selected.Task.ID] = cancel
	b.mu.Unlock()
	defer func() { cancel(); b.mu.Lock(); delete(b.active, selected.Task.ID); b.mu.Unlock() }()
	// Cancellation may commit after queue claim but before active registration.
	if current, e := b.read(ctx, selected.Request.From, selected.Task.ID); e != nil || current.Task.State == "cancelling" {
		cancel()
	}
	session.Adapter = b.runtime().Kind
	session.HostRoot = b.root
	result := runNativeTurn(turnCtx, session, text, turnEvents{
		Thread: bind,
		Started: func(turn string) error {
			return b.update(context.Background(), selected.Task.ID, func(j *job) error { j.TurnID = turn; return nil })
		},
		Review: func(ctx context.Context, method string, params json.RawMessage) (string, error) {
			if selected.Message != nil {
				return "deny", nil
			}
			return b.awaitReview(ctx, selected, method, params)
		},
	})
	_ = b.update(context.Background(), selected.Task.ID, func(j *job) error {
		j.Task.State = result.Status
		j.Task.Result = result.Text
		j.Task.Error = result.Error
		j.ThreadID = result.ThreadID
		j.TurnID = result.TurnID
		j.Files = result.Files
		j.CleanupUncertain = result.CleanupUncertain
		return nil
	})
}

// receiveMessages admits each durable message to its assistant queue. A
// message ID is its immutable request identity, including after a host restart.
func (b *backend) receiveMessages(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, "SELECT envelope FROM agent_messages WHERE direction='incoming' AND state='queued' ORDER BY sequence LIMIT 32")
	if err != nil {
		return err
	}
	var messages []protocol.Envelope
	for rows.Next() {
		var raw string
		var e protocol.Envelope
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			rows.Close()
			return err
		}
		messages = append(messages, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range messages {
		if e.To.NodeID != b.nodeID {
			continue
		}
		request := "mail:" + e.ID
		if len(request) > 128 {
			sum := sha256.Sum256([]byte(e.ID))
			request = "mail:" + hex.EncodeToString(sum[:])
		}
		_, err = b.submitMessage(ctx, agentlink.TaskRequest{RequestID: request, From: e.From, Target: e.To, Text: e.Text}, e)
		if err != nil {
			return err
		}
		if _, err = b.db.ExecContext(ctx, "UPDATE agent_messages SET state='read' WHERE id=? AND state='queued'", e.ID); err != nil {
			return err
		}
	}
	return nil
}

// Terminal messages publish one immutable reply to their original sender.
func messageReply(j job) *protocol.Envelope {
	parent := j.Message
	if parent == nil || parent.From.Kind == "user" && j.Task.State != "completed" {
		return nil
	}
	hops, err := protocol.NextMailHop(parent.Hops)
	if err != nil {
		return nil
	}
	text := j.Task.Result
	if j.Task.State != "completed" {
		// Diagnostic identifiers are protocol data. Never present partial model
		// output alone as an unsuccessful reply. Bound JSON escaping for mail.
		text = encoded(map[string]string{"state": j.Task.State, "error": j.Task.Error, "partial_output": trimBytes(text, maxResult/2)})
	}
	if text == "" {
		return nil
	}
	return &protocol.Envelope{ID: "agent-reply-" + j.Task.ID, Kind: "message", From: j.Task.Receiver.Address, To: parent.From, ConversationID: parent.ConversationID, ReplyTo: &parent.ID, Hops: hops, Text: trimBytes(text, 65536)}
}

// publishMessages commits a reply and marks processing in one transaction.
func (b *backend) publishMessages(ctx context.Context) error {
	rows, err := b.db.QueryContext(ctx, `SELECT j.data FROM agent_adapter_jobs j JOIN agent_messages m ON m.id=json_extract(j.data,'$.message.id') WHERE m.direction='incoming' AND m.state='read' AND json_extract(j.data,'$.task.state') IN ('completed','failed','cancelled') ORDER BY j.sequence LIMIT 32`)
	if err != nil {
		return err
	}
	var jobs []job
	for rows.Next() {
		var raw string
		var j job
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &j); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.Message == nil {
			continue
		}
		err = storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
			if reply := messageReply(j); reply != nil {
				if err := inbox.EnqueueTx(ctx, tx, *reply, "outgoing", "queued"); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state='processed',detail=? WHERE id=? AND direction='incoming'", j.Task.Error, j.Message.ID)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func (b *backend) runMessages(ctx context.Context) {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = b.receiveMessages(ctx)
		_ = b.publishMessages(ctx)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
