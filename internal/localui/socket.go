package localui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/pkg/protocol"
)

const (
	maxActiveOps      = 64
	maxIDLength       = 128
	requestTimeout    = 30 * time.Second
	writeTimeout      = 15 * time.Second
	heartbeatInterval = 15 * time.Second
	maxReadBytes      = 16 << 20
)

type SocketOptions struct {
	Caller             Caller
	ExpectedControlID  string
	Validate           func(method, path string) error
	OriginPatterns     []string
	InsecureSkipVerify bool
}

type SocketError struct {
	Status   int
	APIError protocol.APIError
}

func (e SocketError) Error() string {
	if e.APIError.Message != "" {
		return e.APIError.Message
	}
	if e.APIError.Code != "" {
		return e.APIError.Code
	}
	return fmt.Sprintf("socket error %d", e.Status)
}

func (e SocketError) Unwrap() error {
	return e.APIError
}

type operation struct {
	id     string
	ctx    context.Context
	cancel context.CancelFunc
}

type socketServer struct {
	options    SocketOptions
	conn       *websocket.Conn
	baseCtx    context.Context
	baseCancel context.CancelFunc
	mu         sync.Mutex
	ops        map[string]*operation
	slots      chan struct{}
}

type inboundEnvelope struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type requestMessage struct {
	ID                string          `json:"id"`
	Type              string          `json:"type"`
	Method            string          `json:"method"`
	Path              string          `json:"path"`
	Body              json.RawMessage `json:"body"`
	ExpectedControlID string          `json:"expected_control_id,omitempty"`
}

type subscribeMessage struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	Path              string `json:"path"`
	ExpectedControlID string `json:"expected_control_id,omitempty"`
}

type cancelMessage struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type responseMessage struct {
	Type   string             `json:"type"`
	ID     string             `json:"id"`
	Status int                `json:"status"`
	Result json.RawMessage    `json:"result,omitempty"`
	Error  *protocol.APIError `json:"error,omitempty"`
}

type eventMessage struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Event   string          `json:"event"`
	EventID string          `json:"event_id"`
	Data    json.RawMessage `json:"data"`
}

type endMessage struct {
	Type   string             `json:"type"`
	ID     string             `json:"id"`
	Status int                `json:"status,omitempty"`
	Error  *protocol.APIError `json:"error,omitempty"`
}

type heartbeatMessage struct {
	Type string `json:"type"`
}

func ServeWebSocket(w http.ResponseWriter, r *http.Request, options SocketOptions) {
	if !headerOffersSubprotocol(r, "pot.v1") {
		http.Error(w, "pot.v1 subprotocol required", http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: options.InsecureSkipVerify,
		OriginPatterns:     options.OriginPatterns,
		Subprotocols:       []string{"pot.v1"},
	})
	if err != nil {
		return
	}
	if conn.Subprotocol() != "pot.v1" {
		_ = conn.Close(websocket.StatusPolicyViolation, "pot.v1 subprotocol required")
		return
	}
	conn.SetReadLimit(maxReadBytes)

	baseCtx, baseCancel := context.WithCancel(r.Context())
	s := &socketServer{
		options:    options,
		conn:       conn,
		baseCtx:    baseCtx,
		baseCancel: baseCancel,
		ops:        make(map[string]*operation),
		slots:      make(chan struct{}, maxActiveOps),
	}

	defer func() {
		baseCancel()
		s.mu.Lock()
		for _, op := range s.ops {
			op.cancel()
		}
		s.ops = make(map[string]*operation)
		s.mu.Unlock()
		_ = conn.CloseNow()
	}()

	go s.heartbeat()

	for {
		typ, data, err := conn.Read(baseCtx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			_ = conn.Close(websocket.StatusPolicyViolation, "text messages required")
			return
		}
		s.handleMessage(data)
	}
}

func headerOffersSubprotocol(r *http.Request, proto string) bool {
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(header, ",") {
			if strings.TrimSpace(part) == proto {
				return true
			}
		}
	}
	return false
}

