package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/storage"
)

// Workspace is destination-local preparation evidence, never a Worker prompt.
type Workspace struct {
	Status      string   `json:"status"`
	Kind        string   `json:"kind"`
	Path        string   `json:"path"`
	DirectoryID string   `json:"directory_id"`
	Source      string   `json:"source,omitempty"`
	Remote      string   `json:"remote,omitempty"`
	Base        string   `json:"base,omitempty"`
	Commit      string   `json:"commit,omitempty"`   // Starting commit, not current HEAD.
	Branch      string   `json:"branch,omitempty"`   // Initial branch, not current branch.
	GitDirs     []string `json:"git_dirs,omitempty"` // Administrative directory, then common directory.
	Error       string   `json:"error,omitempty"`
}

func readWorkspace(ctx context.Context, db querier, control, task, run string) (*Workspace, error) {
	var raw string
	err := db.QueryRowContext(ctx, "SELECT body FROM run_workspaces WHERE control_id=? AND task_id=? AND run_id=?", control, task, run).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var w Workspace
	err = json.Unmarshal([]byte(raw), &w)
	return &w, err
}
func (j *Journal) Workspace(ctx context.Context, control, task, run string) (*Workspace, error) {
	return readWorkspace(ctx, j.db, control, task, run)
}

// BeginWorkspace fences interrupted preparation before any filesystem/Git effects.
func (j *Journal) BeginWorkspace(ctx context.Context, control, task, run string) (w *Workspace, claimed bool, err error) {
	err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		r, e := readRun(ctx, tx, control, task, run)
		if e != nil {
			return e
		}
		if r == nil || r.Phase != Running || r.BootID != j.BootID || r.StopRequested != "" || r.Finalizing {
			return fmt.Errorf("workspace requires current original run")
		}
		w, e = readWorkspace(ctx, tx, control, task, run)
		if e != nil || w != nil {
			return e
		}
		var previous string
		e = tx.QueryRowContext(ctx, "SELECT body FROM run_workspaces WHERE control_id=? AND task_id=? LIMIT 1", control, task).Scan(&previous)
		if e == nil {
			if e = json.Unmarshal([]byte(previous), &w); e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, "INSERT INTO run_workspaces(control_id,task_id,run_id,body)VALUES(?,?,?,?)", control, task, run, previous)
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		w = &Workspace{Status: "preparing"}
		raw, _ := json.Marshal(w)
		_, e = tx.ExecContext(ctx, "INSERT INTO run_workspaces(control_id,task_id,run_id,body)VALUES(?,?,?,?)", control, task, run, string(raw))
		claimed = e == nil
		return e
	})
	return
}
func (j *Journal) SaveWorkspace(ctx context.Context, control, task, run string, w Workspace) error {
	return storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		old, err := readWorkspace(ctx, tx, control, task, run)
		if err != nil {
			return err
		}
		if old == nil || old.Status == "ready" || old.Status == "failed" {
			return fmt.Errorf("workspace preparation is fenced")
		}
		if old.Path != "" && old.Path != w.Path {
			return fmt.Errorf("workspace path rebound")
		}
		raw, err := json.Marshal(w)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE run_workspaces SET body=? WHERE control_id=? AND task_id=? AND run_id=?", string(raw), control, task, run)
		return err
	})
}

// TaskWorkspace addresses the original Task, including ended Tasks and downloads.
func (j *Journal) TaskWorkspace(ctx context.Context, task string) (*Workspace, error) {
	rows, err := j.db.QueryContext(ctx, "SELECT body FROM run_workspaces WHERE task_id=?", task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found *Workspace
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var w Workspace
		if err = json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, err
		}
		if w.Status == "ready" {
			if found != nil && (found.Path != w.Path || found.Commit != w.Commit || found.Branch != w.Branch) {
				return nil, fmt.Errorf("ambiguous original Task workspace")
			}
			found = &w
		}
	}
	return found, rows.Err()
}
