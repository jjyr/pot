package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jjyr/pot/pkg/rollout"
)

func contextRef(ctx context.Context, tx *sql.Tx) (ref rollout.Ref, err error) {
	var imported bool
	err = tx.QueryRowContext(ctx, "SELECT sequence,hash,imported FROM context_rollout WHERE singleton=1").Scan(&ref.Sequence, &ref.Hash, &imported)
	if err == nil && !imported {
		err = errors.New("context rollout import incomplete")
	}
	return
}

// Import under the database writer lock. File data is synced before the SQL
// cursor commits; a retry discards only the uncommitted file tail. Legacy rows
// stay authoritative until that commit. Never load the entire legacy history.
func (s *Store) migrateContextRollout(ctx context.Context) error {
	err := Transaction(ctx, s.db, func(tx *sql.Tx) error {
		var imported bool
		if err := tx.QueryRowContext(ctx, "SELECT imported FROM context_rollout WHERE singleton=1").Scan(&imported); err != nil {
			return err
		}
		if imported {
			return nil
		}
		var current string
		err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='thread.context'").Scan(&current)
		hasCurrent := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if hasCurrent {
			if err := CheckContext(json.RawMessage(current)); err != nil {
				return err
			}
		}
		rows, err := tx.QueryContext(ctx, "SELECT classification,body,at FROM rollout_legacy_context ORDER BY sequence")
		if err != nil {
			return err
		}
		defer rows.Close()
		sawLegacy, final := false, false
		ref, err := rollout.AppendManyAt(s.rolloutPath, rollout.Ref{}, func() (json.RawMessage, int64, error) {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			if rows.Next() {
				var classification string
				var body []byte
				var at int64
				if err := rows.Scan(&classification, &body, &at); err != nil {
					return nil, 0, err
				}
				if classification != "context" {
					return nil, 0, fmt.Errorf("unknown legacy context classification %q", classification)
				}
				if !hasCurrent {
					return nil, 0, errors.New("legacy context history has no current checkpoint")
				}
				if err := CheckContext(body); err != nil {
					return nil, 0, err
				}
				sawLegacy = true
				return body, at, nil
			}
			if err := rows.Err(); err != nil {
				return nil, 0, err
			}
			if hasCurrent && !final {
				final = true
				return json.RawMessage(current), time.Now().UnixMilli(), nil
			}
			return nil, 0, io.EOF
		})
		if err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE context_rollout SET sequence=?,hash=?,imported=1,reclaim_pending=? WHERE singleton=1", ref.Sequence, ref.Hash, sawLegacy); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM rollout_legacy_context"); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM metadata WHERE key='thread.context'")
		return err
	})
	if err != nil {
		return err
	}
	// The export removed potentially gigabytes of duplicate snapshots. Reclaim
	// only after the durable import commit, outside a migration transaction.
	// The persisted flag makes interruption before VACUUM safely retryable.
	var reclaim bool
	if err = s.db.QueryRowContext(ctx, "SELECT reclaim_pending FROM context_rollout WHERE singleton=1").Scan(&reclaim); err != nil {
		return err
	}
	if reclaim {
		if _, err = s.db.ExecContext(ctx, "VACUUM"); err != nil {
			return fmt.Errorf("reclaim migrated context history: %w", err)
		}
		_, err = s.db.ExecContext(ctx, "UPDATE context_rollout SET reclaim_pending=0 WHERE singleton=1")
	}
	return err
}

// SaveContextTx binds a synced journal record to the caller's transaction.
// The caller must not publish the new state until the transaction commits.
func (s *Store) SaveContextTx(ctx context.Context, tx *sql.Tx, body json.RawMessage) error {
	if err := CheckContext(body); err != nil {
		return err
	}
	ref, err := contextRef(ctx, tx)
	if err != nil {
		return err
	}
	ref, err = rollout.Append(s.rolloutPath, ref, body)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE context_rollout SET sequence=?,hash=?,activity_at=? WHERE singleton=1", ref.Sequence, ref.Hash, Now())
	return err
}
func (s *Store) EnsureContextTx(ctx context.Context, tx *sql.Tx, body json.RawMessage) error {
	ref, err := contextRef(ctx, tx)
	if err != nil {
		return err
	}
	if !ref.IsZero() {
		_, err = s.LoadContextTx(ctx, tx)
		return err
	}
	return s.SaveContextTx(ctx, tx, body)
}
func (s *Store) SaveContext(ctx context.Context, body json.RawMessage) error {
	return Transaction(ctx, s.db, func(tx *sql.Tx) error { return s.SaveContextTx(ctx, tx, body) })
}
func (s *Store) LoadContextTx(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
	ref, err := contextRef(ctx, tx)
	if err != nil {
		return nil, err
	}
	if ref.IsZero() {
		var started int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM metadata WHERE key='thread'").Scan(&started); err != nil {
			return nil, err
		}
		if started != 0 {
			return nil, errors.New("thread context missing; cannot resume with empty history")
		}
		return nil, nil
	}
	body, err := rollout.Load(s.rolloutPath, ref)
	if err != nil {
		return nil, err
	}
	return body, CheckContext(body)
}
func (s *Store) LoadContext(ctx context.Context) (json.RawMessage, error) {
	var body json.RawMessage
	err := Transaction(ctx, s.db, func(tx *sql.Tx) error { var err error; body, err = s.LoadContextTx(ctx, tx); return err })
	return body, err
}

func touchContextActivity(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE context_rollout SET activity_at=? WHERE singleton=1", Now())
	return err
}
