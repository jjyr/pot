package agenthost

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jjyr/pot/internal/mcpreceiver"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/storage"
)

type receiverRemote interface {
	Register(context.Context, agentlink.MCPReceiverRegistration) error
	Claim(context.Context, string, int) (receiverDelivery, error)
	Ack(context.Context, agentlink.MCPDeliveryAck) error
	Admit(context.Context, agentlink.MCPRunAdmission) error
	InputReceipt(context.Context, agentlink.MCPInputReceipt) error
	Event(context.Context, agentlink.MCPExecutionEvent) (agentlink.MCPEventReceipt, error)
}

type receiverPump struct {
	epoch    string
	backend  *backend
	control  *sql.DB
	identity agentlink.MCPReceiver
	remote   receiverRemote
}

// step reconciles saved receipts before asking for more work. Calls may be
// repeated after response loss, but the backend is entered only through its
// durable transaction. Neither transport reconnect nor a poll creates a run.
func (p *receiverPump) step(ctx context.Context) error { return mcpreceiver.Step(ctx, p.remote, p) }
func (p *receiverPump) CheckActive(ctx context.Context) error {
	var state string
	if err := p.control.QueryRowContext(ctx, "SELECT state FROM mcp_receiver_connections WHERE id=? AND node_id=?", p.identity.ConnectionID(), p.identity.NodeID()).Scan(&state); err != nil {
		return err
	}
	if state != "active" {
		return agentlink.ErrUnauthorized
	}
	return nil
}
func (p *receiverPump) Records(ctx context.Context) ([]agentlink.MCPReceiverRecord, error) {
	rows, err := p.backend.db.QueryContext(ctx, "SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=? ORDER BY rowid", p.identity.ConnectionID())
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []agentlink.MCPReceiverRecord{}
	for _, id := range ids {
		r, e := readReceiverRecord(ctx, p.backend.db, p.identity.ConnectionID(), id)
		if e != nil {
			return nil, e
		}
		result = append(result, r)
	}
	return result, nil
}
func (p *receiverPump) ClaimRequest(ctx context.Context) (string, error) {
	var request string
	err := storage.Transaction(ctx, p.control, func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, "SELECT state,claim_request_id FROM mcp_receiver_connections WHERE id=? AND node_id=?", p.identity.ConnectionID(), p.identity.NodeID()).Scan(&state, &request); err != nil {
			return err
		}
		if state != "active" {
			return agentlink.ErrUnauthorized
		}
		if request == "" {
			request = identifier()
			_, err := tx.ExecContext(ctx, "UPDATE mcp_receiver_connections SET claim_request_id=?,revision=revision+1 WHERE id=?", request, p.identity.ConnectionID())
			return err
		}
		return nil
	})
	return request, err
}
func (p *receiverPump) ClearClaimRequest(ctx context.Context, request string) error {
	_, err := p.control.ExecContext(ctx, "UPDATE mcp_receiver_connections SET claim_request_id='',revision=revision+1 WHERE id=? AND claim_request_id=?", p.identity.ConnectionID(), request)
	return err
}
func (p *receiverPump) Accept(ctx context.Context, d agentlink.MCPReceiverDelivery) error {
	return p.accept(ctx, d)
}

func (p *receiverPump) advance(ctx context.Context, r *receiverRecord) error {
	return agentlink.AdvanceMCPReceiver(ctx, p.remote, p, r)
}
func (p *receiverPump) Save(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	return p.save(ctx, r)
}
func (p *receiverPump) Admit(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	return p.admit(ctx, r)
}
func (p *receiverPump) Snapshot(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	return p.snapshot(ctx, r)
}
func (p *receiverPump) Approval(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	return p.applyApproval(ctx, r)
}
func (p *receiverPump) BeforeSnapshot(ctx context.Context, r *agentlink.MCPReceiverRecord) error {
	return p.reportApprovals(ctx, r)
}

func receiverPermanent(err error) bool {
	var problem agentlink.MCPError
	if errors.As(err, &problem) {
		return problem.Retry() == agentlink.MCPDoNotRetry
	}
	return errors.Is(err, agentlink.ErrUnauthorized) || errors.Is(err, agentlink.ErrFenced) || errors.Is(err, agentlink.ErrUnsupported) || errors.Is(err, agentlink.ErrConflict) || errors.Is(err, agentlink.ErrInvalid)
}

func receiverPause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
