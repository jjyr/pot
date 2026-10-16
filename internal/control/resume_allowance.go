package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/usage"
)

func (s *Service) MailAllowanceWaits(ctx context.Context, owner string) ([]usage.Waiting, error) {
	values, err := s.Mailbox.WaitingMessages(ctx, owner)
	if err != nil {
		return nil, err
	}
	result := []usage.Waiting{}
	for _, message := range values {
		wait, err := s.Usage.Waiting(ctx, mailExecutionOwner(message))
		if err != nil {
			return nil, err
		}
		if wait != nil && wait.Turn == message.ID {
			result = append(result, *wait)
		}
	}
	return result, nil
}

// ResumeAllowance validates the originating assistant before releasing a manual
// wait. Mail correspondence and Tasks cannot release another owner's scope.
func (s *Service) ResumeAllowance(ctx context.Context, owner, scope string) error {
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return err
	}
	if scope == "" {
		scope = owner
	}
	waiting, err := s.Usage.Waiting(ctx, scope)
	if err != nil {
		return err
	}
	if waiting == nil {
		return errors.New("allowance wait unavailable")
	}
	if scope == owner {
		if err = s.Usage.ReleaseWait(ctx, scope); err != nil {
			return err
		}
		if err = c.Resume(ctx); err != nil {
			return err
		}
	} else {
		task, err := s.Tasks.Get(ctx, owner, scope)
		if err != nil {
			return err
		}
		if task != nil && task.RunID == waiting.Turn {
			if err = s.Usage.ReleaseWait(ctx, scope); err != nil {
				return err
			}
			if err = s.Tasks.ResumeWaiting(ctx, owner, scope); err != nil {
				return err
			}
		} else {
			waits, err := s.MailAllowanceWaits(ctx, owner)
			if err != nil {
				return err
			}
			found := false
			for _, value := range waits {
				if value.Scope == scope && value.Turn == waiting.Turn {
					found = true
					break
				}
			}
			if !found {
				return errors.New("allowance wait belongs to another owner or original run")
			}
			if err = s.Usage.ReleaseWait(ctx, scope); err != nil {
				return err
			}
		}
	}
	s.Changes.Notify("usage")
	s.wakeTasks()
	return nil
}
