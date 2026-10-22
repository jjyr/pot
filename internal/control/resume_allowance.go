package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/usage"
)

type ResumeAllowanceCommand struct {
	RequestID string `json:"request_id" required:"true"`
	Turn      string `json:"turn" required:"true"`
	Scope     string `json:"scope,omitempty"`
	Since     int64  `json:"since" required:"true"`
}

func (q ResumeAllowanceCommand) Validate() error {
	if q.RequestID == "" || len(q.RequestID) > 128 || q.Turn == "" || len(q.Turn) > 128 || len(q.Scope) > 128 || q.Since < 0 || q.Since > usage.MaxSafeInteger {
		return errors.New("original allowance request_id and turn are required")
	}
	return nil
}

// ResumeAllowance captures the original owner, turn and wait instance. The
// durable receipt never releases a later wait, including exact lost-ack retries.
func (s *Service) ResumeAllowance(ctx context.Context, owner string, q ResumeAllowanceCommand) error {
	if err := q.Validate(); err != nil {
		return err
	}
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return err
	}
	scope := q.Scope
	if scope == "" {
		scope = owner
	}
	receipt, err := s.Usage.WaitRelease(ctx, owner, q.RequestID, scope, q.Turn, q.Since)
	if err != nil {
		return err
	}
	if receipt != nil && receipt.Complete {
		return nil
	}
	waiting, err := s.Usage.Waiting(ctx, scope)
	if err != nil {
		return err
	}
	if receipt != nil && (waiting == nil || waiting.Turn != receipt.Turn || waiting.Since != receipt.Since) {
		return s.Usage.CompleteWaitRelease(ctx, owner, q.RequestID)
	}
	if waiting == nil || waiting.Turn != q.Turn || waiting.Since != q.Since {
		return errors.New("original allowance wait changed")
	}
	isTask := false
	if scope != owner {
		task, e := s.Tasks.Get(ctx, owner, scope)
		if e != nil {
			return e
		}
		if task != nil && task.RunID == q.Turn {
			isTask = true
		} else {
			return errors.New("allowance wait belongs to another owner or original run")
		}
	}
	receipt, err = s.Usage.AdmitWaitRelease(ctx, owner, q.RequestID, *waiting)
	if err != nil {
		return err
	}
	if receipt.Complete {
		return nil
	}
	if scope == owner {
		err = c.ResumeOriginal(ctx, q.Turn)
	} else if isTask {
		err = s.Tasks.ResumeWaiting(ctx, owner, scope)
	}
	if err != nil {
		return err
	}
	if err = s.Usage.CompleteWaitRelease(ctx, owner, q.RequestID); err != nil {
		return err
	}
	s.Changes.Notify("usage")
	s.wakeTasks()
	return nil
}
