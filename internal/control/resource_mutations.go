package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type ResourceReceipt struct {
	RequestID string          `json:"request_id"`
	State     string          `json:"state"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// OwnerResourceMutation closes admission durably before the owner effect. A
// process failure leaves an explicit uncertain original command; retries never
// reapply an old policy or destructive operation after intervening owner edits.
func (s *Service) OwnerResourceMutation(ctx context.Context, id, scope, digest string, apply func() (any, error)) (json.RawMessage, error) {
	if !core.Identifier(id) || len(digest) != 64 {
		return nil, errors.New("resource mutation requires stable request identity")
	}
	s.resourceMutations.Lock()
	defer s.resourceMutations.Unlock()
	if _, err := s.Tasks.DB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS owner_resource_commands(id TEXT PRIMARY KEY,digest TEXT NOT NULL,scope TEXT NOT NULL,receipt TEXT NOT NULL)"); err != nil {
		return nil, err
	}
	var oldDigest string
	var raw []byte
	err := s.Tasks.DB().QueryRowContext(ctx, "SELECT digest,receipt FROM owner_resource_commands WHERE id=?", id).Scan(&oldDigest, &raw)
	if err == nil {
		if oldDigest != digest {
			return nil, errors.New("resource request identity reused with different content")
		}
		var receipt ResourceReceipt
		if err = core.DecodeJSON(raw, &receipt); err != nil {
			return nil, err
		}
		if receipt.State != "complete" {
			return nil, protocol.APIError{Code: "resource_outcome_unknown", Message: "original resource command requires inspection; it was not replayed"}
		}
		if receipt.Error != "" {
			return nil, errors.New(receipt.Error)
		}
		return receipt.Result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var conflicting bool
	if err = s.Tasks.DB().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM owner_resource_commands WHERE scope=? AND json_extract(receipt,'$.state')='pending')", scope).Scan(&conflicting); err != nil {
		return nil, err
	}
	if conflicting {
		return nil, protocol.APIError{Code: "resource_outcome_unknown", Message: "a prior command for this resource requires inspection before replacement"}
	}
	receipt := ResourceReceipt{RequestID: id, State: "pending"}
	raw, err = json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	if _, err = s.Tasks.DB().ExecContext(ctx, "INSERT INTO owner_resource_commands(id,digest,scope,receipt)VALUES(?,?,?,?)", id, digest, scope, string(raw)); err != nil {
		return nil, err
	}
	value, applyErr := apply()
	receipt.State = "complete"
	if applyErr != nil {
		receipt.Error = applyErr.Error()
		if core.IsCleanupUnconfirmed(applyErr) || errors.Is(applyErr, context.Canceled) || errors.Is(applyErr, context.DeadlineExceeded) {
			receipt.State = "pending"
			applyErr = errors.Join(protocol.APIError{Code: "resource_outcome_unknown", Message: "resource command outcome requires inspection before replacement"}, applyErr)
		}
	} else {
		receipt.Result, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	raw, err = json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	_, err = s.Tasks.DB().ExecContext(context.WithoutCancel(ctx), "UPDATE owner_resource_commands SET receipt=? WHERE id=?", string(raw), id)
	if err != nil {
		return nil, err
	}
	return receipt.Result, applyErr
}
func (s *Service) OwnerResourceReceipts(ctx context.Context) ([]ResourceReceipt, error) {
	if _, err := s.Tasks.DB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS owner_resource_commands(id TEXT PRIMARY KEY,digest TEXT NOT NULL,scope TEXT NOT NULL,receipt TEXT NOT NULL)"); err != nil {
		return nil, err
	}
	rows, err := s.Tasks.DB().QueryContext(ctx, "SELECT receipt FROM owner_resource_commands ORDER BY rowid DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ResourceReceipt{}
	for rows.Next() {
		var raw []byte
		var receipt ResourceReceipt
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = core.DecodeJSON(raw, &receipt); err != nil {
			return nil, err
		}
		result = append(result, receipt)
	}
	return result, rows.Err()
}
