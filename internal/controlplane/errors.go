package controlplane

import (
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type Fault struct {
	Code               string `json:"code"`
	Unsent             bool   `json:"unsent"`
	CleanupUnconfirmed bool   `json:"cleanup_unconfirmed"`
}
type RemoteError struct {
	Message string
	Fault   Fault
}

func (e *RemoteError) Error() string            { return e.Message }
func (e *RemoteError) ModelRequestUnsent() bool { return e.Fault.Unsent }
func (e *RemoteError) Unwrap() error {
	var err error
	if e.Fault.Code != "" {
		err = protocol.APIError{Code: e.Fault.Code, Message: e.Message}
	}
	if e.Fault.CleanupUnconfirmed {
		err = errors.Join(err, core.ErrCleanupUnconfirmed)
	}
	return err
}
func fault(err error) *Fault {
	if err == nil {
		return nil
	}
	f := &Fault{Unsent: core.IsModelRequestUnsent(err), CleanupUnconfirmed: core.IsCleanupUnconfirmed(err)}
	var api protocol.APIError
	var pointer *protocol.APIError
	if errors.As(err, &api) {
		f.Code = api.Code
	} else if errors.As(err, &pointer) && pointer != nil {
		f.Code = pointer.Code
	}
	if len(f.Code) > 128 {
		f.Code = ""
	}
	return f
}
