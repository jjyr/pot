package transport

import (
	"context"
	"errors"
	"net/url"

	"github.com/jjyr/pot/internal/resourcecontrol"
)

// resourceCall is native/private owner-only. The browser proxy intentionally
// excludes this path from both its read and write route allowlists.
func (a *Application) resourceCall(ctx context.Context, method string, u *url.URL, r Request) (any, error, bool) {
	if u.Path != "/api/v1/sandbox-resources" {
		return nil, nil, false
	}
	if a.Resources == nil {
		return nil, errors.New("Sandbox resource preparation unavailable"), true
	}
	if method == "GET" {
		v, err := a.Resources.Snapshot(ctx)
		return v, err, true
	}
	var command struct {
		Action     string `json:"action"`
		SandboxID  string `json:"sandbox_id,omitempty"`
		BundleID   string `json:"bundle_id,omitempty"`
		RequestID  string `json:"request_id,omitempty"`
		Directory  string `json:"directory,omitempty"`
		ResourceID string `json:"resource_id,omitempty"`
	}
	if err := decode(r.Body, &command); err != nil {
		return nil, err, true
	}
	var result resourcecontrol.Request
	var err error
	switch command.Action {
	case "prune":
		err = a.Resources.Prune(ctx, command.ResourceID)
		return map[string]bool{"pruned": err == nil}, err, true
	case "request":
		result, err = a.Resources.Request(ctx, command.SandboxID, command.BundleID)
	case "download":
		result, err = a.Resources.Start(command.RequestID)
	case "import":
		result, err = a.Resources.Import(command.RequestID, command.Directory)
	case "cancel":
		result, err = a.Resources.Cancel(command.RequestID)
	default:
		err = errors.New("unknown resource action")
	}
	return result, err, true
}
