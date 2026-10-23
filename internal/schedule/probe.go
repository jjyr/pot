package schedule

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// ProbeRequest is an immutable occurrence snapshot, never agent history.
type ProbeRequest struct {
	ReconcileOnly bool   `json:"-"`
	Owner         string `json:"owner"`
	ScheduleID    string `json:"schedule_id"`
	OccurrenceID  string `json:"occurrence_id"`
	Trigger       string `json:"trigger"`
	ScheduledAt   string `json:"scheduled_at"`
	Timezone      string `json:"timezone"`
	Plan          Plan   `json:"plan"`
}
type ProbeRunner interface {
	RunProbe(context.Context, ProbeRequest) (protocol.ScheduleHookResult, error)
}

type probeLife struct {
	mu     sync.Mutex
	active map[string]bool
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
}

// ParseProbe accepts one strict JSON object. Missing status and empty stdout run.
// Unknown, duplicate, null or malformed fields cannot accidentally authorize skip.
func ParseProbe(data []byte) (protocol.ScheduleHookResult, error) {
	result := protocol.ScheduleHookResult{Status: "run"}
	if len(data) > 16384 || !utf8.Valid(data) {
		return result, fmt.Errorf("probe stdout exceeds bounds or is not UTF-8")
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return result, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return result, fmt.Errorf("probe response must be an object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return result, err
		}
		key, ok := token.(string)
		if !ok || seen[key] || key != "status" && key != "reason" {
			return result, fmt.Errorf("invalid or duplicate probe field")
		}
		seen[key] = true
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return result, err
		}
		var text string
		if string(value) == "null" || json.Unmarshal(value, &text) != nil {
			return result, fmt.Errorf("probe fields must be strings")
		}
		if key == "status" {
			if text != "run" && text != "skip" {
				return result, fmt.Errorf("invalid probe status")
			}
			result.Status = text
		} else {
			if len(text) > 1000 {
				return result, fmt.Errorf("probe reason exceeds 1000 bytes")
			}
			result.Reason = text
		}
	}
	if _, err = decoder.Token(); err != nil {
		return result, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return result, fmt.Errorf("probe stdout must contain exactly one object")
	}
	return result, nil
}

func (s *Service) reserveProbeTx(ctx context.Context, tx *sql.Tx, owner string, plan Plan, at int64, occurrence, trigger string) error {
	request := ProbeRequest{Owner: owner, ScheduleID: plan.Info.ID, OccurrenceID: occurrence, Trigger: trigger, ScheduledAt: time.UnixMilli(at).UTC().Format(time.RFC3339Nano), Timezone: plan.Info.Spec.Timezone, Plan: plan}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schedule_probes(assistant_id,schedule_id,occurrence_id,body) VALUES(?,?,?,?)", owner, plan.Info.ID, occurrence, string(raw)); err != nil {
		return err
	}
	return writeRun(ctx, tx, owner, plan.Info.ID, protocol.ScheduleRun{ID: occurrence, ScheduledForMS: at, Generation: plan.Generation, State: "hook_pending", Notify: plan.Info.Spec.Notify})
}

