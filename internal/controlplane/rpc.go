package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"github.com/jjyr/pot/internal/core"
	"strings"
	"sync"
	"time"
)

const MaxMessage = 2 * 1024 * 1024
const maxPending = 64

type wire struct {
	Version  uint32          `json:"version" required:"true"`
	Sender   string          `json:"sender" required:"true"`
	Audience string          `json:"audience" required:"true"`
	Epoch    uint64          `json:"epoch" required:"true"`
	Kind     string          `json:"kind" required:"true"`
	ID       string          `json:"id" required:"true"`
	Method   string          `json:"method" required:"true"`
	Params   json.RawMessage `json:"params" required:"true"`
	Error    string          `json:"error" required:"true"`
}
type response struct {
	data json.RawMessage
	err  error
}
type Peer struct {
	conn      *websocket.Conn
	directory *Directory
	remote    Identity
	principal Principal
	handler   Handler
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	pending   map[string]chan response
	seen      map[string]bool
	handlers  chan struct{}
	done      chan struct{}
	close     sync.Once
}

func newPeer(ctx context.Context, c *websocket.Conn, d *Directory, remote Identity, p Principal, h Handler) *Peer {
	ctx, cancel := context.WithCancel(ctx)
	c.SetReadLimit(MaxMessage)
	return &Peer{conn: c, directory: d, remote: remote, principal: p, handler: h, ctx: ctx, cancel: cancel, pending: map[string]chan response{}, seen: map[string]bool{}, handlers: make(chan struct{}, maxPending), done: make(chan struct{})}
}
func (p *Peer) Done() <-chan struct{} { return p.done }
func (p *Peer) Close()                { p.close.Do(func() { p.cancel(); p.conn.CloseNow(); close(p.done) }) }
func (p *Peer) send(ctx context.Context, w wire) error {
	w.Version = 1
	w.Sender = p.directory.identity.RuntimeID
	w.Audience = p.remote.RuntimeID
	w.Epoch = p.principal.Epoch
	signed, err := sign(p.directory.key, w)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(signed)
	if err != nil {
		return err
	}
	if len(raw) > MaxMessage {
		return fmt.Errorf("RPC message exceeds limit")
	}
	return p.conn.Write(ctx, websocket.MessageText, raw)
}

// Call sends once. Every error after admission may mean the remote accepted the
// request; callers must inspect the original durable identity, never resubmit it.
func (p *Peer) Call(ctx context.Context, r Request) (json.RawMessage, error) {
	if err := validRequest(r); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.ctx.Err() != nil {
		p.mu.Unlock()
		return nil, ErrDisconnected
	}
	if len(p.pending) >= maxPending || p.pending[r.ID] != nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("RPC capacity/request conflict")
	}
	ch := make(chan response, 1)
	p.pending[r.ID] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, r.ID); p.mu.Unlock() }()
	if err := p.send(ctx, wire{Kind: "request", ID: r.ID, Method: r.Method, Params: r.Params}); err != nil {
		p.Close()
		return nil, fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	select {
	case reply := <-ch:
		return reply.data, reply.err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %v", ErrUncertain, ctx.Err())
	case <-p.done:
		return nil, ErrUncertain
	}
}

// Notify is best-effort and intended only for resynchronizable state signals.
// Model/tool calls and other effects must use Call with a durable request ID.
func (p *Peer) Notify(ctx context.Context, r Request) error {
	if !strings.HasPrefix(r.Method, "event.") {
		return fmt.Errorf("notifications require event method")
	}
	if err := validRequest(r); err != nil {
		return err
	}
	return p.send(ctx, wire{Kind: "event", ID: r.ID, Method: r.Method, Params: r.Params})
}
func (p *Peer) run() {
	defer p.Close()
	for {
		kind, raw, err := p.conn.Read(p.ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageText {
			return
		}
		var signed Signed
		if core.DecodeJSON(raw, &signed) != nil {
			return
		}
		var w wire
		if verify(p.remote, signed, &w) != nil || w.Version != 1 || w.Audience != p.directory.identity.RuntimeID || w.Sender != p.remote.RuntimeID || w.Epoch != p.principal.Epoch {
			return
		}
		if w.Kind == "response" {
			p.mu.Lock()
			ch := p.pending[w.ID]
			p.mu.Unlock()
			if ch != nil {
				r := response{data: w.Params}
				if w.Error != "" {
					r.err = fmt.Errorf("remote: %s", w.Error)
				}
				select {
				case ch <- r:
				default:
				}
			}
			continue
		}
		if (w.Kind != "request" && w.Kind != "event") || w.Error != "" || validRequest(Request{w.ID, w.Method, w.Params}) != nil {
			return
		}
		if w.Kind == "event" && !strings.HasPrefix(w.Method, "event.") {
			return
		}
		p.mu.Lock()
		duplicate := p.seen[w.ID]
		if len(p.seen) >= 4096 {
			p.mu.Unlock()
			return
		}
		p.seen[w.ID] = true
		p.mu.Unlock()
		if duplicate {
			return
		}
		select {
		case p.handlers <- struct{}{}:
		default:
			return
		}
		go func(w wire) {
			defer func() { <-p.handlers }()
			var result json.RawMessage
			var err error
			if w.Kind == "event" { // Events are hints only, handler still authenticates scope.
				if p.handler != nil {
					_, _ = p.handler(p.ctx, p.principal, Request{w.ID, w.Method, w.Params})
				}
				return
			}
			if p.handler == nil {
				err = fmt.Errorf("RPC method unavailable")
			} else {
				result, err = p.handler(p.ctx, p.principal, Request{w.ID, w.Method, w.Params})
			}
			if len(result) == 0 {
				result = json.RawMessage("null")
			}
			if len(result) > MaxMessage/2 {
				result = json.RawMessage("null")
				err = fmt.Errorf("RPC result exceeds limit")
			}
			message := ""
			if err != nil {
				message = err.Error()
				if len(message) > 4096 {
					message = message[:4096]
				}
			}
			ctx, cancel := context.WithTimeout(p.ctx, 30*time.Second)
			defer cancel()
			if p.send(ctx, wire{Kind: "response", ID: w.ID, Method: w.Method, Params: result, Error: message}) != nil {
				p.Close()
			}
		}(w)
	}
}
