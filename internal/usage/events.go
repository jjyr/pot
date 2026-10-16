package usage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jjyr/pot/pkg/storage"
)

type ToolEvent struct {
	CallID  string
	Name    string
	Action  string
	Content json.RawMessage
	IsError bool
}

func (l *Ledger) Tool(ctx context.Context, scope, turn string, e ToolEvent, atMS int64) error {
	if e.CallID == "" {
		return nil
	}
	key, _ := json.Marshal([]string{scope, turn, e.CallID})
	switch e.Action {
	case "start", "tool_execution_start":
		name := e.Name
		if name == "" {
			name = "tool"
		}
		_, err := l.db.ExecContext(ctx, "INSERT OR IGNORE INTO tools(id,scope,turn_id,name,at) VALUES (?,?,?,?,?)", string(key), scope, turn, name, atMS)
		return err
	case "end", "tool_execution_end":
		content := e.Content
		if len(content) == 0 {
			content = json.RawMessage("null")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, content); err != nil {
			return err
		}
		_, err := l.db.ExecContext(ctx, "UPDATE tools SET end_at=?,bytes=?,failed=? WHERE id=?", atMS, compact.Len(), e.IsError, string(key))
		return err
	default:
		return errors.New("invalid tool usage event")
	}
}

type CompactionEvent struct {
	ID           string
	Action       string
	Reason       string
	TokensBefore *int64
	TokensAfter  *int64
	EntryID      *string
}

func (l *Ledger) Compaction(ctx context.Context, scope, turn string, e CompactionEvent, atMS int64) error {
	if e.ID == "" || len(e.ID) > 128 {
		return errors.New("invalid compaction ID")
	}
	for _, n := range []*int64{e.TokensBefore, e.TokensAfter} {
		if n != nil && (*n < 0 || *n > MaxSafeInteger) {
			return errors.New("invalid context size")
		}
	}
	if e.EntryID != nil && len(*e.EntryID) > 128 {
		return errors.New("invalid context entry ID")
	}
	return storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		switch e.Action {
		case "start":
			if e.Reason != "manual" && e.Reason != "threshold" && e.Reason != "overflow" {
				return errors.New("invalid compaction reason")
			}
			var owner, oldTurn string
			err := tx.QueryRowContext(ctx, "SELECT scope,turn_id FROM compactions WHERE id=?", e.ID).Scan(&owner, &oldTurn)
			if err == nil {
				if owner != scope || oldTurn != turn {
					return errors.New("compaction belongs to another turn")
				}
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE compactions SET state='interrupted',end_at=? WHERE scope=? AND state='running'", atMS, scope); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO compactions(id,scope,turn_id,at,reason,tokens_before) VALUES (?,?,?,?,?,?)", e.ID, scope, turn, atMS, e.Reason, e.TokensBefore)
			return err
		case "complete", "failed", "cancelled":
			result, err := tx.ExecContext(ctx, "UPDATE compactions SET state=?,end_at=COALESCE(end_at,?),tokens_before=COALESCE(?,tokens_before),tokens_after=COALESCE(?,tokens_after),entry_id=COALESCE(?,entry_id) WHERE id=? AND scope=? AND turn_id=? AND (state='running' OR state=?)", e.Action, atMS, e.TokensBefore, e.TokensAfter, e.EntryID, e.ID, scope, turn, e.Action)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return errors.New("unknown or finished compaction")
			}
			return nil
		default:
			return errors.New("invalid compaction event")
		}
	})
}
func (l *Ledger) InterruptCompactions(ctx context.Context, scope, state string, atMS int64) error {
	if state != "interrupted" && state != "cancelled" && state != "failed" {
		return errors.New("invalid compaction interruption")
	}
	_, err := l.db.ExecContext(ctx, "UPDATE compactions SET state=?,end_at=? WHERE scope=? AND state='running'", state, atMS, scope)
	return err
}
func (l *Ledger) ValidateCompaction(ctx context.Context, scope, turn, id string) error {
	var valid bool
	err := l.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM compactions WHERE id=? AND scope=? AND turn_id=? AND state='running')", id, scope, turn).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("unknown or inactive compaction")
	}
	return nil
}
func (l *Ledger) TagCompaction(ctx context.Context, request, operation string) error {
	return storage.Transaction(ctx, l.db, func(tx *sql.Tx) error {
		var valid bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM requests r JOIN compactions c ON c.scope=r.scope AND c.turn_id=r.turn_id WHERE r.id=? AND c.id=? AND c.state='running')", request, operation).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return errors.New("compaction request owner mismatch or inactive operation")
		}
		result, err := tx.ExecContext(ctx, "UPDATE requests SET compaction_id=? WHERE id=? AND (compaction_id IS NULL OR compaction_id=?)", operation, request, operation)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("request already belongs to another compaction")
		}
		return nil
	})
}

type RequestBudget struct {
	Used            uint64 `json:"used"`
	Limit           uint64 `json:"limit"`
	MaxOutputTokens uint64 `json:"max_output_tokens"`
}
type BudgetSettings struct {
	Namespace       string
	Data            string
	Limit           uint64
	MaxOutputTokens uint64
}

func (b BudgetSettings) Status() (RequestBudget, error) {
	if b.Namespace == "" || strings.ContainsAny(b.Namespace, "/\\") || b.Namespace == "." || b.Namespace == ".." {
		return RequestBudget{}, errors.New("invalid request-budget namespace")
	}
	entries, err := os.ReadDir(filepath.Join(b.Data, b.Namespace))
	if os.IsNotExist(err) {
		return RequestBudget{Limit: b.Limit, MaxOutputTokens: b.MaxOutputTokens}, nil
	}
	if err != nil {
		return RequestBudget{}, err
	}
	var used uint64
	for _, e := range entries {
		if _, err := strconv.ParseUint(e.Name(), 10, 64); err == nil {
			used++
		}
	}
	return RequestBudget{used, b.Limit, b.MaxOutputTokens}, nil
}

// ReserveIn persists lifetime/per-turn request counts independently of token
// allowance windows. Reservations are never deleted or automatically retried.
func ReserveIn(directory string, limit uint64) (uint64, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return 0, err
	}
	for n := uint64(1); n <= limit; n++ {
		file, err := os.OpenFile(filepath.Join(directory, strconv.FormatUint(n, 10)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		_, err = file.WriteString("reserved\n")
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return 0, err
		}
		if closeErr != nil {
			return 0, closeErr
		}
		dir, err := os.Open(directory)
		if err != nil {
			return 0, err
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return 0, err
		}
		return n, nil
	}
	return 0, errors.New("model request budget exhausted")
}
