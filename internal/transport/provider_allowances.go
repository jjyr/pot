package transport

import (
	"context"
	"github.com/jjyr/pot/internal/control"
)

func (a *Application) providerAllowanceCall(ctx context.Context, method, path string, r Request) (any, error, bool) {
	switch path {
	case "/api/v1/usage/providers":
		if method == "GET" {
			result, err := a.Control.ProviderAllowances(ctx)
			return result, err, true
		}
		var body control.SaveProviderAllowance
		if err := decode(r.Body, &body); err != nil {
			return nil, err, true
		}
		result, err := a.Control.SaveProviderAllowance(ctx, body)
		return result, err, true
	}
	return nil, nil, false
}
