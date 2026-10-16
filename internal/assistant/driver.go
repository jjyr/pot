package assistant

import (
	"context"
	_ "embed"
	"errors"

	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

// Canonical orchestration instructions shared by Main assistant sessions.
//
//go:embed assets/main_prompt.txt
var MainPrompt string

const ResultPrefix = "pot-result:"

type ChatServices interface {
	Configured() bool
	Identity(protocol.SendMessage) execution.Identity
	Execute(context.Context, protocol.SendMessage) execution.Result
	Failed(error)
	FinishTurn(context.Context, protocol.SendMessage, execution.Result) error
}
type Driver struct {
	Manager  *execution.Manager
	Services ChatServices
}

// Serve sequences accepted chat input verbatim. Every actual Agent invocation
// enters the destination's shared Manager; identity, persistence and authorization
// remain in the injected owning adapter. Queued input is never replayed on restart.
func (d Driver) Serve(ctx context.Context, input <-chan protocol.SendMessage) error {
	if d.Manager == nil || d.Services == nil {
		return errors.New("assistant driver requires execution manager and services")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case request, ok := <-input:
			if !ok {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			result := execution.Result{Phase: execution.Failed}
			if !d.Services.Configured() {
				result.Err = errors.New("configure an assistant before sending messages")
			} else {
				identity := d.Services.Identity(request)
				if identity.Owner == "" || identity.RequestID != request.RequestID || identity.Generation == 0 || (identity.Role != "assistant" && identity.Role != "summary") {
					result.Err = errors.New("invalid assistant execution identity")
				} else {
					_, result = d.Manager.Execute(ctx, identity, func(runCtx context.Context) execution.Result { return d.Services.Execute(runCtx, request) })
				}
			}
			if result.Err != nil {
				d.Services.Failed(result.Err)
			}
			// Final persistence must still run after cancellation. It is bounded by the
			// owning adapter's cleanup policy, not the already cancelled invocation ctx.
			if err := d.Services.FinishTurn(context.WithoutCancel(ctx), request, result); err != nil {
				d.Services.Failed(err)
				return err
			}
		}
	}
}
