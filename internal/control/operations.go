package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
)

var ErrOperationUnknown = errors.New("operation outcome is unknown; reconcile the original receipt before new effects")

type Operation struct {
	Owner      string               `json:"owner"`
	Turn       string               `json:"turn"`
	Generation uint64               `json:"generation"`
	CallID     string               `json:"call_id"`
	Target     core.ExecutionTarget `json:"target"`
	Tool       string               `json:"tool"`
	Args       json.RawMessage      `json:"args"`
}

func (o Operation) Key() (string, error) {
	if o.Owner == "" || o.Turn == "" || o.Generation == 0 || o.Generation > core.MaxSafeInteger || o.CallID == "" || o.Tool == "" || !json.Valid(o.Args) {
		return "", errors.New("invalid operation identity")
	}
	if err := o.Target.Validate(); err != nil {
		return "", err
	}
	return core.HashJSON([]any{o.Owner, o.Turn, o.Generation, o.CallID})
}

type Receipt struct {
	ID        string          `json:"id"`
	InputHash string          `json:"input_hash"`
	State     string          `json:"state"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}
type Operations struct{ db *sql.DB }

func OpenOperations(path string) (*Operations, error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS operations(id TEXT PRIMARY KEY,input_hash TEXT NOT NULL,state TEXT NOT NULL,result BLOB,error TEXT NOT NULL); CREATE TABLE IF NOT EXISTS operation_scopes(id TEXT PRIMARY KEY REFERENCES operations(id),scope TEXT NOT NULL); UPDATE operations SET state='unknown' WHERE state='started'"); err != nil {
		db.Close()
		return nil, err
	}
	return &Operations{db}, nil
}
func (o *Operations) Close() error { return o.db.Close() }

// Begin durably crosses the effect boundary. A completed duplicate returns its
// original receipt, while an in-flight/restarted request never starts again.
func (o *Operations) Begin(ctx context.Context, op Operation) (receipt Receipt, claimed bool, err error) {
	id, err := op.Key()
	if err != nil {
		return receipt, false, err
	}
	hash, err := core.HashJSON(op)
	if err != nil {
		return receipt, false, err
	}
	err = storage.Transaction(ctx, o.db, func(tx *sql.Tx) error {
		var saved []byte
		err := tx.QueryRowContext(ctx, "SELECT id,input_hash,state,result,error FROM operations WHERE id=?", id).Scan(&receipt.ID, &receipt.InputHash, &receipt.State, &saved, &receipt.Error)
		receipt.Result = json.RawMessage(saved)
		if err == nil {
			if receipt.InputHash != hash {
				return errors.New("operation identity reused with different target, tool or arguments")
			}
			if receipt.State != "complete" {
				return ErrOperationUnknown
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// An unknown operation fences other effects in the same owner/generation until
		// explicit recovery; a new call ID cannot silently bypass an uncertain result.
		var unknown bool
		// Scope is bound into an explicit column-free key prefix table below.
		scope, err := core.HashJSON([]any{op.Owner})
		if err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM operations o JOIN operation_scopes s ON s.id=o.id WHERE s.scope=? AND o.state='unknown')", scope).Scan(&unknown); err != nil {
			return err
		}
		if unknown {
			return ErrOperationUnknown
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO operations VALUES (?,?,'started',NULL,'')", id, hash); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO operation_scopes VALUES (?,?)", id, scope); err != nil {
			return err
		}
		receipt = Receipt{ID: id, InputHash: hash, State: "started"}
		claimed = true
		return nil
	})
	return receipt, claimed, err
}
func (o *Operations) Get(ctx context.Context, id string) (Receipt, error) {
	var r Receipt
	var body []byte
	err := o.db.QueryRowContext(ctx, "SELECT id,input_hash,state,result,error FROM operations WHERE id=?", id).Scan(&r.ID, &r.InputHash, &r.State, &body, &r.Error)
	r.Result = json.RawMessage(body)
	return r, err
}
func (o *Operations) Complete(ctx context.Context, id string, result json.RawMessage, operationErr error) error {
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	if !json.Valid(result) || len(result) > 4*1024*1024 {
		return errors.New("invalid operation receipt result")
	}
	message := ""
	if operationErr != nil {
		message = operationErr.Error()
	}
	return storage.Transaction(ctx, o.db, func(tx *sql.Tx) error {
		var state string
		var old []byte
		var oldError string
		if err := tx.QueryRowContext(ctx, "SELECT state,result,error FROM operations WHERE id=?", id).Scan(&state, &old, &oldError); err != nil {
			return err
		}
		if state == "complete" {
			if string(old) != string(result) || oldError != message {
				return errors.New("operation result already settled differently")
			}
			return nil
		}
		if state != "started" {
			return ErrOperationUnknown
		}
		_, err := tx.ExecContext(ctx, "UPDATE operations SET state='complete',result=?,error=? WHERE id=?", []byte(result), message, id)
		return err
	})
}
func (o *Operations) Unknown(ctx context.Context, id string) error {
	result, err := o.db.ExecContext(ctx, "UPDATE operations SET state='unknown' WHERE id=? AND state='started'", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if n != 1 && err == nil {
		return ErrOperationUnknown
	}
	return err
}
