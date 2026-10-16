package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jjyr/pot/pkg/core"
)

// RememberPrepared binds the original authorized request to the guest-prepared
// input before execution. Inspection can then verify the same input without
// calling a guest's prepare or execute methods again.
func (m *Manager) RememberPrepared(ctx context.Context, grant AccessGrant, callID, name string, original, prepared json.RawMessage) error {
	if err := grant.validate(m.runtimeID); err != nil {
		return err
	}
	if callID == "" || len(callID) > 256 || !json.Valid(original) || !json.Valid(prepared) || len(original)+len(prepared) > 8*1024*1024 {
		return errors.New("invalid original prepared input")
	}
	key, err := core.HashJSON([]any{grant.Owner, grant.Turn, strconv.FormatUint(grant.ThreadGeneration, 10), callID})
	if err != nil {
		return err
	}
	input, err := core.HashJSON([]any{grant, name, original})
	if err != nil {
		return err
	}
	digest, err := core.HashJSON([]any{grant.Target, grant.WorkspaceOwner, grant.Worker, grant.Policy, name, prepared})
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, "INSERT OR IGNORE INTO sandbox_prepared_receipts VALUES(?,?,?)", key, input, digest)
	if err != nil {
		return err
	}
	var oldInput, oldDigest string
	if err = m.db.QueryRowContext(ctx, "SELECT input_hash,prepared_hash FROM sandbox_prepared_receipts WHERE key=?", key).Scan(&oldInput, &oldDigest); err != nil {
		return err
	}
	if oldInput != input || oldDigest != digest {
		return errors.New("original prepared operation identity conflict")
	}
	return nil
}

// InspectPreparedReceipt is read-only: absent or uncertain original evidence
// remains unresolved. Neither preparation nor execution is retried.
func (m *Manager) InspectPreparedReceipt(ctx context.Context, grant AccessGrant, callID, name string, original json.RawMessage) (json.RawMessage, error) {
	if err := grant.validate(m.runtimeID); err != nil {
		return nil, err
	}
	key, err := core.HashJSON([]any{grant.Owner, grant.Turn, strconv.FormatUint(grant.ThreadGeneration, 10), callID})
	if err != nil {
		return nil, err
	}
	expected, err := core.HashJSON([]any{grant, name, original})
	if err != nil {
		return nil, err
	}
	var input, prepared, digest, state string
	var result []byte
	if err = m.db.QueryRowContext(ctx, "SELECT p.input_hash,p.prepared_hash,r.digest,r.state,r.result FROM sandbox_prepared_receipts p JOIN sandbox_receipts r USING(key) WHERE p.key=?", key).Scan(&input, &prepared, &digest, &state, &result); err != nil {
		return nil, errors.Join(errors.New("original Sandbox request/result unavailable"), err)
	}
	if input != expected || prepared != digest {
		return nil, errors.New("original Sandbox request binding changed")
	}
	if state != "complete" {
		return nil, core.ErrCleanupUnconfirmed
	}
	if !json.Valid(result) || len(result) > 4*1024*1024 {
		return nil, errors.New("invalid original Sandbox result")
	}
	return json.RawMessage(result), nil
}