func (s *socketServer) heartbeat() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.baseCtx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.baseCtx.Err() != nil {
				s.mu.Unlock()
				return
			}
			err := s.writeLocked(heartbeatMessage{Type: "heartbeat"})
			s.mu.Unlock()
			if err != nil {
				s.baseCancel()
				return
			}
		}
	}
}

func (s *socketServer) handleMessage(data []byte) {
	var env inboundEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		s.sendError(env.ID, invalidRequestError(err))
		return
	}

	switch env.Type {
	case "request":
		var msg requestMessage
		if err := decodeStrict(data, &msg); err != nil {
			s.sendError(env.ID, invalidRequestError(err))
			return
		}
		s.handleRequest(msg)
	case "subscribe":
		var msg subscribeMessage
		if err := decodeStrict(data, &msg); err != nil {
			s.sendError(env.ID, invalidRequestError(err))
			return
		}
		s.handleSubscribe(msg)
	case "unsubscribe", "cancel":
		var msg cancelMessage
		if err := decodeStrict(data, &msg); err != nil {
			s.sendError(env.ID, invalidRequestError(err))
			return
		}
		s.handleCancel(msg)
	default:
		s.sendError(env.ID, invalidRequestError(errors.New("unsupported message type")))
	}
}

func (s *socketServer) handleRequest(msg requestMessage) {
	if se := s.expectedControlError(msg.ExpectedControlID); se.Status != 0 {
		s.sendError(msg.ID, se)
		return
	}
	switch msg.Method {
	case "GET", "POST":
	case "DELETE":
		if !isIntegrationPath(msg.Path) {
			s.sendError(msg.ID, SocketError{
				Status: 405,
				APIError: protocol.APIError{
					Code:    "method_not_allowed",
					Message: "method not allowed",
				},
			})
			return
		}
	default:
		s.sendError(msg.ID, SocketError{
			Status: 405,
			APIError: protocol.APIError{
				Code:    "method_not_allowed",
				Message: "method not allowed",
			},
		})
		return
	}

	ctx, cancel := context.WithTimeout(s.baseCtx, requestTimeout)
	op := &operation{id: msg.ID, ctx: ctx, cancel: cancel}
	if !s.startOp(op) {
		cancel()
		return
	}
	go s.runRequest(op, msg)
}

func (s *socketServer) handleSubscribe(msg subscribeMessage) {
	if se := s.expectedControlError(msg.ExpectedControlID); se.Status != 0 {
		s.sendError(msg.ID, se)
		return
	}

	ctx, cancel := context.WithCancel(s.baseCtx)
	op := &operation{id: msg.ID, ctx: ctx, cancel: cancel}
	if !s.startOp(op) {
		cancel()
		return
	}
	go s.runSubscribe(op, msg)
}

