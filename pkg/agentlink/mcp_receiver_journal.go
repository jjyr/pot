package agentlink

import (
	"context"
	"github.com/jjyr/pot/pkg/protocol"
)

type MCPReceiverRecord struct {
	SchemaVersion         int                 `json:"schema_version"`
	Claim                 MCPClaim            `json:"claim"`
	Work                  *MCPWorkDelivery    `json:"work,omitempty"`
	Control               *MCPControlDelivery `json:"control,omitempty"`
	Ack                   MCPDeliveryAck      `json:"ack"`
	Acknowledged          bool                `json:"acknowledged"`
	Admission             *MCPRunAdmission    `json:"admission,omitempty"`
	AdmissionAcknowledged bool                `json:"admission_acknowledged"`
	ControlApplied        bool                `json:"control_applied"`
	InputReceipt          *MCPInputReceipt    `json:"input_receipt,omitempty"`
	InputAcknowledged     bool                `json:"input_acknowledged"`
	Sequence              uint64              `json:"sequence"`
	LastPhase             protocol.TaskPhase  `json:"last_phase,omitempty"`
	Outbox                *MCPExecutionEvent  `json:"outbox,omitempty"`
	Receipt               *MCPEventReceipt    `json:"receipt,omitempty"`
	Revision              int64               `json:"-"`
}

type MCPReceiverDelivery struct {
	Claim   *MCPClaim           `json:"claim,omitempty"`
	Work    *MCPWorkDelivery    `json:"work,omitempty"`
	Control *MCPControlDelivery `json:"control,omitempty"`
}

// MCPReceiverReporter is a transport boundary, not an execution engine.
type MCPReceiverReporter interface {
	Ack(context.Context, MCPDeliveryAck) error
	Admit(context.Context, MCPRunAdmission) error
	InputReceipt(context.Context, MCPInputReceipt) error
	Event(context.Context, MCPExecutionEvent) (MCPEventReceipt, error)
}

// MCPReceiverBackend adapts an existing backend's atomic admission, native
// cleanup fences and journal CAS. It must never launch work on network retries.
type MCPReceiverBackend interface {
	Save(context.Context, *MCPReceiverRecord) error
	Admit(context.Context, *MCPReceiverRecord) error
	Snapshot(context.Context, *MCPReceiverRecord) error
	Approval(context.Context, *MCPReceiverRecord) error
	BeforeSnapshot(context.Context, *MCPReceiverRecord) error
}

// AdvanceMCPReceiver reconciles durable receipts in order. Both native Hosts and
// Pot receivers use it; only the existing business backend can create a run.
func AdvanceMCPReceiver(ctx context.Context, remote MCPReceiverReporter, backend MCPReceiverBackend, r *MCPReceiverRecord) error {
	if !r.Acknowledged {
		if err := remote.Ack(ctx, r.Ack); err != nil {
			return err
		}
		r.Acknowledged = true
		if err := backend.Save(ctx, r); err != nil {
			return err
		}
	}
	if !r.Ack.Accepted {
		return nil
	}
	if r.Control != nil && r.Control.Approval != nil {
		if !r.ControlApplied {
			return backend.Approval(ctx, r)
		}
		return nil
	}
	if r.Admission == nil && !r.ControlApplied {
		if err := backend.Admit(ctx, r); err != nil {
			return err
		}
	}
	if r.Admission == nil {
		if r.InputReceipt != nil && !r.InputAcknowledged {
			if err := remote.InputReceipt(ctx, *r.InputReceipt); err != nil {
				return err
			}
			r.InputAcknowledged = true
			return backend.Save(ctx, r)
		}
		return nil
	}
	if !r.AdmissionAcknowledged {
		if err := remote.Admit(ctx, *r.Admission); err != nil {
			return err
		}
		r.AdmissionAcknowledged = true
		if err := backend.Save(ctx, r); err != nil {
			return err
		}
	}
	if r.Control != nil && r.Control.Input != nil && !r.InputAcknowledged {
		if r.InputReceipt == nil {
			r.InputReceipt = &MCPInputReceipt{Claim: r.Claim, Binding: r.Admission.Binding, State: "consumed"}
			if err := backend.Save(ctx, r); err != nil {
				return err
			}
		}
		if err := remote.InputReceipt(ctx, *r.InputReceipt); err != nil {
			return err
		}
		r.InputAcknowledged = true
		if err := backend.Save(ctx, r); err != nil {
			return err
		}
	}
	if err := backend.BeforeSnapshot(ctx, r); err != nil {
		return err
	}
	if err := backend.Snapshot(ctx, r); err != nil {
		return err
	}
	if r.Outbox == nil {
		return nil
	}
	receipt, err := remote.Event(ctx, *r.Outbox)
	if err != nil {
		return err
	}
	if _, err = receipt.Retry(*r.Outbox); err != nil {
		return err
	}
	r.Receipt = &receipt
	r.Sequence = r.Outbox.Sequence
	r.LastPhase = r.Outbox.Phase
	r.Outbox = nil
	return backend.Save(ctx, r)
}
