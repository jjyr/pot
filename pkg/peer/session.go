package peer

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"
)

type frame struct {
	Session   string          `json:"session"`
	Direction string          `json:"direction"`
	Sequence  uint64          `json:"sequence"`
	Kind      string          `json:"kind"`
	ID        string          `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Error     *RemoteError    `json:"error,omitempty"`
}
type response struct {
	raw json.RawMessage
	err error
}
type pendingCall struct {
	method string
	result chan response
}

type Session struct {
	conn                      net.Conn
	local                     Identity
	key                       ed25519.PrivateKey
	remote                    Identity
	id, service               string
	auth                      json.RawMessage
	handler                   Handler
	outDirection, inDirection string
	ctx                       context.Context
	cancel                    context.CancelFunc
	done                      chan struct{}
	closeOnce                 sync.Once
	writer                    chan struct{}
	outSeq                    uint64 // protected by writer
	inSeq                     uint64 // single reader
	mu                        sync.Mutex
	pending                   map[string]pendingCall
	inflight                  map[string]bool
	handlers                  chan struct{}
}

func newSession(ctx context.Context, c net.Conn, local Identity, key ed25519.PrivateKey, remote Identity, id, service string, auth json.RawMessage, handler Handler, out, in string) *Session {
	ctx, cancel := context.WithCancel(ctx)
	return &Session{conn: c, local: local, key: append(ed25519.PrivateKey(nil), key...), remote: remote, id: id, service: service, auth: append(json.RawMessage(nil), auth...), handler: handler, outDirection: out, inDirection: in, ctx: ctx, cancel: cancel, done: make(chan struct{}), writer: make(chan struct{}, 1), pending: map[string]pendingCall{}, inflight: map[string]bool{}, handlers: make(chan struct{}, maxPending)}
}
func (s *Session) Remote() Identity      { return s.remote }
func (s *Session) Service() string       { return s.service }
func (s *Session) Auth() json.RawMessage { return append(json.RawMessage(nil), s.auth...) }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Close()                { s.closeOnce.Do(func() { s.cancel(); _ = s.conn.Close(); close(s.done) }) }
func (s *Session) start() {
	go s.readLoop()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		defer s.Close()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				if s.send(s.ctx, frame{Kind: "ping"}) != nil {
					return
				}
			}
		}
	}()
}
func (s *Session) send(ctx context.Context, value frame) error {
	select {
	case s.writer <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrClosed
	}
	defer func() { <-s.writer }()
	if s.ctx.Err() != nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.outSeq == math.MaxUint64 {
		return fmt.Errorf("peer sequence exhausted")
	}
	s.outSeq++
	value.Session = s.id
	value.Direction = s.outDirection
	value.Sequence = s.outSeq
	raw, err := seal(s.key, "pot-peer/frame/v1", value)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = s.conn.SetWriteDeadline(deadline)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = s.conn.SetWriteDeadline(time.Now()); close(done) })
	err = writePacket(s.conn, raw, MaxFrame)
	if !stop() {
		<-done
	}
	_ = s.conn.SetWriteDeadline(time.Time{})
	return err
}

// Call sends one request exactly once. Any uncertain delivery is returned to the
// business layer; peer never replays work on this session or after reconnect.
func (s *Session) Call(ctx context.Context, r Request) (json.RawMessage, error) {
	if err := validRequest(r); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if _, exists := s.pending[r.ID]; exists || len(s.pending) >= maxPending {
		s.mu.Unlock()
		return nil, fmt.Errorf("peer request conflict/capacity")
	}
	ch := make(chan response, 1)
	s.pending[r.ID] = pendingCall{r.Method, ch}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, r.ID); s.mu.Unlock() }()
	if err := s.send(ctx, frame{Kind: "request", ID: r.ID, Method: r.Method, Params: r.Params}); err != nil {
		s.Close()
		return nil, fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	select {
	case reply := <-ch:
		return reply.raw, reply.err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %v", ErrUncertain, ctx.Err())
	case <-s.done:
		return nil, ErrUncertain
	}
}

// Notify is a lossy hint, restricted to event methods. Durable effects use Call.
func (s *Session) Notify(ctx context.Context, r Request) error {
	if err := validRequest(r); err != nil {
		return err
	}
	if len(r.Method) < 6 || r.Method[:6] != "event." {
		return fmt.Errorf("notifications require event methods")
	}
	if err := s.send(ctx, frame{Kind: "event", ID: r.ID, Method: r.Method, Params: r.Params}); err != nil {
		s.Close()
		return err
	}
	return nil
}
func boundedError(err error) *RemoteError {
	var remote *RemoteError
	if errors.As(err, &remote) {
		out := *remote
		if len(out.Message) > 4096 {
			out.Message = out.Message[:4096]
		}
		if len(out.Code) > 128 {
			out.Code = out.Code[:128]
		}
		if len(out.Data) > 16*1024 || len(out.Data) > 0 && !json.Valid(out.Data) {
			out.Data = nil
		}
		return &out
	}
	message := err.Error()
	if len(message) > 4096 {
		message = message[:4096]
	}
	return &RemoteError{Message: message}
}
func (s *Session) readLoop() {
	defer s.Close()
	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		raw, err := readPacket(s.conn, MaxFrame)
		if err != nil {
			return
		}
		var f frame
		if open(raw, s.remote, "pot-peer/frame/v1", &f) != nil || f.Session != s.id || f.Direction != s.inDirection || s.inSeq == math.MaxUint64 || f.Sequence != s.inSeq+1 {
			return
		}
		s.inSeq = f.Sequence
		if f.Kind == "ping" || f.Kind == "pong" {
			if f.ID != "" || f.Method != "" || len(f.Params) != 0 || f.Error != nil {
				return
			}
			if f.Kind == "ping" && s.send(s.ctx, frame{Kind: "pong"}) != nil {
				return
			}
			continue
		}
		if f.Kind == "response" {
			if len(f.Params) > MaxPayload || !json.Valid(f.Params) {
				return
			}
			s.mu.Lock()
			p, ok := s.pending[f.ID]
			s.mu.Unlock()
			if ok {
				if p.method != f.Method {
					return
				}
				result := response{raw: f.Params}
				if f.Error != nil {
					if len(f.Error.Message) > 4096 || len(f.Error.Code) > 128 || len(f.Error.Data) > 16*1024 {
						return
					}
					result.err = f.Error
				}
				select {
				case p.result <- result:
				default:
				}
			}
			continue
		}
		if (f.Kind != "request" && f.Kind != "event") || f.Error != nil || validRequest(Request{f.ID, f.Method, f.Params}) != nil {
			return
		}
		if f.Kind == "event" && (len(f.Method) < 6 || f.Method[:6] != "event.") {
			return
		}
		s.mu.Lock()
		if s.inflight[f.ID] {
			s.mu.Unlock()
			return
		}
		s.inflight[f.ID] = true
		s.mu.Unlock()
		select {
		case s.handlers <- struct{}{}:
		default:
			return
		}
		go s.handle(f)
	}
}
func (s *Session) handle(f frame) {
	defer func() { <-s.handlers; s.mu.Lock(); delete(s.inflight, f.ID); s.mu.Unlock() }()
	var result json.RawMessage
	var err error
	if s.handler == nil {
		err = fmt.Errorf("peer service method unavailable")
	} else {
		result, err = s.handler(s.ctx, Request{f.ID, f.Method, f.Params})
	}
	if f.Kind == "event" {
		return
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	if len(result) > MaxPayload || !json.Valid(result) {
		result = json.RawMessage("null")
		err = fmt.Errorf("invalid peer response size/JSON")
	}
	reply := frame{Kind: "response", ID: f.ID, Method: f.Method, Params: result}
	if err != nil {
		reply.Error = boundedError(err)
	}
	if s.send(s.ctx, reply) != nil {
		s.Close()
	}
}