func (s *socketServer) handleCancel(msg cancelMessage) {
	if msg.ID == "" || len(msg.ID) > maxIDLength {
		s.sendError(msg.ID, SocketError{
			Status: 400,
			APIError: protocol.APIError{
				Code:    "request_failed",
				Message: "invalid id",
			},
		})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if op, ok := s.ops[msg.ID]; ok {
		delete(s.ops, msg.ID)
		op.cancel()
	}

	_ = s.writeLocked(responseMessage{
		Type:   "response",
		ID:     msg.ID,
		Status: 200,
		Result: json.RawMessage("{}"),
	})
}

func (s *socketServer) expectedControlError(got string) SocketError {
	if s.options.ExpectedControlID == "" {
		return SocketError{}
	}
	if got != "" && got != s.options.ExpectedControlID {
		return SocketError{
			Status: 409,
			APIError: protocol.APIError{
				Code:    "identity_changed",
				Message: "expected control id mismatch",
			},
		}
	}
	return SocketError{}
}

func (s *socketServer) startOp(op *operation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if op.id == "" || len(op.id) > maxIDLength {
		s.writeErrorLocked(op.id, SocketError{
			Status: 400,
			APIError: protocol.APIError{
				Code:    "request_failed",
				Message: "invalid id",
			},
		})
		return false
	}
	if _, ok := s.ops[op.id]; ok {
		s.writeErrorLocked(op.id, SocketError{
			Status: 409,
			APIError: protocol.APIError{
				Code:    "duplicate_id",
				Message: "duplicate id",
			},
		})
		return false
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.writeErrorLocked(op.id, SocketError{
			Status: 429,
			APIError: protocol.APIError{
				Code:    "too_many_requests",
				Message: "too many active operations",
			},
		})
		return false
	}
	s.ops[op.id] = op
	return true
}

func (s *socketServer) releaseSlot() {
	select {
	case <-s.slots:
	default:
	}
}

func (s *socketServer) runRequest(op *operation, msg requestMessage) {
	defer op.cancel()
	defer s.releaseSlot()

	stop := context.AfterFunc(op.ctx, func() {
		if errors.Is(op.ctx.Err(), context.DeadlineExceeded) {
			s.finishOpError(op, SocketError{
				Status: 504,
				APIError: protocol.APIError{
					Code:    "request_timeout",
					Message: "request timed out",
				},
			})
		}
	})
	defer stop()

	path, err := socketPath(msg.Path)
	if err != nil {
		s.finishOpError(op, socketErrorFromError(err))
		return
	}
	if s.options.Validate != nil {
		if err := s.options.Validate(msg.Method, path); err != nil {
			s.finishOpError(op, socketErrorFromError(err))
			return
		}
	}
	if s.options.Caller == nil {
		s.finishOpError(op, SocketError{
			Status: 500,
			APIError: protocol.APIError{
				Code:    "request_failed",
				Message: "caller unavailable",
			},
		})
		return
	}

	if err := op.ctx.Err(); err != nil {
		s.finishOpError(op, socketErrorFromError(err))
		return
	}
	result, err := s.options.Caller.Call(op.ctx, msg.Method, path, msg.Body)
	if ctxErr := op.ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			s.finishOpError(op, SocketError{
				Status: 504,
				APIError: protocol.APIError{
					Code:    "request_timeout",
					Message: "request timed out",
				},
			})
		} else {
			s.finishOpError(op, socketErrorFromError(ctxErr))
		}
		return
	}
	if err != nil {
		s.finishOpError(op, socketErrorFromError(err))
		return
	}

	s.writeForOp(op, responseMessage{
		Type:   "response",
		ID:     op.id,
		Status: 200,
		Result: result,
	}, true)
}

func (s *socketServer) runSubscribe(op *operation, msg subscribeMessage) {
	defer op.cancel()
	defer s.releaseSlot()

	path, err := socketPath(msg.Path)
	if err != nil {
		s.finishOpError(op, socketErrorFromError(err))
		return
	}
	if s.options.Validate != nil {
		if err := s.options.Validate("GET", path); err != nil {
			s.finishOpError(op, socketErrorFromError(err))
			return
		}
	}

	if !s.writeForOp(op, responseMessage{
		Type:   "response",
		ID:     op.id,
		Status: 200,
		Result: json.RawMessage("{}"),
	}, false) {
		s.abortOp(op)
		return
	}

	if s.options.Caller == nil {
		se := SocketError{
			Status: 500,
			APIError: protocol.APIError{
				Code:    "request_failed",
				Message: "caller unavailable",
			},
		}
		s.writeForOp(op, endMessage{
			Type:   "end",
			ID:     op.id,
			Status: se.Status,
			Error:  &se.APIError,
		}, true)
		return
	}

	err = s.options.Caller.Stream(op.ctx, path, func(ev Event) error {
		if !s.writeForOp(op, eventMessage{
			Type:    "event",
			ID:      op.id,
			Event:   ev.Type,
			EventID: ev.ID,
			Data:    ev.Data,
		}, false) {
			return errors.New("subscription is no longer active")
		}
		return nil
	})
	if err != nil {
		se := socketErrorFromError(err)
		s.writeForOp(op, endMessage{
			Type:   "end",
			ID:     op.id,
			Status: se.Status,
			Error:  &se.APIError,
		}, true)
		return
	}

	s.writeForOp(op, endMessage{
		Type: "end",
		ID:   op.id,
	}, true)
}

