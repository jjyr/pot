package tasks

import (
	"context"
	"errors"
	"github.com/jjyr/pot/pkg/storage"
)

// OriginalOperations reads migration-only evidence; new effects have one journal.
func (j *Journal) OriginalOperations(ctx context.Context, node, task, run string) ([]storage.RetiredOperation, error) {
	return storage.ReadRetiredOperations(ctx, j.db, node, task, run)
}

// RecordCleanup is written only after the original injected cleanup service has
// confirmed its effects stopped. Restart preserves this evidence, never handles.
func (j *Journal) RecordCleanup(ctx context.Context, control, task, run string) error {
	return j.change(ctx, control, task, run, func(r *Run) error {
		if r.BootID != j.BootID || (r.Phase != Running && r.Phase != RecoveryRequired) {
			return errors.New("cleanup evidence belongs to another original boot/run")
		}
		r.CleanupConfirmed = true
		return nil
	})
}

// RecoveryIdle rejects a reconciliation while the original invocation is still
// unwinding, even if it has already written its fenced journal state.
func (e *Executor) RecoveryIdle(control, task, run string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active[key(control, task, run)] == nil
}
