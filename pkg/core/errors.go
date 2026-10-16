package core

import "errors"

type cleanupUnconfirmedError struct{}

func (cleanupUnconfirmedError) Error() string            { return "execution cleanup unconfirmed" }
func (cleanupUnconfirmedError) CleanupUnconfirmed() bool { return true }

// ErrCleanupUnconfirmed fences further effects until the owning execution layer
// reconciles original operations. Wrapping/joining it must preserve errors.Is.
var ErrCleanupUnconfirmed error = cleanupUnconfirmedError{}

// IsCleanupUnconfirmed recognizes the shared sentinel and narrow external
// markers without coupling execution, sandbox or composition implementations.
func IsCleanupUnconfirmed(err error) bool {
	if errors.Is(err, ErrCleanupUnconfirmed) {
		return true
	}
	var marker interface{ CleanupUnconfirmed() bool }
	return errors.As(err, &marker) && marker.CleanupUnconfirmed()
}

// ModelRequestError marks a confirmed unsent request or an explicit provider
// rejection. It permits clearing an invocation's uncertainty marker, never an
// automatic retry. Transport/stream loss must not use this type.
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
