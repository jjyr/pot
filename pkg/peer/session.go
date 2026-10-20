package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	yamux "github.com/libp2p/go-yamux/v5"
)

// Session multiplexes independent RPC attempts over an authenticated connection.
// Stream IDs correlate responses; request IDs belong to the business service.
type Session struct {
	conn      net.Conn
	mux       *yamux.Session
	remote    Identity
	service   string
	auth      json.RawMessage
	handler   Handler
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	pending   map[string]bool
	handlers  chan struct{}
	memory    *memoryBudget
}

func newSession(ctx context.Context, c net.Conn, remote Identity, service string, auth json.RawMessage, handler Handler, client bool) (*Session, error) {
	ctx, cancel := context.WithCancel(ctx)
	budget := &memoryBudget{limit: maxSessionMemory, parent: hostMemory}
	config := yamux.DefaultConfig()
	config.AcceptBacklog = maxPending
	config.MaxIncomingStreams = maxPending
	config.InitialStreamWindowSize = 256 * 1024
	config.MaxStreamWindowSize = 4 * 1024 * 1024
	config.MaxMessageSize = 64 * 1024
	config.KeepAliveInterval = 20 * time.Second
	config.MeasureRTTInterval = 20 * time.Second
	config.ConnectionWriteTimeout = 30 * time.Second
	config.WriteCoalesceDelay = 0
	config.LogOutput = io.Discard
	manager := func() (yamux.MemoryManager, error) { return &streamMemory{budget: budget}, nil }
	constructor := yamux.Server
	if client {
		constructor = yamux.Client
	}
	mux, err := constructor(c, config, manager)
	if err != nil {
		cancel()
		return nil, err
	}
	return &Session{conn: c, mux: mux, remote: remote, service: service, auth: append(json.RawMessage(nil), auth...), handler: handler, ctx: ctx, cancel: cancel, done: make(chan struct{}), pending: make(map[string]bool), handlers: make(chan struct{}, maxPending), memory: budget}, nil
}
func (s *Session) Remote() Identity      { return s.remote }
func (s *Session) Service() string       { return s.service }
func (s *Session) Auth() json.RawMessage { return append(json.RawMessage(nil), s.auth...) }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Close()                { s.closeOnce.Do(func() { s.cancel(); _ = s.mux.Close(); close(s.done) }) }
func (s *Session) start() {
	go s.acceptLoop()
	go func() {
		select {
		case <-s.ctx.Done():
		case <-s.mux.CloseChan():
		}
		s.Close()
	}()
}
func (s *Session) open(ctx context.Context, r Request) (*yamux.Stream, func(), error) {
	if err := validRequest(r); err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		return nil, nil, ErrClosed
	}
	if s.pending[r.ID] || len(s.pending) >= maxPending {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("peer request conflict/capacity")
	}
	s.pending[r.ID] = true
	s.mu.Unlock()
	release := func() { s.mu.Lock(); delete(s.pending, r.ID); s.mu.Unlock() }
	stream, err := s.mux.OpenStream(ctx)
	if err != nil {
		release()
		// Yamux cannot recover an initial receive-window reservation failure.
		// Close the exhausted session rather than leaving half-open SYN slots.
		if errors.Is(err, errMemoryCapacity) {
			s.Close()
		}
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	cleanup := func() { stop(); _ = stream.Close(); release() }
	return stream, cleanup, nil
}
func streamDeadline(ctx context.Context, duration time.Duration) time.Time {
	deadline := time.Now().Add(duration)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return deadline
}

// Call sends one request exactly once. Any uncertain delivery is returned to the
// business layer; peer never replays work on this session or after reconnect.
func (s *Session) Call(ctx context.Context, r Request) (json.RawMessage, error) {
	stream, cleanup, err := s.open(ctx, r)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	_ = stream.SetWriteDeadline(streamDeadline(ctx, 30*time.Second))
	err = writeMessage(ctx, stream, frame{Kind: "request", ID: r.ID, Method: r.Method}, r.Params, s.memory)
	if err == nil {
		err = stream.CloseWrite()
	}
	if err != nil {
		_ = stream.Reset()
		return nil, fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	_ = stream.SetReadDeadline(streamDeadline(ctx, 90*time.Second))
	h, body, release, err := readMessage(stream, s.memory, func(h frame) bool { return h.Kind == "response" && h.ID == r.ID && h.Method == r.Method })
	defer release()
	if err != nil || !json.Valid(body) {
		_ = stream.Reset()
		if err == nil {
			err = fmt.Errorf("invalid peer response JSON")
		}
		return nil, fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	if h.Error != nil {
		return body, h.Error
	}
	return body, nil
}

// Notify is a lossy hint, restricted to event methods. Durable effects use Call.
func (s *Session) Notify(ctx context.Context, r Request) error {
	if !strings.HasPrefix(r.Method, "event.") {
		return fmt.Errorf("notifications require event methods")
	}
	stream, cleanup, err := s.open(ctx, r)
	if err != nil {
		return err
	}
	defer cleanup()
	_ = stream.SetWriteDeadline(streamDeadline(ctx, 30*time.Second))
	err = writeMessage(ctx, stream, frame{Kind: "event", ID: r.ID, Method: r.Method}, r.Params, s.memory)
	if err == nil {
		err = stream.CloseWrite()
	}
	if err != nil {
		_ = stream.Reset()
	}
	return err
}
func boundedText(text string, limit int) string {
	text = strings.ToValidUTF8(text, "?")
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
func boundedError(err error) *RemoteError {
	var remote *RemoteError
	if errors.As(err, &remote) {
		out := *remote
		out.Message = boundedText(out.Message, 4096)
		out.Code = boundedText(out.Code, 128)
		if len(out.Data) > 16*1024 || len(out.Data) > 0 && !json.Valid(out.Data) {
			out.Data = nil
		}
		return &out
	}
	return &RemoteError{Message: boundedText(err.Error(), 4096)}
}

func (s *Session) acceptLoop() {
	defer s.Close()
	for {
		stream, err := s.mux.AcceptStream()
		if err != nil {
			return
		}
		select {
		case s.handlers <- struct{}{}:
			go s.handle(stream)
		default:
			_ = stream.Reset()
		}
	}
}
func (s *Session) handle(stream *yamux.Stream) {
	defer func() { _ = stream.Close(); <-s.handlers }()
	_ = stream.SetReadDeadline(time.Now().Add(90 * time.Second))
	h, body, release, err := readMessage(stream, s.memory, func(h frame) bool { return h.Kind == "request" || h.Kind == "event" })
	defer release()
	if err != nil || !json.Valid(body) {
		_ = stream.Reset()
		return
	}
	var result json.RawMessage
	if s.handler == nil {
		err = fmt.Errorf("peer service method unavailable")
	} else {
		// An abandoned RPC wait does not cancel a durable business operation.
		// The handler owns cancellation and deduplication through its receipts.
		result, err = s.handler(s.ctx, Request{h.ID, h.Method, body})
	}
	if h.Kind == "event" {
		return
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	if len(result) > MaxPayload || !json.Valid(result) {
		result = json.RawMessage("null")
		err = fmt.Errorf("invalid peer response size/JSON")
	}
	reply := frame{Kind: "response", ID: h.ID, Method: h.Method}
	if err != nil {
		reply.Error = boundedError(err)
	}
	_ = stream.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if writeMessage(s.ctx, stream, reply, result, s.memory) != nil {
		_ = stream.Reset()
		return
	}
	if stream.CloseWrite() != nil {
		_ = stream.Reset()
	}
}
