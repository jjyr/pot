package core

import "errors"

type cleanupUnconfirmedError struct{}

func (cleanupUnconfirmedError) Error() string            { return "execution cleanup unconfirmed" }
func (cleanupUnconfirmedError) CleanupUnconfirmed() bool { return true }

// ErrCleanupUnconfirmed fences further effects until the owning execution layer
// reconciles original operations. Wrapping/joining it must preserve errors.Is.
var ErrCleanupUnconfirmed error = cleanupUnconfirmedError{}

// IsCleanupUnconfirmed recognizes the shared sentinel and narrow external
// markers without coupling execution or composition implementations.
func IsCleanupUnconfirmed(err error) bool {
	if errors.Is(err, ErrCleanupUnconfirmed) {
		return true
	}
	var marker interface{ CleanupUnconfirmed() bool }
	return errors.As(err, &marker) && marker.CleanupUnconfirmed()
}

// ModelRequestError marks a confirmed unsent request or an explicit provider
// rejection. It permits clearing an invocation's uncertainty marker, never an
// automatic retry by itself. Transport/stream loss must not use this type.
type ModelRequestError struct {
	Cause       error
	Disposition string
}

func (e *ModelRequestError) Error() string { return e.Cause.Error() }
func (e *ModelRequestError) Unwrap() error { return e.Cause }
func (e *ModelRequestError) ModelRequestUnsent() bool {
	return e.Disposition == "unsent" || e.Disposition == "rejected"
}
func IsModelRequestUnsent(err error) bool {
	var marker interface{ ModelRequestUnsent() bool }
	return errors.As(err, &marker) && marker.ModelRequestUnsent()
}

// TransientModelError permits a bounded retry of model inference only. It does
// not assert that a provider request was unsent or authorize replaying tools.
// This is an in-process error contract, never a persisted checkpoint field.
type TransientModelError struct{ Cause error }

func (e *TransientModelError) Error() string { return e.Cause.Error() }
func (e *TransientModelError) Unwrap() error { return e.Cause }