func (s *socketServer) finishOpError(op *operation, se SocketError) {
	if se.Status == 0 {
		se.Status = 500
	}
	s.writeForOp(op, responseMessage{
		Type:   "response",
		ID:     op.id,
		Status: se.Status,
		Error:  &se.APIError,
	}, true)
}

func (s *socketServer) abortOp(op *operation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ops[op.id] == op {
		delete(s.ops, op.id)
		op.cancel()
	}
}

func (s *socketServer) writeForOp(op *operation, v interface{}, retire bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ops[op.id] != op {
		return false
	}
	err := s.writeLocked(v)
	if retire {
		if s.ops[op.id] == op {
			delete(s.ops, op.id)
			op.cancel()
		}
	}
	return err == nil
}

func (s *socketServer) sendError(id string, se SocketError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeErrorLocked(id, se)
}

func (s *socketServer) writeErrorLocked(id string, se SocketError) {
	if se.Status == 0 {
		se.Status = 500
	}
	_ = s.writeLocked(responseMessage{
		Type:   "response",
		ID:     id,
		Status: se.Status,
		Error:  &se.APIError,
	})
}

func (s *socketServer) writeLocked(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		s.baseCancel()
		return err
	}
	if len(data) > maxReadBytes {
		s.baseCancel()
		return fmt.Errorf("outbound message exceeds %d bytes", maxReadBytes)
	}
	ctx, cancel := context.WithTimeout(s.baseCtx, writeTimeout)
	defer cancel()
	if err := s.conn.Write(ctx, websocket.MessageText, data); err != nil {
		s.baseCancel()
		return err
	}
	return nil
}

func socketErrorFromError(err error) SocketError {
	if err == nil {
		return SocketError{}
	}

	var se SocketError
	if errors.As(err, &se) {
		return se
	}
	var sep *SocketError
	if errors.As(err, &sep) && sep != nil {
		return *sep
	}

	var apiErr protocol.APIError
	if errors.As(err, &apiErr) {
		return SocketError{
			Status:   statusForAPICode(apiErr.Code),
			APIError: apiErr,
		}
	}
	var apiErrPtr *protocol.APIError
	if errors.As(err, &apiErrPtr) && apiErrPtr != nil {
		return SocketError{
			Status:   statusForAPICode(apiErrPtr.Code),
			APIError: *apiErrPtr,
		}
	}

	return SocketError{
		Status: 400,
		APIError: protocol.APIError{
			Code:    "request_failed",
			Message: err.Error(),
		},
	}
}

func statusForAPICode(code string) int {
	switch code {
	case "not_found":
		return 404
	case "unauthorized":
		return 401
	case "forbidden":
		return 403
	case "method_not_allowed":
		return 405
	case "pot_token_limit":
		return 429
	default:
		return 409
	}
}

func invalidRequestError(err error) SocketError {
	return SocketError{
		Status: 400,
		APIError: protocol.APIError{
			Code:    "request_failed",
			Message: err.Error(),
		},
	}
}

func socketPath(raw string) (string, error) {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return "", err
	}
	if u.IsAbs() || u.Host != "" || u.Scheme != "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("absolute or hierarchical URI is not allowed")
	}
	if u.Path == "" || u.Path[0] != '/' || strings.HasPrefix(u.Path, "//") {
		return "", errors.New("path must start with a single slash")
	}
	if strings.Contains(u.Path, "\\") || strings.Contains(u.Path, "//") {
		return "", errors.New("invalid path separator")
	}
	if hasEncodedSep(u.EscapedPath()) {
		return "", errors.New("invalid encoded path")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return "", errors.New("invalid path component")
		}
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", err
	}
	u.RawQuery = q.Encode()
	return u.RequestURI(), nil
}

func hasEncodedSep(escaped string) bool {
	lower := strings.ToLower(escaped)
	return strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%2e")
}

func isIntegrationPath(raw string) bool {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return false
	}
	return strings.HasPrefix(u.Path, "/api/v1/integrations/")
}

func decodeStrict(data []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}
