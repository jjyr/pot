package agent

import (
	"context"
	"encoding/json"
	"os"

	"github.com/jjyr/pot/pkg/agentlink"
)

type pendingReview struct {
	value    agentlink.ExecutionApproval
	job      job
	done     chan string
	ctx      context.Context
	resolved bool
}

func (b *backend) awaitReview(ctx context.Context, j job, method string, params json.RawMessage) (string, error) {
	if len(params) > maxBody {
		return "", agentlink.ErrInvalid
	}
	if j.Message != nil || j.Request.Executor != "codex" || j.Request.From.Kind != "assistant" {
		return "", agentlink.ErrUnauthorized
	}
	p := &pendingReview{value: agentlink.ExecutionApproval{ID: identifier(), JobID: j.Task.ID, Method: method, Params: append(json.RawMessage(nil), params...)}, job: j, done: make(chan string, 1), ctx: ctx}
	b.mu.Lock()
	if len(b.reviews) >= 32 {
		b.mu.Unlock()
		return "", agentlink.ErrCapacity
	}
	b.reviews[p.value.ID] = p
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.reviews, p.value.ID)
		remaining := false
		for _, other := range b.reviews {
			if !other.resolved && other.job.Task.ID == j.Task.ID {
				remaining = true
			}
		}
		b.mu.Unlock()
		if !remaining {
			_ = b.update(context.Background(), j.Task.ID, func(current *job) error {
				if current.Task.State == "waiting_approval" {
					current.Task.State = "running"
				}
				return nil
			})
		}
	}()
	if err := b.update(ctx, j.Task.ID, func(current *job) error {
		if current.Task.State == "cancelling" {
			return context.Canceled
		}
		current.Task.State = "waiting_approval"
		return nil
	}); err != nil {
		return "", err
	}
	select {
	case action := <-p.done:
		// Decision consumes just this request; no session or policy grant is sent.
		return action, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b *backend) Review(ctx context.Context, r agentlink.ReviewRequest) (agentlink.ReviewState, error) {
	host, _ := os.Hostname()
	result := agentlink.ReviewState{NodeID: b.nodeID, Host: host, Pending: []agentlink.ExecutionApproval{}}
	if r.From.Kind != "user" || !r.From.Valid() || r.Caller.NodeID != r.From.NodeID || !r.Caller.Valid() || (r.Caller.Kind != "assistant" && r.Caller.Kind != "task" && r.Caller.Kind != "user") {
		return result, agentlink.ErrUnauthorized
	}
	if r.Caller.Kind != "assistant" {
		return result, agentlink.ErrUnauthorized
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	result.CanDecide = true
	if r.Action != "read" {
		if !result.CanDecide {
			return result, agentlink.ErrUnauthorized
		}
		if r.Action != "allow_once" && r.Action != "deny" {
			return result, agentlink.ErrInvalid
		}
		p := b.reviews[r.ID]
		if p == nil || p.resolved || p.ctx.Err() != nil || p.job.Message != nil || p.job.Request.Executor != "codex" || p.job.Request.From != r.Caller {
			return result, agentlink.ErrNotFound
		}
		current, err := b.read(ctx, r.Caller, p.job.Task.ID)
		if err != nil || current.Task.State != "waiting_approval" {
			return result, agentlink.ErrConflict
		}
		// Mark consumed while retaining other concurrent requests for this job.
		p.resolved = true
		p.done <- r.Action
		return result, nil
	}
	for _, p := range b.reviews {
		if !p.resolved && p.ctx.Err() == nil && p.job.Message == nil && p.job.Request.Executor == "codex" && p.job.Request.From == r.Caller {
			result.Pending = append(result.Pending, p.value)
		}
	}
	return result, nil
}

var _ agentlink.ReviewBackend = (*backend)(nil)
