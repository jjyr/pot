package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/peer"
)

const MaxMessage = 2 * 1024 * 1024
const MaxPayload = peer.MaxPayload

// Peer preserves the execution contract while transport framing, authentication,
// replay protection and bounded concurrency belong to the common TCP session.
type Peer struct{ session *peer.Session }

func (p *Peer) Done() <-chan struct{} { return p.session.Done() }
func (p *Peer) Close()                { p.session.Close() }
func (p *Peer) Call(ctx context.Context, r Request) (json.RawMessage, error) {
	if err := validRequest(r); err != nil {
		return nil, err
	}
	value, err := p.session.Call(ctx, peer.Request{ID: r.ID, Method: r.Method, Params: r.Params})
	if errors.Is(err, peer.ErrUncertain) {
		return nil, fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	if errors.Is(err, peer.ErrClosed) {
		return nil, ErrDisconnected
	}
	var remote *peer.RemoteError
	if errors.As(err, &remote) {
		f := Fault{}
		if len(remote.Data) > 0 {
			_ = core.DecodeJSON(remote.Data, &f)
		}
		return nil, &RemoteError{Message: remote.Message, Fault: f}
	}
	return value, err
}
func (p *Peer) Notify(ctx context.Context, r Request) error {
	if !strings.HasPrefix(r.Method, "event.") {
		return fmt.Errorf("notifications require event method")
	}
	if err := validRequest(r); err != nil {
		return err
	}
	return p.session.Notify(ctx, peer.Request{ID: r.ID, Method: r.Method, Params: r.Params})
}
func executionHandler(handler Handler, principal Principal) peer.Handler {
	return func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
		if handler == nil {
			return nil, fmt.Errorf("RPC method unavailable")
		}
		result, err := handler(ctx, principal, Request{ID: request.ID, Method: request.Method, Params: request.Params})
		if err != nil {
			data, _ := json.Marshal(fault(err))
			return nil, &peer.RemoteError{Message: err.Error(), Data: data}
		}
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		return result, nil
	}
}
