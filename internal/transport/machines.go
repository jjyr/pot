package transport

import (
	"context"
	"github.com/jjyr/pot/internal/control"
	"net/url"
)

func (a *Application) machineCall(ctx context.Context, method string, u *url.URL, r Request) (any, error, bool) {
	switch u.Path {
	case "/api/v1/machines":
		if method == "GET" {
			value, err := a.Control.MachineList(ctx)
			return value, err, true
		}
		var request control.MachineCommand
		if err := decode(r.Body, &request); err != nil {
			return nil, err, true
		}
		value, err := a.Control.ManageMachine(ctx, request)
		return value, err, true
	case "/api/v1/runtime/control":
		if method == "GET" {
			return map[string]any{"identity": a.Control.Machines.Identity(), "role": a.Control.ControlRole(), "membership": func() any {
				member := a.Control.Machines.Membership()
				if member == nil {
					return nil
				}
				return map[string]any{"control": member.Enrollment.Control, "url": member.Origin, "epoch": member.Enrollment.Epoch}
			}()}, nil, true
		}
		var request control.MembershipCommand
		if err := decode(r.Body, &request); err != nil {
			return nil, err, true
		}
		value, err := a.Control.ManageMembership(ctx, request)
		return value, err, true
	}
	return nil, nil, false
}
