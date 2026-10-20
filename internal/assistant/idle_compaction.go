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
	Delay  time.Duration
	Source string
}

const compactPrefix = "pot-compact-"

var ErrCompactDeferred = errors.New("compaction deferred for pending input or results")

func isCompactRequest(id string) bool { return strings.HasPrefix(id, compactPrefix) }
func idleRoute(spec core.ResolvedSpec) string {
	key, _ := core.HashJSON([]string{spec.Provider, spec.Model, spec.GatewayProtocol, spec.AuthRef})
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
	var request, route, attempt string
	var finished int64
	if err := c.store.DB().QueryRowContext(ctx, "SELECT request_id,finished_at,route,attempt_for FROM assistant_idle_compaction WHERE singleton=1").Scan(&request, &finished, &route, &attempt); err != nil {
		return err
	}
	if config, err := c.configurationLocked(ctx); err != nil {
		return err
	} else if config.PendingSpec != nil {
		return nil
	}
	if request == "" || request == attempt || route != idleRoute(c.thread.Config) {
		return nil
	}
	delay := 15 * time.Minute
	if c.options.IdlePolicy != nil {
		policy, err := c.options.IdlePolicy(ctx, *c.thread)
		delay = policy.Delay
		if err != nil {
			return err
		}
	}
	if delay <= 0 || now.UnixMilli() < finished+delay.Milliseconds() {
		return nil
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
		var finished int64
		var route string
		if err := c.store.DB().QueryRowContext(ctx, "SELECT finished_at,route FROM assistant_idle_compaction WHERE singleton=1").Scan(&finished, &route); err != nil {
			return err
		}
		delay := 15 * time.Minute
		if c.options.IdlePolicy != nil {
			policy, err := c.options.IdlePolicy(ctx, *c.thread)
			if err != nil {
				return err
			}
			delay = policy.Delay
		}
		if delay <= 0 || time.Now().UnixMilli() < finished+delay.Milliseconds() || route != idleRoute(c.thread.Config) {
			return ErrCompactDeferred
		}
		if config, err := c.configurationLocked(ctx); err != nil {
			return err
		} else if config.PendingSpec != nil {
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

func (c *Conversation) idleScheduleLocked(ctx context.Context) json.RawMessage {
	var finished int64
	_ = c.store.DB().QueryRowContext(ctx, "SELECT finished_at FROM assistant_idle_compaction WHERE singleton=1").Scan(&finished)
	delay := 15 * time.Minute
	source := "default"
	if c.options.IdlePolicy != nil {
		if value, err := c.options.IdlePolicy(ctx, *c.thread); err == nil {
			delay = value.Delay
			source = value.Source
		}
	}
	body, _ := json.Marshal(map[string]any{"idle_minutes": delay.Minutes(), "source": source, "anchor_ms": finished, "deadline_ms": finished + delay.Milliseconds(), "started_ms": storage.Now(), "provider_cache_ttl": nil, "provider_cache_source": "unknown"})
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
