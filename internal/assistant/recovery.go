package assistant

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

// RecoveryState is an internal service snapshot, never a client-supplied proof.
// Its digest fences a decision against an exact original checkpoint and turn.
type RecoveryState struct {
	RequestID        string
	UserMessageID    string
	Generation       uint64
	BootID           string
	CheckpointHash   string
	Context          json.RawMessage
	Config           core.ResolvedSpec
	CleanupConfirmed bool
}

func (c *Conversation) Recovery(ctx context.Context) (*RecoveryState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("conversation closed")
	}
	if !c.blocked || c.turn != nil || c.recovery == nil {
		return nil, errors.New("conversation has no stopped original recovery turn")
	}
	if c.thread == nil || c.recovery.Owner != c.options.Owner || c.recovery.Generation != c.thread.Generation {
		return nil, errors.New("original recovery generation changed")
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		return nil, err
	}
	hash, err := core.HashJSON(raw)
	if err != nil {
		return nil, err
	}
	proof, exists, err := c.store.Get(ctx, "conversation.cleanup."+c.recovery.Request.RequestID)
	if err != nil {
		return nil, err
	}
	return &RecoveryState{RequestID: c.recovery.Request.RequestID, UserMessageID: c.recovery.UserMessageID, Generation: c.recovery.Generation, BootID: c.recovery.BootID, CheckpointHash: hash, Context: raw, Config: c.thread.Config, CleanupConfirmed: exists && proof == cleanupProof(c.recovery)}, nil
}
func cleanupProof(turn *savedTurn) string {
	raw, _ := json.Marshal([]any{turn.Owner, turn.Request.RequestID, turn.Generation, turn.BootID})
	return string(raw)
}
func (c *Conversation) recordCleanup(ctx context.Context, request string, generation uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turn == nil || c.turn.Request.RequestID != request || c.turn.Generation != generation {
		return errors.New("original cleanup turn changed")
	}
	return c.store.Set(ctx, "conversation.cleanup."+request, cleanupProof(c.turn))
}

// ReconcileRecovery is called only after Control independently verifies original
// service receipts and cleanup. It closes the old turn; it never queues input.
func (c *Conversation) ReconcileRecovery(ctx context.Context, state RecoveryState, values map[string]json.RawMessage, cleanupConfirmed bool) error {
	if !cleanupConfirmed {
		return core.ErrCleanupUnconfirmed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || !c.blocked || c.turn != nil || c.recovery == nil || c.thread == nil || c.recovery.Request.RequestID != state.RequestID || c.recovery.UserMessageID != state.UserMessageID || c.recovery.Generation != state.Generation || c.recovery.BootID != state.BootID || c.thread.Generation != state.Generation || c.thread.Config.SpecHash != state.Config.SpecHash {
		return errors.New("original conversation recovery identity changed")
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		return err
	}
	hash, err := core.HashJSON(raw)
	if err != nil || hash != state.CheckpointHash {
		return errors.New("original conversation checkpoint changed")
	}
	recovered := raw
	if len(raw) > 0 {
		var checkpoint agent.Checkpoint
		if err = core.DecodeJSON(raw, &checkpoint); err != nil {
			return err
		}
		if (checkpoint.Started != nil || len(values) > 0) && checkpoint.LastRequest != state.RequestID {
			return errors.New("checkpoint belongs to a different original turn")
		}
		if checkpoint.ModelRequestID != nil {
			return agent.ErrUncertainEffects
		}
		vision := false
		if catalog := state.Config.Params.Agent.ModelCatalog; catalog != nil {
			descriptor, e := catalog.Resolve(state.Config.Provider, state.Config.Model)
			if e != nil {
				return e
			}
			vision = descriptor.Vision()
		}
		recovered, err = agent.ReconcileContext(raw, values, vision)
		if err != nil {
			return err
		}
		if err = core.DecodeJSON(recovered, &checkpoint); err != nil {
			return err
		}
		if checkpoint.Started != nil {
			return agent.ErrUncertainEffects
		}
		for _, call := range checkpoint.Pending {
			message := modelprotocol.TextMessage("tool", "Not executed because the previous turn was interrupted.")
			message.CallID = call.ID
			checkpoint.Messages = append(checkpoint.Messages, message)
		}
		checkpoint.Pending = []modelprotocol.ToolCall{}
		recovered, err = json.Marshal(checkpoint)
		if err != nil {
			return err
		}
	}
	// Event, checkpoint and admission fence change share one SQLite transaction.
	receipt, _ := json.Marshal(recoveryReceipt{RequestID: state.RequestID, UserMessageID: state.UserMessageID, Generation: state.Generation, BootID: state.BootID})
	metadata := map[string]string{"conversation.turn": "null", "conversation.blocked": "false", "conversation.recovery": "null", "conversation.recovered." + state.RequestID: string(receipt)}
	if len(recovered) > 0 {
		metadata["thread.context"] = string(recovered)
	}
	event, err := c.store.AppendEvent(ctx, protocol.Event{Type: "turn"}, metadata)
	if err != nil {
		return err
	}
	c.blocked = false
	c.recovery = nil
	c.lastError = nil
	c.cursor = *event.Cursor
	c.changedLocked()
	return nil
}

type recoveryReceipt struct {
	RequestID     string `json:"request_id"`
	UserMessageID string `json:"user_message_id"`
	Generation    uint64 `json:"generation"`
	BootID        string `json:"boot_id"`
}

func (c *Conversation) RecoveredState(ctx context.Context, userMessageID string) (*RecoveryState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.messages {
		if m.ID == userMessageID && m.RequestID != nil {
			value, exists, err := c.store.Get(ctx, "conversation.recovered."+*m.RequestID)
			if err != nil || !exists {
				return nil, err
			}
			var receipt recoveryReceipt
			if err = core.DecodeJSON([]byte(value), &receipt); err != nil {
				return nil, err
			}
			if receipt.RequestID != *m.RequestID || receipt.UserMessageID != userMessageID || receipt.Generation == 0 {
				return nil, errors.New("original recovery receipt binding changed")
			}
			return &RecoveryState{RequestID: receipt.RequestID, UserMessageID: receipt.UserMessageID, Generation: receipt.Generation, BootID: receipt.BootID, CleanupConfirmed: true}, nil
		}
	}
	return nil, nil
}
func (c *Conversation) Recovered(ctx context.Context, userMessageID string) (bool, error) {
	state, err := c.RecoveredState(ctx, userMessageID)
	return state != nil, err
}
