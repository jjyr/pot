// Package mcpreceiver contains transport and reconciliation shared by Pot and
// native receiver adapters. Task admission and execution belong to their backend.
package mcpreceiver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/agentlink"
)

type Remote interface {
	agentlink.MCPReceiverReporter
	Claim(context.Context, string, int) (agentlink.MCPReceiverDelivery, error)
}

type Journal interface {
	agentlink.MCPReceiverBackend
	CheckActive(context.Context) error
	Records(context.Context) ([]agentlink.MCPReceiverRecord, error)
	ClaimRequest(context.Context) (string, error)
	ClearClaimRequest(context.Context, string) error
	Accept(context.Context, agentlink.MCPReceiverDelivery) error
}

// Step always reconciles saved receipts before polling. A lost claim response
// retains its request ID; neither disconnect nor response loss launches a run.
func Step(ctx context.Context, remote Remote, backend Journal) error {
	if e := backend.CheckActive(ctx); e != nil {
		return e
	}
	records, e := backend.Records(ctx)
	if e != nil {
		return e
	}
	for i := range records {
		if e = agentlink.AdvanceMCPReceiver(ctx, remote, backend, &records[i]); e != nil {
			if errors.Is(e, agentlink.ErrCapacity) {
				continue
			}
			return e
		}
	}
	request, e := backend.ClaimRequest(ctx)
	if e != nil {
		return e
	}
	delivery, e := remote.Claim(ctx, request, 1000)
	if e != nil {
		return e
	}
	if delivery.Claim != nil {
		if e = backend.Accept(ctx, delivery); e != nil {
			return e
		}
	} else if delivery.Work != nil || delivery.Control != nil {
		return agentlink.ErrInvalid
	}
	return backend.ClearClaimRequest(ctx, request)
}

// Record validates an authenticated delivery before its immutable local journal
// insert. The server remains responsible for comparing it to the stored claim.
func Record(identity agentlink.MCPReceiver, d agentlink.MCPReceiverDelivery) (agentlink.MCPReceiverRecord, error) {
	var r agentlink.MCPReceiverRecord
	if d.Claim == nil || (d.Work == nil) == (d.Control == nil) {
		return r, agentlink.ErrInvalid
	}
	c := *d.Claim
	if e := c.ValidateAgainst(identity, c); e != nil {
		return r, e
	}
	if d.Work != nil {
		if e := d.Work.Validate(); e != nil {
			return r, e
		}
		if c.DeliveryID != d.Work.DeliveryID || c.Binding != d.Work.Binding {
			return r, agentlink.ErrFenced
		}
	} else {
		if e := d.Control.Validate(); e != nil {
			return r, e
		}
		if c.DeliveryID != d.Control.DeliveryID || c.Binding != d.Control.Dispatch {
			return r, agentlink.ErrFenced
		}
	}
	r = agentlink.MCPReceiverRecord{SchemaVersion: 2, Claim: c, Work: d.Work, Control: d.Control, Ack: agentlink.MCPDeliveryAck{Claim: c, Accepted: true}, Revision: 1}
	return r, nil
}

type Query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func Read(ctx context.Context, q Query, connection, delivery string) (agentlink.MCPReceiverRecord, error) {
	var r agentlink.MCPReceiverRecord
	var raw string
	var revision int64
	e := q.QueryRowContext(ctx, "SELECT revision,body FROM mcp_receiver_journal WHERE connection_id=? AND delivery_id=?", connection, delivery).Scan(&revision, &raw)
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal([]byte(raw), &r); e != nil {
		return r, e
	}
	r.Revision = revision
	if r.SchemaVersion != 2 || r.Claim.ConnectionID != connection || r.Claim.DeliveryID != delivery {
		return r, agentlink.ErrFenced
	}
	return r, nil
}
func Save(ctx context.Context, tx *sql.Tx, r *agentlink.MCPReceiverRecord) error {
	var task, run any
	if r.Admission != nil {
		task = r.Admission.Binding.Task.AgentID
		run = r.Admission.Binding.RunID
	}
	result, e := tx.ExecContext(ctx, "UPDATE mcp_receiver_journal SET body=?,execution_task=?,execution_run=?,revision=revision+1 WHERE connection_id=? AND delivery_id=? AND revision=?", encode(r), task, run, r.Claim.ConnectionID, r.Claim.DeliveryID, r.Revision)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return agentlink.ErrConflict
	}
	r.Revision++
	return nil
}
