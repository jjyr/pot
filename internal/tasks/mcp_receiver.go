package tasks

import (
	"context"
	"database/sql"
	"time"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// ReadReceiverTx uses the existing Task record in the receiver journal's
// transaction. It does not import another node's Task as a local execution.
func (s *Service) ReadReceiverTx(ctx context.Context, tx *sql.Tx, assistant, task string) (*Record, error) {
	return read(ctx, tx, assistant, task)
}

// ContinueReceiverTx admits terminal input with the old-run comparison and
// journal mapping in one transaction. Receiver ownership must already have been
// checked against the original durable delivery. A remote actor remains the
// input's actual sender; it does not become a local user or approval authority.
func (s *Service) ContinueReceiverTx(ctx context.Context, tx *sql.Tx, assistant, task, oldRun, request, text string, from protocol.AgentRef, proof *Run) (Record, error) {
	var result Record
	if !from.Valid() || validateInput(request, text) != nil {
		return result, agentlink.ErrInvalid
	}
	r, e := read(ctx, tx, assistant, task)
	if e != nil {
		return result, e
	}
	if r == nil {
		return result, agentlink.ErrNotFound
	}
	if r.Execution != nil || !r.local(s.options.NodeID) || r.RunID != oldRun {
		return result, agentlink.ErrFenced
	}
	if r.Info.Phase.Scope() == "active" || r.Outcome == nil {
		return result, agentlink.ErrFenced
	}
	if r.Launch != nil {
		if proof == nil || !proof.Phase.Ended() || !proof.CleanupConfirmed || proof.Launch.ControlID != s.options.NodeID || proof.Launch.TaskID != task || proof.Launch.RunID != oldRun {
			return result, core.ErrCleanupUnconfirmed
		}
		digest, e := r.Launch.Digest()
		if e != nil || digest != proof.LaunchDigest {
			return result, agentlink.ErrFenced
		}
	}

	digest, e := inputDigest(currentInputDigestFormat, text, "input", from, nil)
	if e != nil {
		return result, e
	}
	messageID, _ := core.HashJSON([]string{task, request})
	envelope := protocol.Envelope{ID: messageID, Kind: "input", Owner: assistant, RequestID: request, Digest: digest, From: from, To: protocol.AgentRef{NodeID: s.options.NodeID, Kind: "task", AgentID: task}, ConversationID: oldRun, Text: text}
	if e = inbox.EnqueueTx(ctx, tx, envelope, "in", "consumed"); e != nil {
		return result, e
	}
	if e = s.beginContinuationTx(ctx, tx, r, request, text); e != nil {
		return result, e
	}
	r.Info.UpdatedAtMS = time.Now().UnixMilli()
	if e = write(ctx, tx, r); e != nil {
		return result, e
	}
	return *r, nil
}

// ReceiverContinuationProof reads the original execution backend. The immutable
// launch digest and old run are checked again in ContinueReceiverTx's writer tx.
func (s *Service) ReceiverContinuationProof(ctx context.Context, assistant, task, run string) (*Run, error) {
	r, e := s.Get(ctx, assistant, task)
	if e != nil {
		return nil, e
	}
	if r == nil || r.RunID != run {
		return nil, agentlink.ErrFenced
	}
	if r.Launch == nil {
		return nil, nil
	}
	if s.options.Executor == nil {
		return nil, core.ErrCleanupUnconfirmed
	}
	proof, e := s.options.Executor.Inspect(ctx, s.options.NodeID, task, run)
	if e != nil {
		return nil, e
	}
	if proof == nil || !proof.Phase.Ended() || !proof.CleanupConfirmed {
		return nil, core.ErrCleanupUnconfirmed
	}
	return proof, nil
}
