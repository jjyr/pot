package transport

import (
	"context"
	"errors"
	"net/url"
)

func (a *Application) machineCall(ctx context.Context, method string, u *url.URL, r Request) (any, error, bool) {
	if u.Path != "/api/v1/machines" {
		return nil, nil, false
	}
	if method != "GET" {
		return nil, errors.New("machines inventory requires GET"), true
	}
	value, err := a.Control.MachineList(ctx)
	return value, err, true
}
