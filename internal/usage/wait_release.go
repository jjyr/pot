package usage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jjyr/pot/pkg/storage"
)

type WaitRelease struct {
	Scope    string
	Turn     string
	Since    int64
	Complete bool
}

func readWaitRelease(ctx context.Context, db queryer, owner, id, scope, turn string, since int64) (*WaitRelease, error) {
	var receipt WaitRelease
	err := db.QueryRowContext(ctx, "SELECT scope,turn_id,since,complete FROM wait_release_requests WHERE owner=? AND request_id=?", owner, id).Scan(&receipt.Scope, &receipt.Turn, &receipt.Since, &receipt.Complete)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if receipt.Scope != scope || receipt.Turn != turn || receipt.Since != since {
		return nil, errors.New("allowance resume request identity changed")
	}
	return &receipt, nil
}
func (l *Ledger) WaitRelease(ctx context.Context, owner, id, scope, turn string, since int64) (*WaitRelease, error) {
	return readWaitRelease(ctx, l.db, owner, id, scope, turn, since)
}

// AdmitWaitRelease commits the exact observed wait-instance release and original
// request receipt together. Neither a stale turn nor a replacement wait matches.
func (l *Ledger) AdmitWaitRelease(ctx context.Context, owner, id string, wait Waiting) (*WaitRelease, error) {
	if owner == "" || id == "" || wait.Scope == "" || wait.Turn == "" {
		return nil, errors.New("invalid allowance release identity")
	}
	var receipt *WaitRelease
	err := storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		old, err := readWaitRelease(ctx, tx, owner, id, wait.Scope, wait.Turn, wait.Since)
		if err != nil {
			return err
		}
		if old != nil {
			receipt = old
			return nil
		}
		result, err := tx.ExecContext(ctx, "UPDATE waits SET released=1 WHERE scope=? AND turn_id=? AND at=?", wait.Scope, wait.Turn, wait.Since)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("original allowance wait changed")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO wait_release_requests VALUES(?,?,?,?,?,0)", owner, id, wait.Scope, wait.Turn, wait.Since); err != nil {
			return err
		}
		receipt = &WaitRelease{Scope: wait.Scope, Turn: wait.Turn, Since: wait.Since}
		return nil
	})
	return receipt, err
}
func (l *Ledger) CompleteWaitRelease(ctx context.Context, owner, id string) error {
	_, err := l.db.ExecContext(ctx, "UPDATE wait_release_requests SET complete=1 WHERE owner=? AND request_id=?", owner, id)
	return err
}
