package agentlink

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
	"time"
)

type DeleteAssistantRequest struct {
	From        protocol.AgentRef `json:"from"`
	AssistantID string            `json:"assistant_id"`
	Revision    uint64            `json:"revision"`
}

type AssistantDeletion struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// AssistantDeletionBackend retires an idle identity; it grants no execution or
// filesystem configuration authority. Only authenticated user callers enter it.
type AssistantDeletionBackend interface {
	DeleteAssistant(context.Context, DeleteAssistantRequest) error
}

func (s *Service) DeleteAssistant(ctx context.Context, link string, input DeleteAssistantRequest) (AssistantDeletion, error) {
	if input.From.Kind != "user" || !input.From.Valid() || input.From.NodeID != s.options.HostIdentity.NodeID {
		return AssistantDeletion{}, ErrUnauthorized
	}
	c, err := s.Get(ctx, link)
	if err != nil {
		return AssistantDeletion{}, err
	}
	session, err := s.businessSession(ctx, c)
	if err != nil {
		return AssistantDeletion{}, err
	}
	var result AssistantDeletion
	err = s.call(ctx, session, "assistant.delete", input, &result)
	if err == nil && (result.ID != input.AssistantID || !result.Deleted) {
		err = ErrConflict
	}
	if err == nil {
		// The authenticated acknowledgement is deletion evidence even when the
		// following directory read is unavailable. Persist that evidence in the
		// existing snapshot before notifying its consumers and fencing old reads.
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		s.mu.Lock()
		s.directoryEpoch[link]++
		current, readErr := s.Get(persist, link)
		if readErr == nil && current.Directory != nil {
			d := *current.Directory
			d.Targets = append([]Endpoint(nil), d.Targets...)
			target := AssistantTarget(c.PeerHost.NodeID, input.AssistantID)
			targets := d.Targets[:0]
			for _, entry := range d.Targets {
				if entry.Address != target {
					targets = append(targets, entry)
				}
			}
			d.Targets = targets
			if d.DefaultTarget != nil && *d.DefaultTarget == target {
				d.DefaultTarget = nil
			}
			current.Directory = &d
			readErr = s.update(persist, current)
		}
		s.mu.Unlock()
		if readErr != nil && !errors.Is(readErr, ErrRevoked) {
			return result, readErr
		}
		// A successful retirement remains successful if a later directory read fails.
		_, _ = s.Directory(ctx, link)
	}
	return result, err
}
