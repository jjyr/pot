package tasks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/storage"
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
	if !core.Identifier(options.ControlID) || !core.Identifier(options.RuntimeID) {
		return nil, fmt.Errorf("invalid Task Control identity")
	}
	if options.MaxTasks == 0 {
		options.MaxTasks = 1000
	}
	if options.MaxTasks < 1 {
		return nil, fmt.Errorf("invalid task limit")
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	s := &Service{db: db, options: options}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS task_schema(version INTEGER NOT NULL);INSERT INTO task_schema SELECT 4 WHERE NOT EXISTS(SELECT 1 FROM task_schema);CREATE TABLE IF NOT EXISTS tasks(assistant_id TEXT NOT NULL,id TEXT NOT NULL,phase TEXT NOT NULL,created INTEGER NOT NULL,body TEXT NOT NULL,PRIMARY KEY(assistant_id,id));CREATE TABLE IF NOT EXISTS task_writes(assistant_id TEXT NOT NULL,request_id TEXT NOT NULL,digest TEXT NOT NULL,task_id TEXT NOT NULL,PRIMARY KEY(assistant_id,request_id));CREATE TABLE IF NOT EXISTS task_inputs(sequence INTEGER PRIMARY KEY AUTOINCREMENT,assistant_id TEXT NOT NULL,task_id TEXT NOT NULL,request_id TEXT NOT NULL,digest TEXT NOT NULL,kind TEXT NOT NULL,sender TEXT NOT NULL,text TEXT NOT NULL,state TEXT NOT NULL,UNIQUE(assistant_id,task_id,request_id));CREATE TABLE IF NOT EXISTS task_notifications(id TEXT PRIMARY KEY,assistant_id TEXT NOT NULL,task_id TEXT NOT NULL,phase TEXT NOT NULL,text TEXT NOT NULL);`); err != nil {
		db.Close()
		return nil, err
	}
	var version int
	if err := db.QueryRow("SELECT version FROM task_schema").Scan(&version); err != nil || version != 4 {
		db.Close()
		return nil, fmt.Errorf("unsupported task schema")
	}
	if err := s.validateStored(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS task_authority(control_id TEXT NOT NULL,runtime_id TEXT NOT NULL);INSERT INTO task_authority SELECT ?,? WHERE NOT EXISTS(SELECT 1 FROM task_authority)", options.ControlID, options.RuntimeID); err != nil {
		db.Close()
		return nil, err
	}
	var savedControl, savedRuntime string
	if err := db.QueryRow("SELECT control_id,runtime_id FROM task_authority").Scan(&savedControl, &savedRuntime); err != nil || savedControl != options.ControlID || savedRuntime != options.RuntimeID {
		db.Close()
		return nil, fmt.Errorf("Task data belongs to another Control/Runtime")
	}
	return s, nil
}
func (s *Service) Close() error      { return s.db.Close() }
func (s *Service) DB() *sql.DB       { return s.db }
func (s *Service) ControlID() string { return s.options.ControlID }
func (s *Service) RuntimeID() string { return s.options.RuntimeID }

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
		if r.SchemaVersion != 4 || r.AssistantID != assistant || r.Info.AssistantID != assistant || r.Info.ID != id || !core.Identifier(assistant) || !core.Identifier(id) {
			return fmt.Errorf("invalid stored Task ownership/schema")
		}
		if err := r.Config.Validate(); err != nil {
			return err
		}
		if r.Launch != nil {
			if err := r.Launch.Validate(r.Launch.RuntimeID); err != nil {
				return err
			}
			if r.Launch.ControlID != s.options.ControlID || r.Launch.AssistantID != assistant || r.Launch.TaskID != id || r.Launch.RunID != r.RunID || r.Info.ExecutionTarget == nil || *r.Info.ExecutionTarget != r.Launch.ExecutionTarget {
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

// FailResultTx records a failed mandatory scheduled report without reopening work.
func (s *Service) FailResultTx(ctx context.Context, tx *sql.Tx, assistant, id, detail string) error {
	record, err := read(ctx, tx, assistant, id)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("scheduled Task missing")
	}
	if record.Info.Phase != protocol.TaskComplete {
		return nil
	}
	record.Info.Phase = protocol.TaskFailed
	record.Error = detail
	return write(ctx, tx, record)
}
func (s *Service) Prepare(ctx context.Context, assistant string, request protocol.CreateTask) (Selection, error) {
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
	if selection.PluginDigests == nil {
		selection.PluginDigests = map[string]string{}
	}
	return selection, nil
}
func (s *Service) Create(ctx context.Context, assistant string, request protocol.CreateTask) (Record, error) {
	if request.MachineID == "" {
		request.MachineID = s.options.RuntimeID
	}
	hash, err := core.HashJSON(request)
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
		record, err = s.EnqueueTx(ctx, tx, assistant, request, selection, time.Now().UnixMilli())
		return err
	})
	if err == nil {
		s.Notify()
	}
	return record, err
}

// EnqueueTx atomically joins a schedule occurrence with its Task and request
// receipt. Selection must be resolved by the owning Control before opening tx.
func (s *Service) EnqueueTx(ctx context.Context, tx *sql.Tx, assistant string, request protocol.CreateTask, selection Selection, at int64) (Record, error) {
	if !core.Identifier(assistant) {
		return Record{}, fmt.Errorf("invalid assistant")
	}
	if err := request.Validate(); err != nil {
		return Record{}, err
	}
	if request.MachineID == "" {
		request.MachineID = s.options.RuntimeID
	}
	hash, err := core.HashJSON(request)
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
	digests := map[string]string{}
	for k, v := range selection.PluginDigests {
		digests[k] = v
	}
	id, runID := ID(), ID()
	probe := runner.Launch{ControlID: s.options.ControlID, AssistantID: assistant, RuntimeID: request.MachineID, TaskID: id, RunID: runID, Generation: 1, ExecutionTarget: core.HostTarget(request.MachineID), Config: selection.Config, Plugins: plugins, Skills: request.Skills, ResourceRevision: selection.ResourceRevision, PluginDigests: digests, Text: request.Text}
	if err := probe.Validate(request.MachineID); err != nil {
		return Record{}, err
	}
	title := strings.TrimSpace(strings.SplitN(request.Text, "\n", 2)[0])
	runes := []rune(title)
	if len(runes) > 80 {
		title = string(runes[:80])
	}
	record := Record{SchemaVersion: 4, AssistantID: assistant, Info: protocol.TaskInfo{ID: id, AssistantID: assistant, Title: title, Phase: protocol.TaskQueued, CreatedAtMS: at, UpdatedAtMS: at, Resource: request.Resource, Routing: &protocol.TaskRouting{Profile: selection.Config.SourceProfileID, Revision: selection.Config.SourceProfileRevision, Provider: selection.Config.Provider, Reason: "assistant selection", SelectionReason: request.SelectionReason}}, Request: request, Config: selection.Config, Plugins: plugins, ResourceRevision: selection.ResourceRevision, PluginDigests: digests, RunID: runID, Generation: 1}
	record.Info.MachineID = request.MachineID
	if err := write(ctx, tx, &record); err != nil {
		return Record{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO task_writes(assistant_id,request_id,digest,task_id) VALUES(?,?,?,?)", assistant, request.RequestID, hash, id); err != nil {
		return Record{}, err
	}
	inputHash, _ := core.HashJSON([]string{request.Text, "input", assistant})
	if _, err := tx.ExecContext(ctx, "INSERT INTO task_inputs(assistant_id,task_id,request_id,digest,kind,sender,text,state) VALUES(?,?,?,?,?,?,?,'active')", assistant, id, runID, inputHash, "input", assistant, request.Text); err != nil {
		return Record{}, err
	}
	return record, nil
}
func (s *Service) change(ctx context.Context, assistant, id string, fn func(*sql.Tx, *Record) error) error {
	changed := false
	err := storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		record, err := read(ctx, tx, assistant, id)
		if err != nil {
			return err
		}
		if record == nil {
			return fmt.Errorf("task missing")
		}
		before, _ := json.Marshal(record)
		if err := fn(tx, record); err != nil {
			return err
		}
		after, _ := json.Marshal(record)
		if string(before) == string(after) {
			return nil
		}
		changed = true
		record.Info.UpdatedAtMS = time.Now().UnixMilli()
		return write(ctx, tx, record)
	})
	if err == nil && changed {
		s.Notify()
	}
	return err
}

func (s *Service) Notify()                       { s.changes.Notify("tasks") }
func (s *Service) Subscribe() *core.Subscription { return s.changes.Subscribe() }
func validateInput(requestID, text string) error {
	if requestID == "" || len(requestID) > 128 || strings.TrimSpace(text) == "" || len(text) > 65536 || !utf8.ValidString(text) {
		return fmt.Errorf("invalid Task input")
	}
	return nil
}
