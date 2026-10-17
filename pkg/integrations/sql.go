package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/storage"
)

// SQLAccounts uses a caller-owned database, which may be shared with other
// components. It owns only its tables; it does not close the database.
type SQLAccounts struct{ db *sql.DB }

func NewSQLAccounts(db *sql.DB) (*SQLAccounts, error) {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS integration_accounts(service TEXT PRIMARY KEY, revision INTEGER NOT NULL, metadata TEXT, secret_ref TEXT NOT NULL)`)
	if err != nil {
		return nil, err
	}
	return &SQLAccounts{db}, nil
}
func (s *SQLAccounts) List(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT metadata,secret_ref FROM integration_accounts WHERE metadata IS NOT NULL ORDER BY service`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Account{}
	for rows.Next() {
		var raw string
		var a Account
		if err = rows.Scan(&raw, &a.SecretRef); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &a); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}
func (s *SQLAccounts) Revision(ctx context.Context, service string) (uint64, error) {
	var revision uint64
	err := s.db.QueryRowContext(ctx, `SELECT revision FROM integration_accounts WHERE service=?`, service).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}
func (s *SQLAccounts) Save(ctx context.Context, a Account, expected uint64) (Account, error) {
	a.Revision = expected + 1
	raw, err := json.Marshal(a)
	if err != nil {
		return Account{}, err
	}
	err = storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		if expected == 0 {
			result, err := tx.ExecContext(ctx, `INSERT INTO integration_accounts(service,revision,metadata,secret_ref) VALUES(?,?,?,?) ON CONFLICT(service) DO NOTHING`, a.Service, a.Revision, string(raw), a.SecretRef)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err == nil && count != 1 {
				return ErrChanged
			}
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE integration_accounts SET revision=?,metadata=?,secret_ref=? WHERE service=? AND revision=?`, a.Revision, string(raw), a.SecretRef, a.Service, expected)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count != 1 {
			return ErrChanged
		}
		return err
	})
	return a, err
}
func (s *SQLAccounts) Delete(ctx context.Context, service string, expected uint64) error {
	// A tombstone fences pending login completion and other service instances.
	result, err := s.db.ExecContext(ctx, `UPDATE integration_accounts SET revision=revision+1,metadata=NULL,secret_ref='' WHERE service=? AND revision=? AND metadata IS NOT NULL`, service, expected)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ErrChanged
	}
	return err
}