// Resume only reuses immutable operation identities. The runner must return an
// existing complete receipt or an uncertainty fence; it must never replay effects.
func (s *Service) resumeProbes(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT p.body FROM schedule_probes p JOIN schedule_runs r ON r.assistant_id=p.assistant_id AND r.schedule_id=p.schedule_id AND r.id=p.occurrence_id WHERE r.finished IS NULL AND json_extract(r.body,'$.state') IN ('hook_pending','hook_running')")
	if err != nil {
		return err
	}
	var requests []ProbeRequest
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var r ProbeRequest
		if err = core.DecodeJSON(raw, &r); err != nil {
			rows.Close()
			return err
		}
		requests = append(requests, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, r := range requests {
		key := r.Owner + ":" + r.OccurrenceID
		s.probes.mu.Lock()
		if s.probes.closed {
			s.probes.mu.Unlock()
			return nil
		}
		if !s.probes.active[key] && len(s.probes.active) < 8 {
			s.probes.active[key] = true
			s.probes.wg.Add(1)
			go func(r ProbeRequest, key string) {
				defer s.probes.wg.Done()
				defer func() {
					s.probes.mu.Lock()
					delete(s.probes.active, key)
					s.probes.mu.Unlock()
					s.options.Tasks.Notify()
				}()
				s.runProbe(s.probes.ctx, r)
			}(r, key)
		}
		s.probes.mu.Unlock()
	}
	return nil
}
func (s *Service) runProbe(ctx context.Context, r ProbeRequest) {
	// Avoid even starting a pending stale script after an edit/pause/delete.
	current, err := s.Get(ctx, r.Owner, r.ScheduleID)
	if err != nil {
		return
	}
	result := protocol.ScheduleHookResult{Status: "error", Diagnostic: "probe executor unavailable"}
	stale := current == nil || current.Generation != r.Plan.Generation || current.Info.Deleted
	reservation, err := readRun(ctx, s.db, r.Owner, r.ScheduleID, r.OccurrenceID)
	if err != nil || reservation == nil || reservation.FinishedAtMS != nil {
		return
	}
	reconcile := stale && reservation.State == "hook_running"
	if stale {
		result = protocol.ScheduleHookResult{Status: "not_started"}
	}
	if !stale || reconcile {
		err = storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
			run, e := readRun(ctx, tx, r.Owner, r.ScheduleID, r.OccurrenceID)
			if e != nil {
				return e
			}
			if run == nil || run.FinishedAtMS != nil || run.State == "hook_blocked" {
				return fmt.Errorf("probe reservation ended")
			}
			run.State = "hook_running"
			return writeRun(ctx, tx, r.Owner, r.ScheduleID, *run)
		})
		if err != nil {
			return
		}
		if reconcile && s.options.Probes == nil {
			err = core.ErrCleanupUnconfirmed
		}
		if s.options.Probes != nil {
			r.ReconcileOnly = reconcile
			result, err = s.options.Probes.RunProbe(ctx, r)
		}
	}
	if core.IsCleanupUnconfirmed(err) {
		result.Status = "blocked"
		result.Diagnostic = "probe cleanup or operation outcome is unconfirmed; original receipt requires reconciliation"
	} else if err != nil {
		result.Status = "error"
		result.Diagnostic = boundedDiagnostic(err.Error())
	}
	if result.Status != "run" && result.Status != "skip" && result.Status != "error" && result.Status != "blocked" && result.Status != "not_started" {
		result = protocol.ScheduleHookResult{Status: "error", Diagnostic: "invalid executor result"}
	}
	// Re-resolve model selection after the hook, with no model request or Task.
	var prepared resolved
	if !stale && result.Status != "skip" && result.Status != "blocked" {
		hash, _ := core.HashJSON([]string{r.Owner, r.OccurrenceID})
		prepared, err = s.resolve(ctx, r.Owner, r.Plan.Info.Spec, "schedule:"+hash)
		if err != nil {
			result.Status = "blocked"
			result.Diagnostic = boundedDiagnostic(err.Error())
		}
	}
	settle, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = storage.Transaction(settle, s.db, func(tx *sql.Tx) error {
		plan, e := readPlan(settle, tx, r.Owner, r.ScheduleID)
		if e != nil {
			return e
		}
		run, e := readRun(settle, tx, r.Owner, r.ScheduleID, r.OccurrenceID)
		if e != nil {
			return e
		}
		if run == nil || run.FinishedAtMS != nil || run.TaskID != nil {
			return nil
		}
		run.Hook = &result
		at := storage.Now()
		// Unknown cleanup fences even a stale generation: a new script must not
		// bypass a possibly-live old process.
		if result.Status == "blocked" {
			run.State = "hook_blocked"
			return writeRun(settle, tx, r.Owner, r.ScheduleID, *run)
		}
		if stale || plan == nil || plan.Generation != r.Plan.Generation || plan.Info.Deleted {
			run.State = "skipped_stale"
			run.FinishedAtMS = &at
			return writeRun(settle, tx, r.Owner, r.ScheduleID, *run)
		}
		if result.Status == "skip" {
			run.State = "skipped_hook"
			run.FinishedAtMS = &at
			run.Summary = result.Reason
			return writeRun(settle, tx, r.Owner, r.ScheduleID, *run)
		}
		task, e := s.options.Tasks.EnqueueTx(settle, tx, r.Owner, prepared.Request, prepared.Selection, protocol.TaskOrigin{Kind: "schedule", ScheduleID: r.ScheduleID, OccurrenceID: r.OccurrenceID}, at)
		if e != nil {
			return e
		}
		run.TaskID = &task.Info.ID
		run.State = "queued"
		return writeRun(settle, tx, r.Owner, r.ScheduleID, *run)
	})
	// Persist failures as a visible, non-retrying fence. A completed operation
	// receipt remains authoritative on restart if this write also fails.
	if err != nil {
		_ = storage.Transaction(settle, s.db, func(tx *sql.Tx) error {
			run, e := readRun(settle, tx, r.Owner, r.ScheduleID, r.OccurrenceID)
			if e != nil || run == nil {
				return e
			}
			if run.TaskID != nil || run.FinishedAtMS != nil {
				return nil
			}
			run.State = "hook_blocked"
			run.Hook = &protocol.ScheduleHookResult{Status: "blocked", Diagnostic: boundedDiagnostic(err.Error())}
			return writeRun(settle, tx, r.Owner, r.ScheduleID, *run)
		})
	}
	s.options.Tasks.Notify()
}
func boundedDiagnostic(s string) string {
	if len(s) > 2000 {
		s = s[:2000]
	}
	return strings.ToValidUTF8(s, "")
}
func (s *Service) Close() error {
	s.probes.mu.Lock()
	s.probes.closed = true
	s.probes.cancel()
	s.probes.mu.Unlock()
	s.probes.wg.Wait()
	return nil
}
