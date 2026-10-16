package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
)

// PreparedResources returns the destination's immutable evidence for one original
// run. A missing value means resource synchronization has not completed yet.
func (j *Journal) PreparedResources(ctx context.Context, control, task, run string) (json.RawMessage, string, error) {
	return readResources(ctx, j.db, control, task, run)
}
func readResources(ctx context.Context, q querier, control, task, run string) (json.RawMessage, string, error) {
	var body, revision string
	err := q.QueryRowContext(ctx, "SELECT body,digest FROM run_resources WHERE control_id=? AND task_id=? AND run_id=?", control, task, run).Scan(&body, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	hash, err := core.HashJSON(json.RawMessage(body))
	if err != nil || hash != revision {
		return nil, "", fmt.Errorf("prepared resource evidence checksum mismatch")
	}
	return json.RawMessage(body), revision, nil
}

// PinResources fixes the first successfully synchronized resources for this run.
// Retries and allowance resumes reuse that value rather than resampling Control.
func (j *Journal) PinResources(ctx context.Context, control, task, run string, body json.RawMessage) (raw json.RawMessage, revision string, err error) {
	if !json.Valid(body) || len(body) > 24*1024*1024 {
		return nil, "", fmt.Errorf("invalid prepared resource evidence")
	}
	canonical, err := core.CanonicalJSON(body)
	if err != nil {
		return nil, "", err
	}
	hash, err := core.HashJSON(json.RawMessage(canonical))
	if err != nil {
		return nil, "", err
	}
	err = storage.Transaction(ctx, j.db, func(tx *sql.Tx) error {
		value, err := readRun(ctx, tx, control, task, run)
		if err != nil {
			return err
		}
		if value == nil || value.Phase != Running || value.BootID != j.BootID || value.StopRequested != "" || value.Finalizing {
			return fmt.Errorf("resource preparation requires current original run")
		}
		raw, revision, err = readResources(ctx, tx, control, task, run)
		if err != nil {
			return err
		}
		if raw != nil {
			return nil
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO run_resources(control_id,task_id,run_id,digest,body)VALUES(?,?,?,?,?)", control, task, run, hash, string(canonical))
		if err != nil {
			return err
		}
		raw, revision = json.RawMessage(canonical), hash
		return nil
	})
	return
}
