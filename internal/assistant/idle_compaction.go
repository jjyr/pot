package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"strings"
	"time"
)

type IdleCompactionPolicy struct {
	CacheTTL time.Duration
	Source   string
}

const compactPrefix = "pot-compact-"

var ErrCompactDeferred = errors.New("compaction deferred for pending input or results")

func isCompactRequest(id string) bool { return strings.HasPrefix(id, compactPrefix) }
func idleRoute(spec core.ResolvedSpec) string {
	key, _ := core.HashJSON([]any{spec.Provider, spec.Model, spec.GatewayProtocol, spec.AuthRef, spec.Params.ReasoningEffort})
	return key
}

func (c *Conversation) idleLoop() {
	defer close(c.idleDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			_ = c.CheckIdleCompaction(c.ctx, time.Now())
		}
	}
}

// CheckIdleCompaction is also a deterministic clock boundary for hosts/tests.
// An unfinished background Task does not participate in this decision.
func (c *Conversation) CheckIdleCompaction(ctx context.Context, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.blocked || c.turn != nil || c.thread == nil || !c.thread.Config.Params.Agent.AutoCompact {
		return nil
	}
	if eligible, err := c.idleEligibleLocked(ctx, now); err != nil || !eligible {
		return err
	}
	var pending int
	if err := c.store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chat_user_inbox").Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		return err
	}
	var checkpoint agent.Checkpoint
	if err = core.DecodeJSON(raw, &checkpoint); err != nil {
		return err
	}
	if checkpoint.Started != nil || checkpoint.ModelRequestID != nil || len(checkpoint.Pending) > 0 {
		return nil
	}
	turn := protocol.SendMessage{RequestID: compactPrefix + storage.ID(), ConversationID: c.options.ConversationID}
	enqueue := func() error {
		c.turn = &savedTurn{Owner: c.options.Owner, BootID: c.options.BootID, Generation: c.thread.Generation, Request: turn, UserMessageID: turn.RequestID, State: "queued"}
		if err = c.appendTurnLocked(ctx); err != nil {
			c.turn = nil
			return err
		}
		c.input <- turn
		return nil
	}
	if c.options.CompactGate != nil {
		err = c.options.CompactGate(ctx, enqueue)
		if errors.Is(err, ErrCompactDeferred) {
			return nil
		}
		return err
	}
	return enqueue()
}

// Run under the lane and conversation mutex. Control's gate holds the canonical
// Task/mailbox transaction until this start receipt is durable, closing the
// completed-result race across the two databases.
func (c *Conversation) startIdleLocked(ctx context.Context) error {
	start := func() error {
		if eligible, err := c.idleEligibleLocked(ctx, time.Now()); err != nil {
			return err
		} else if !eligible {
			return ErrCompactDeferred
		}
		var pending int
		if err := c.store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chat_user_inbox").Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return ErrCompactDeferred
		}
		// Mark the history attempted first: a crash can skip maintenance, but
		// must never submit the same history twice. Recovery abandons this turn.
		if _, err := c.store.DB().ExecContext(ctx, "UPDATE assistant_idle_compaction SET attempt_for=request_id WHERE singleton=1"); err != nil {
			return err
		}
		c.turn.State = "running"
		return c.appendTurnLocked(ctx)
	}
	if c.options.CompactGate != nil {
		return c.options.CompactGate(ctx, start)
	}
	return start()
}

// Compaction cannot dispatch tools. An interrupted sample can therefore be
// abandoned without the ordinary uncertain-tool recovery workflow.
func (c *Conversation) abandonIdleLocked(ctx context.Context) error {
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		return err
	}
	if len(raw) > 0 {
		var checkpoint agent.Checkpoint
		if err = core.DecodeJSON(raw, &checkpoint); err != nil {
			return err
		}
		if checkpoint.Started != nil || len(checkpoint.Pending) > 0 {
			return agent.ErrUncertainEffects
		}
		checkpoint.ModelRequestID = nil
		body, err := json.Marshal(checkpoint)
		if err != nil {
			return err
		}
		if err = c.store.SaveContext(ctx, body); err != nil {
			return err
		}
	}
	if err = c.finishCompactionMessagesLocked(ctx, "failed"); err != nil {
		return err
	}
	c.turn = nil
	return c.appendTurnLocked(ctx)
}

func (c *Conversation) idlePolicyLocked(ctx context.Context) (IdleCompactionPolicy, error) {
	if c.options.IdlePolicy != nil {
		return c.options.IdlePolicy(ctx, *c.thread)
	}
	return IdleCompactionPolicy{CacheTTL: 15 * time.Minute, Source: "default"}, nil
}

// TTL estimates a reuse window. Arm at 90% from request start, and never
// launch after expiry. Missing/zero cache evidence closes this opportunity.
func (c *Conversation) idleEligibleLocked(ctx context.Context, now time.Time) (bool, error) {
	var request, route, attempt string
	var started int64
	var active bool
	if err := c.store.DB().QueryRowContext(ctx, "SELECT request_id,started_at,route,attempt_for,COALESCE(cache_read,0)>0 OR COALESCE(cache_write,0)>0 FROM assistant_idle_compaction WHERE singleton=1").Scan(&request, &started, &route, &attempt, &active); err != nil {
		return false, err
	}
	if request == "" || request == attempt || started <= 0 || route != idleRoute(c.thread.Config) {
		return false, nil
	}
	if config, err := c.configurationLocked(ctx); err != nil {
		return false, err
	} else if config.PendingSpec != nil {
		return false, nil
	}
	policy, err := c.idlePolicyLocked(ctx)
	if err != nil {
		return false, err
	}
	ttl := policy.CacheTTL.Milliseconds()
	if ttl <= 0 || now.UnixMilli() < started+ttl*9/10 {
		return false, nil
	}
	if !active || now.UnixMilli() >= started+ttl {
		// Only a new successful ordinary response can arm another opportunity.
		_, err = c.store.DB().ExecContext(ctx, "UPDATE assistant_idle_compaction SET attempt_for=request_id WHERE singleton=1")
		return false, err
	}
	return true, nil
}

func (c *Conversation) idleScheduleLocked(ctx context.Context) json.RawMessage {
	var started int64
	var read, write *int64
	_ = c.store.DB().QueryRowContext(ctx, "SELECT started_at,cache_read,cache_write FROM assistant_idle_compaction WHERE singleton=1").Scan(&started, &read, &write)
	policy, _ := c.idlePolicyLocked(ctx)
	ttl := policy.CacheTTL.Milliseconds()
	body, _ := json.Marshal(map[string]any{"cache_ttl_minutes": policy.CacheTTL.Minutes(), "source": policy.Source, "anchor_ms": started, "due_ms": started + ttl*9/10, "expires_ms": started + ttl, "started_ms": storage.Now(), "cache_read_tokens": read, "cache_write_tokens": write})
	return body
}

func (c *Conversation) finishCompactionMessagesLocked(ctx context.Context, state string) error {
	for _, message := range c.messages {
		if message.Compaction == nil || message.Compaction.State != "running" {
			continue
		}
		value := *message.Compaction
		value.State = state
		value.FinishedAtMS = storage.Now()
		message.Compaction = &value
		message.Status = protocol.Failed
		if err := c.putMessageLocked(ctx, message, nil); err != nil {
			return err
		}
	}

	return nil
}
