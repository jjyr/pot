package tasks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jjyr/pot/pkg/rollout"
	"github.com/jjyr/pot/pkg/storage"
)

// A rollout shares the durable database's lifetime, never the worktree or cache.
func taskRolloutDirectory(db *sql.DB) (string, error) {
	rows, err := db.Query("PRAGMA database_list")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var main string
	for rows.Next() {
		var sequence int
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			return "", err
		}
		if name == "main" {
			main = path
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if main == "" {
		return "", fmt.Errorf("task rollouts require a file-backed database")
	}
	return filepath.Join(filepath.Dir(main), "sessions", "tasks"), nil
}

func (j *Journal) rolloutPath(value Run) string {
	identity, _ := json.Marshal([]string{value.Launch.ControlID, value.Launch.TaskID})
	hash := sha256.Sum256(identity)
	return filepath.Join(j.rolloutDirectory, hex.EncodeToString(hash[:])+".jsonl")
}

func (j *Journal) hydrateRun(value *Run) error {
	if value == nil || value.RolloutRef == nil {
		return nil
	}
	if len(value.Context) != 0 {
		return fmt.Errorf("task run has both inline and rollout context")
	}
	raw, err := rollout.Load(j.rolloutPath(*value), *value.RolloutRef)
	if err != nil {
		return fmt.Errorf("load task rollout: %w", err)
	}
	if err := storage.CheckContext(raw); err != nil {
		return err
	}
	value.Context = raw
	return nil
}

// The caller holds the database write transaction. Durably append before moving
// its committed reference; a rolled-back SQL write leaves only an uncommitted
// tail, which Append reconciles against the prior reference on the next write.
func readTaskHead(ctx context.Context, q querier, control, task string) (rollout.Ref, error) {
	var ref rollout.Ref
	err := q.QueryRowContext(ctx, "SELECT sequence,hash FROM task_sessions WHERE control_id=? AND task_id=?", control, task).Scan(&ref.Sequence, &ref.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return rollout.Ref{}, nil
	}
	return ref, err
}
func ensureTaskSession(ctx context.Context, tx *sql.Tx, value Run) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO task_sessions(control_id,task_id) VALUES(?,?) ON CONFLICT DO NOTHING", value.Launch.ControlID, value.Launch.TaskID)
	return err
}
func (j *Journal) persistRun(ctx context.Context, tx *sql.Tx, value *Run) error {
	return j.persistRunCheckpoint(ctx, tx, value, false)
}
func (j *Journal) persistRunCheckpoint(ctx context.Context, tx *sql.Tx, value *Run, importing bool) error {
	if err := ensureTaskSession(ctx, tx, *value); err != nil {
		return err
	}
	if len(value.Context) != 0 {
		if err := storage.CheckContext(value.Context); err != nil {
			return err
		}
		if !importing {
			var latest uint64
			if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(CAST(json_extract(body,'$.sequence') AS INTEGER)),0) FROM task_runs WHERE control_id=? AND task_id=?", value.Launch.ControlID, value.Launch.TaskID).Scan(&latest); err != nil {
				return err
			}
			if value.Sequence < latest {
				return fmt.Errorf("stale task run cannot write session")
			}
		}
		committed, err := readTaskHead(ctx, tx, value.Launch.ControlID, value.Launch.TaskID)
		if err != nil {
			return err
		}
		ref, err := rollout.Append(j.rolloutPath(*value), committed, value.Context)
		if err != nil {
			return fmt.Errorf("append task rollout: %w", err)
		}
		value.RolloutRef = &ref
		if _, err = tx.ExecContext(ctx, "UPDATE task_sessions SET sequence=?,hash=? WHERE control_id=? AND task_id=?", ref.Sequence, ref.Hash, value.Launch.ControlID, value.Launch.TaskID); err != nil {
			return err
		}
	}
	activity := time.Now().UnixMilli()
	if importing {
		activity = value.FinishedAtMS
		if value.StartedAtMS > activity {
			activity = value.StartedAtMS
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE task_sessions SET activity_at=MAX(activity_at,?) WHERE control_id=? AND task_id=?", activity, value.Launch.ControlID, value.Launch.TaskID); err != nil {
		return err
	}
	return writeRun(ctx, tx, *value)
}

