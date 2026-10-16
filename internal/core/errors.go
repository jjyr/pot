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
