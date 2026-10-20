package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

func (s *Service) recordCompaction(ctx context.Context, scope, turn string, raw json.RawMessage) error {
	var report core.CompactionReport
	if err := core.DecodeJSON(raw, &report); err != nil {
		return err
	}
	if report.Tokens > core.MaxSafeInteger {
		return errors.New("compaction token count exceeds precision")
	}
	if report.Action != "evidence" {
		event := usage.CompactionEvent{ID: report.ID, Action: report.Action, Reason: report.Reason}
		tokens := int64(report.Tokens)
		if report.Action == "start" {
			event.TokensBefore = &tokens
		} else if report.Action == "complete" {
			event.TokensAfter = &tokens
		}
		if err := s.Usage.Compaction(ctx, scope, turn, event, storage.Now()); err != nil {
			return err
		}
	}
	return s.Usage.RecordCompaction(ctx, scope, turn, report.ID, raw)
}

func (s *Service) CompactionRecord(ctx context.Context, owner, scope, id string) (any, error) {
	if scope == "" {
		scope = owner
	}
	if scope != owner {
		task, err := s.Tasks.Get(ctx, owner, scope)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, errors.New("compaction owner mismatch")
		}
	}
	return s.Usage.CompactionRecord(ctx, scope, id)
}
