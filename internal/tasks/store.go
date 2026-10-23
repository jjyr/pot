package tasks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"strings"
	"time"
	"unicode/utf8"
)

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func Open(path string, options Options) (*Service, error) {
	if !core.Identifier(options.NodeID) {
		return nil, fmt.Errorf("invalid Task node identity")
	}
	if options.MaxTasks == 0 {
		options.MaxTasks = 1000
	}
	if options.MaxTasks < 1 {
		return nil, fmt.Errorf("invalid task limit")
	}
	db, err := storage.OpenDatabase(path, storage.TasksDatabase)
	if err != nil {
		return nil, err
	}
	s := &Service{db: db, options: options}
	if _, err := db.Exec("INSERT INTO task_node(singleton,node_id) SELECT 1,? WHERE NOT EXISTS(SELECT 1 FROM task_node)", options.NodeID); err != nil {
		db.Close()
		return nil, err
	}
	var savedNode string
	if err := db.QueryRow("SELECT node_id FROM task_node WHERE singleton=1").Scan(&savedNode); err != nil || savedNode != options.NodeID {
		db.Close()
		return nil, fmt.Errorf("Task data belongs to another node")
	}
	if err := s.initializeOutcomes(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.validateStored(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Service) Close() error   { return s.db.Close() }
func (s *Service) DB() *sql.DB    { return s.db }
func (s *Service) NodeID() string { return s.options.NodeID }

type query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func read(ctx context.Context, q query, assistant, id string) (*Record, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT body FROM tasks WHERE assistant_id=? AND id=?", assistant, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Record
	if err := core.DecodeJSON([]byte(raw), &r); err != nil {
		return nil, err
	}
	if r.AssistantID != assistant || r.Info.ID != id {
		return nil, fmt.Errorf("task journal identity mismatch")
	}
	return &r, nil
}
func write(ctx context.Context, tx *sql.Tx, r *Record) error {
	if r.Revision >= core.MaxSafeInteger {
		return fmt.Errorf("task cursor exhausted")
	}
	r.Revision++
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	// A compact terminal transition is committed with the authoritative Task.
	// Same-phase metadata writes cannot repeat an outcome or advance its cursor.
	switch r.Info.Phase {
	case protocol.TaskComplete, protocol.TaskFailed, protocol.TaskInterrupted, protocol.TaskCancelled:
		if r.Outcome != nil && r.Outcome.SuppressionReason != "" {
			break
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO task_outcomes(assistant_id,task_id,title,phase,updated_at_ms,run_id)
 VALUES(?,?,?,?,?,?) ON CONFLICT(assistant_id,task_id,run_id,phase) WHERE run_id!='' DO NOTHING`,
			r.AssistantID, r.Info.ID, r.Info.Title, r.Info.Phase, r.Info.UpdatedAtMS, r.RunID)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO tasks(assistant_id,id,phase,created,body) VALUES(?,?,?,?,?) ON CONFLICT(assistant_id,id) DO UPDATE SET phase=excluded.phase,body=excluded.body", r.AssistantID, r.Info.ID, r.Info.Phase, r.Info.CreatedAtMS, string(raw))
	return err
}
func (s *Service) validateStored(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT assistant_id,id,body FROM tasks")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var assistant, id, raw string
		if err := rows.Scan(&assistant, &id, &raw); err != nil {
			return err
		}
		var r Record
		if err := core.DecodeJSON([]byte(raw), &r); err != nil {
			return err
		}
		if r.SchemaVersion != 13 || r.AssistantID != assistant || r.Info.AssistantID != assistant || r.Info.ID != id || r.Info.RunID != r.RunID || !core.Identifier(assistant) || !core.Identifier(id) {
			return fmt.Errorf("invalid stored Task ownership/schema")
		}
		if err := r.Origin.Validate(); err != nil {
			return err
		}
		if r.Execution != nil {
			if r.Info.AgentLink == nil || !r.Execution.Request.From.Valid() || r.Launch != nil {
				return fmt.Errorf("invalid Agent Link Task binding")
			}
			continue
		}
		if r.Config == nil {
			return fmt.Errorf("local Task has no model snapshot")
		}
		if err := r.Config.Validate(); err != nil {
			return err
		}
		if r.Launch != nil {
			if err := r.Launch.Validate(r.Launch.RuntimeID); err != nil {
				return err
			}
			if r.Launch.ControlID != s.options.NodeID || r.Launch.AssistantID != assistant || r.Launch.TaskID != id || r.Launch.RunID != r.RunID || r.Info.ExecutionTarget == nil || *r.Info.ExecutionTarget != r.Launch.ExecutionTarget {
				return fmt.Errorf("stored Task launch binding mismatch")
			}
		}
	}
	return rows.Err()
}
func (s *Service) Get(ctx context.Context, assistant, id string) (*Record, error) {
	return read(ctx, s.db, assistant, id)
}
func (s *Service) GetTx(ctx context.Context, tx *sql.Tx, assistant, id string) (*Record, error) {
	return read(ctx, tx, assistant, id)
}

func (s *Service) Prepare(ctx context.Context, assistant string, request protocol.CreateTask) (Selection, error) {
	if request.Target != nil && *request.Target != (protocol.AgentRef{NodeID: s.options.NodeID, Kind: "assistant", AgentID: assistant}) {
		return Selection{}, fmt.Errorf("explicit executor must use Agent Link submission")
	}
	if !core.Identifier(assistant) {
		return Selection{}, fmt.Errorf("invalid assistant identity")
	}
	if err := request.Validate(); err != nil {
		return Selection{}, err
	}
	if s.options.ResolveModel == nil {
		return Selection{}, fmt.Errorf("Control model selection unavailable")
	}
	selection, err := s.options.ResolveModel(ctx, assistant, request)
	if err != nil {
		return Selection{}, err
	}
	if err := selection.Config.Validate(); err != nil {
		return Selection{}, err
	}
	if len(selection.Config.Params.Agent.Plugins) > 0 {
		return Selection{}, fmt.Errorf("Task selection must separate plugin bundles from model snapshot")
	}
	if selection.Plugins == nil {
		selection.Plugins = []string{}
	}
	return selection, nil
}

// AcceptLocal is called only by the local Pot receiving adapter. Caller-side
// submissions enter through Agent Link, regardless of execution location.
func (s *Service) AcceptLocal(ctx context.Context, assistant string, request protocol.CreateTask) (Record, error) {
	hash, err := core.HashJSON([]any{request, protocol.TaskOrigin{Kind: "manual"}})
	if err != nil {
		return Record{}, err
	}
	var existingID, digest string
	err = s.db.QueryRowContext(ctx, "SELECT task_id,digest FROM task_writes WHERE assistant_id=? AND request_id=?", assistant, request.RequestID).Scan(&existingID, &digest)
	if err == nil {
		if digest != hash {
			return Record{}, fmt.Errorf("changed content under Task request identity")
		}
		record, err := s.Get(ctx, assistant, existingID)
		if err != nil {
			return Record{}, err
		}
		if record == nil {
			return Record{}, fmt.Errorf("Task receipt refers to missing record")
		}
		return *record, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, err
	}
	selection, err := s.Prepare(ctx, assistant, request)
	if err != nil {
		return Record{}, err
	}
	var record Record
	err = storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		var err error
		record, err = s.EnqueueTx(ctx, tx, assistant, request, selection, protocol.TaskOrigin{Kind: "manual"}, time.Now().UnixMilli())
		return err
	})
	if err == nil {
		s.Notify()
	}
	return record, err
}

// EnqueueTx atomically joins a schedule occurrence with its Task and request
// receipt. Selection must be resolved by the owning Control before opening tx.
func (s *Service) EnqueueTx(ctx context.Context, tx *sql.Tx, assistant string, request protocol.CreateTask, selection Selection, origin protocol.TaskOrigin, at int64) (Record, error) {
	if s.options.OwnerAvailable != nil {
		if err := s.options.OwnerAvailable(ctx, assistant); err != nil {
			return Record{}, err
		}
	}
	if !core.Identifier(assistant) {
		return Record{}, fmt.Errorf("invalid assistant")
	}
	if err := request.Validate(); err != nil {
		return Record{}, err
	}
	if err := origin.Validate(); err != nil {
		return Record{}, err
	}
	hash, err := core.HashJSON([]any{request, origin})
	if err != nil {
		return Record{}, err
	}

	var oldID, oldHash string
	err = tx.QueryRowContext(ctx, "SELECT task_id,digest FROM task_writes WHERE assistant_id=? AND request_id=?", assistant, request.RequestID).Scan(&oldID, &oldHash)
	if err == nil {
		if oldHash != hash {
			return Record{}, fmt.Errorf("Task request content conflict")
		}
		r, err := read(ctx, tx, assistant, oldID)
		if err != nil {
			return Record{}, err
		}
		if r == nil {
			return Record{}, fmt.Errorf("Task receipt refers to missing record")
		}
		return *r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, err
	}

	if request.Target != nil && *request.Target != (protocol.AgentRef{NodeID: s.options.NodeID, Kind: "assistant", AgentID: assistant}) {
		return Record{}, protocol.APIError{Code: "local_node_required"}
	}

	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE assistant_id=?", assistant).Scan(&count); err != nil {
		return Record{}, err
	}
	if count >= s.options.MaxTasks {
		return Record{}, fmt.Errorf("task total limit reached")
	}
	if err := selection.Config.Validate(); err != nil {
		return Record{}, err
	}
	plugins := append([]string{}, selection.Plugins...)
	id, runID := ID(), ID()
	probe := Launch{ControlID: s.options.NodeID, AssistantID: assistant, RuntimeID: s.options.NodeID, TaskID: id, RunID: runID, Generation: 1, ExecutionTarget: core.RuntimeTarget(s.options.NodeID), Config: selection.Config, Plugins: plugins, Skills: request.Skills, Workspace: request.Workspace, Text: request.Text}
	if err := probe.Validate(s.options.NodeID); err != nil {
		return Record{}, err
	}
	title := strings.TrimSpace(strings.SplitN(request.Text, "\n", 2)[0])
	runes := []rune(title)
	if len(runes) > 80 {
		title = string(runes[:80])
	}
	record := Record{SchemaVersion: 13, NodeID: s.options.NodeID, Origin: origin, AssistantID: assistant, Info: protocol.TaskInfo{Address: &protocol.AgentRef{NodeID: s.options.NodeID, Kind: "task", AgentID: id}, ID: id, AssistantID: assistant, Title: title, Phase: protocol.TaskQueued, CreatedAtMS: at, UpdatedAtMS: at, Routing: &protocol.TaskRouting{Profile: selection.Config.SourceProfileID, Revision: selection.Config.SourceProfileRevision, Provider: selection.Config.Provider, Reason: "assistant selection", SelectionReason: request.SelectionReason}}, Request: request, Config: &selection.Config, Plugins: plugins, RunID: runID, Generation: 1}
	record.Info.MachineID = s.options.NodeID
	record.Info.RunID = runID
	if err := write(ctx, tx, &record); err != nil {
		return Record{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO task_writes(assistant_id,request_id,digest,task_id) VALUES(?,?,?,?)", assistant, request.RequestID, hash, id); err != nil {
		return Record{}, err
	}
	sender := protocol.AgentRef{NodeID: s.options.NodeID, Kind: "assistant", AgentID: assistant}
	inputHash, _ := inputDigest(currentInputDigestFormat, request.Text, "input", sender, nil)
	messageID, _ := core.HashJSON([]string{id, runID})
	message := protocol.Envelope{ID: messageID, Kind: "input", Owner: assistant, RequestID: runID, Digest: inputHash, From: sender, To: protocol.AgentRef{NodeID: s.options.NodeID, Kind: "task", AgentID: id}, ConversationID: runID, Text: request.Text}
	if err := inbox.EnqueueTx(ctx, tx, message, "in", "active"); err != nil {
		return Record{}, err
	}

	return record, nil
}
func (s *Service) change(ctx context.Context, assistant, id string, fn func(*sql.Tx, *Record) error) error {
	changed := false
	var published []hooks.Event
	err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		record, err := read(ctx, tx, assistant, id)
		if err != nil {
			return err
		}
		if record == nil {
			return fmt.Errorf("task missing")
		}
		before, _ := json.Marshal(record)
		wasActive := record.Info.Phase.Scope() == "active"
		started := record.Info.StartedAtMS
		if err := fn(tx, record); err != nil {
			return err
		}
		after, _ := json.Marshal(record)
		if string(before) == string(after) {
			return nil
		}
		changed = true
		record.Info.UpdatedAtMS = time.Now().UnixMilli()
		if started == 0 && record.Info.StartedAtMS > 0 {
			published = append(published, hooks.Started(assistant, id, record.RunID, record.Generation))
		}
		if wasActive && record.Info.Phase.Scope() != "active" {
			if record.Outcome == nil {
				return fmt.Errorf("Task terminal transition requires a finalized outcome")
			}
			event := hooks.Task(assistant, record.Generation, *record.Outcome)
			if err := hooks.EnqueueTx(ctx, tx, s.options.NodeID, s.options.NodeID, event); err != nil {
				return err
			}
			published = append(published, event)
		}
		return write(ctx, tx, record)
	})
	if err == nil && changed {
		s.Notify()
		if s.options.PublishHook != nil {
			for _, event := range published {
				s.options.PublishHook(event)
			}
		}
	}
	return err
}

func (s *Service) Notify() {
	s.changes.Notify("tasks")
	// Read only the indexed outcome head after commit, never the Task archive.
	s.outcomeMu.Lock()
	var cursor uint64
	err := s.db.QueryRow("SELECT COALESCE(MAX(sequence),0) FROM task_outcomes").Scan(&cursor)
	changed := err == nil && cursor > s.outcomeCursor
	if changed {
		s.outcomeCursor = cursor
	}
	s.outcomeMu.Unlock()
	if changed && s.options.OutcomesChanged != nil {
		s.options.OutcomesChanged()
	}
}
func (s *Service) Subscribe() *core.Subscription { return s.changes.Subscribe() }
func validateInput(requestID, text string) error {
	if requestID == "" || len(requestID) > 128 || strings.TrimSpace(text) == "" || len(text) > 65536 || !utf8.ValidString(text) {
		return fmt.Errorf("invalid Task input")
	}
	return nil
}

// LocalMachine accepts only this node. Remote work must use Agent Link.
func (s *Service) LocalMachine(_ context.Context, machine string) (string, error) {
	if machine != "" && machine != s.options.NodeID {
		return "", protocol.APIError{Code: "remote_execution_removed"}
	}
	return s.options.NodeID, nil
}

// RetireOwner serializes identity retirement with Task admission, including a
// schedule whose model selection completed before the owner was deleted.
func (s *Service) RetireOwner(ctx context.Context, owner string, commit func() error) error {
	return storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		var busy bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM tasks WHERE assistant_id=? AND phase IN ('queued','waiting','running','waiting_approval'))", owner).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return protocol.APIError{Code: "assistant_busy"}
		}
		return commit()
	})
}