// CompressRolloutBatch examines at most 32 rows. The cursor is a rowid, so
// ineligible/hot rows cannot cause an unbounded scan or starve later sessions.
// limit bounds successful compressions independently of the 32-row scan budget.
// An empty next cursor wraps the next pass to the beginning.
func (j *Journal) CompressRolloutBatch(ctx context.Context, before time.Time, cursor string, limit int, bytesPerSecond int64) (next string, compressed int, err error) {
	if limit <= 0 {
		return cursor, 0, nil
	}
	if limit > 32 {
		limit = 32
	}
	var after int64
	if cursor != "" {
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 0 {
			return cursor, 0, fmt.Errorf("invalid task rollout cursor")
		}
	}
	rows, err := j.db.QueryContext(ctx, "SELECT rowid,control_id,task_id,sequence,hash FROM task_sessions WHERE rowid>? ORDER BY rowid LIMIT ?", after, 32)
	if err != nil {
		return cursor, 0, err
	}
	type candidate struct {
		rowid int64
		run   Run
	}
	candidates := make([]candidate, 0, 32)
	for rows.Next() {
		var item candidate
		item.run.RolloutRef = &rollout.Ref{}
		if err = rows.Scan(&item.rowid, &item.run.Launch.ControlID, &item.run.Launch.TaskID, &item.run.RolloutRef.Sequence, &item.run.RolloutRef.Hash); err != nil {
			rows.Close()
			return cursor, 0, err
		}
		candidates = append(candidates, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return cursor, 0, err
	}
	if err = rows.Close(); err != nil {
		return cursor, 0, err
	}
	next = cursor
	for _, item := range candidates {
		if err = ctx.Err(); err != nil {
			return next, compressed, err
		}
		// Advance past a failed entry as well, so a background caller can make
		// progress and report the error without starving the remaining sessions.
		next = strconv.FormatInt(item.rowid, 10)
		run := item.run
		cold, checkErr := coldTaskSession(ctx, j.db, run, before)
		if checkErr != nil {
			return next, compressed, checkErr
		}
		if !cold {
			continue
		}
		var stage *rollout.Compression
		stage, err = rollout.PrepareCompression(ctx, j.rolloutPath(run), *run.RolloutRef, bytesPerSecond)
		if errors.Is(err, rollout.ErrNotEligible) || errors.Is(err, rollout.ErrChanged) {
			continue
		}
		if err != nil {
			return next, compressed, err
		}
		committed := false
		commitErr := func() error {
			defer stage.Abort()
			return storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
				current, e := readTaskHead(ctx, tx, run.Launch.ControlID, run.Launch.TaskID)
				if e != nil {
					return e
				}
				cold, e := coldTaskSession(ctx, tx, run, before)
				if e != nil {
					return e
				}
				if !cold || current != *run.RolloutRef {
					return rollout.ErrChanged
				}
				if e = stage.Commit(ctx); e != nil {
					return e
				}
				committed = true
				return nil
			})
		}()
		if committed {
			compressed++
		}
		if errors.Is(commitErr, rollout.ErrNotEligible) || errors.Is(commitErr, rollout.ErrChanged) {
			continue
		}
		if commitErr != nil {
			return next, compressed, commitErr
		}
		if compressed >= limit {
			return next, compressed, nil
		}
	}
	if len(candidates) < 32 {
		next = ""
	}
	return next, compressed, nil
}

func coldTaskSession(ctx context.Context, q querier, run Run, before time.Time) (bool, error) {
	if run.RolloutRef == nil || run.RolloutRef.IsZero() {
		return false, nil
	}
	var cold bool
	err := q.QueryRowContext(ctx, `SELECT activity_at < ? AND NOT EXISTS (
 SELECT 1 FROM task_runs r WHERE r.control_id=s.control_id AND r.task_id=s.task_id AND
 (r.phase NOT IN ('complete','failed','cancelled','interrupted') OR COALESCE(json_extract(r.body,'$.cleanup_confirmed'),0)=0 OR COALESCE(json_extract(r.body,'$.finished_at_ms'),0)=0 OR json_extract(r.body,'$.finished_at_ms')>=?))
 FROM task_sessions s WHERE control_id=? AND task_id=?`, before.UnixMilli(), before.UnixMilli(), run.Launch.ControlID, run.Launch.TaskID).Scan(&cold)
	return cold, err
}

// CompressColdRollouts is an explicit, bounded maintenance pass. Background
// callers use CompressRolloutBatch and carry its cursor between passes.
func (j *Journal) CompressColdRollouts(ctx context.Context, before time.Time) error {
	_, _, err := j.CompressRolloutBatch(ctx, before, "", 32, 0)
	return err
}

// SQL import commits its references before reclaiming freed inline checkpoint
// pages. Leaving the flag set until VACUUM succeeds makes interruption retryable.
func (j *Journal) reclaimMigratedRollouts(ctx context.Context) error {
	var pending bool
	if err := j.db.QueryRowContext(ctx, "SELECT reclaim_pending FROM task_rollout_migration WHERE singleton=1").Scan(&pending); err != nil {
		return fmt.Errorf("read task rollout reclamation: %w", err)
	}
	if !pending {
		return nil
	}
	if _, err := j.db.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("reclaim migrated task checkpoints: %w", err)
	}
	if _, err := j.db.ExecContext(ctx, "UPDATE task_rollout_migration SET reclaim_pending=0 WHERE singleton=1"); err != nil {
		return fmt.Errorf("complete task rollout reclamation: %w", err)
	}
	return nil
}
