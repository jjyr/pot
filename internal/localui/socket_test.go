package localui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/pkg/protocol"
)

type wireMsg struct {
	Type              string          `json:"type"`
	ID                string          `json:"id"`
	Method            string          `json:"method"`
	Path              string          `json:"path"`
	Body              json.RawMessage `json:"body"`
	ExpectedControlID string          `json:"expected_control_id"`
	Status            int             `json:"status"`
	Result            json.RawMessage `json:"result"`
	Error             json.RawMessage `json:"error"`
	Event             string          `json:"event"`
	EventID           string          `json:"event_id"`
	Data              json.RawMessage `json:"data"`
}

type outMsg struct {
	Type              string          `json:"type"`
	ID                string          `json:"id"`
	Method            string          `json:"method,omitempty"`
	Path              string          `json:"path,omitempty"`
	Body              json.RawMessage `json:"body,omitempty"`
	ExpectedControlID string          `json:"expected_control_id,omitempty"`
}

type fakeCaller struct {
	call        func(context.Context, string, string, json.RawMessage) (json.RawMessage, error)
	stream      func(context.Context, string, func(Event) error) error
	callCount   int32
	streamCount int32
}

func (f *fakeCaller) Call(ctx context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
	atomic.AddInt32(&f.callCount, 1)
	if f.call == nil {
		return nil, fmt.Errorf("unexpected call")
	}
	return f.call(ctx, method, path, raw)
}

func (f *fakeCaller) Stream(ctx context.Context, path string, receive func(Event) error) error {
	atomic.AddInt32(&f.streamCount, 1)
	if f.stream == nil {
		return fmt.Errorf("unexpected stream")
	}
	return f.stream(ctx, path, receive)
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func wsURL(s *httptest.Server) string { return strings.Replace(s.URL, "http", "ws", 1) }

func connect(t *testing.T, ctx context.Context, opts SocketOptions, hdr http.Header) (*websocket.Conn, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ServeWebSocket(w, r, opts)
	}))
	if hdr == nil {
		hdr = http.Header{}
	}
	if hdr.Get("Origin") == "" {
		hdr.Set("Origin", srv.URL)
	}
	c, _, err := websocket.Dial(ctx, wsURL(srv), &websocket.DialOptions{Subprotocols: []string{"pot.v1"}, HTTPHeader: hdr})
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v", err)
	}
	return c, func() { _ = c.CloseNow(); srv.Close() }
}

func writeMsg(t *testing.T, ctx context.Context, c *websocket.Conn, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readMsg(t *testing.T, ctx context.Context, c *websocket.Conn) wireMsg {
	t.Helper()
	typ, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("frame type %v", typ)
	}
	var m wireMsg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return m
}

func forbiddenErr() error {
	return SocketError{Status: http.StatusForbidden, APIError: protocol.APIError{Code: "forbidden", Message: "forbidden"}}
}

func TestRequestResult(t *testing.T) {
	ctx := testCtx(t)
	type args struct {
		method string
		path   string
		body   json.RawMessage
	}
	got := make(chan args, 1)
	fc := &fakeCaller{call: func(_ context.Context, m, p string, raw json.RawMessage) (json.RawMessage, error) {
		got <- args{method: m, path: p, body: raw}
		return json.RawMessage(`{"ok":true}`), nil
	}}
	c, done := connect(t, ctx, SocketOptions{Caller: fc, InsecureSkipVerify: true}, nil)
	defer done()
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "1", Method: "GET", Path: "/api/v1/x", Body: json.RawMessage(`{"a":1}`)})
	m := readMsg(t, ctx, c)
	if m.Type != "response" || m.ID != "1" || m.Status != 200 || string(m.Result) != `{"ok":true}` {
		t.Fatalf("bad response: %+v", m)
	}
	select {
	case a := <-got:
		if a.method != "GET" || a.path != "/api/v1/x" || string(a.body) != `{"a":1}` {
			t.Fatalf("bad caller args: %s %s %s", a.method, a.path, a.body)
		}
	case <-ctx.Done():
		t.Fatal("caller not invoked")
	}
}

func TestErrors(t *testing.T) {
	t.Run("typed api error", func(t *testing.T) {
		ctx := testCtx(t)
		fc := &fakeCaller{call: func(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
			return nil, SocketError{Status: http.StatusNotFound, APIError: protocol.APIError{Code: "not_found", Message: "not_found"}}
		}}
		c, done := connect(t, ctx, SocketOptions{Caller: fc, InsecureSkipVerify: true}, nil)
		defer done()
		writeMsg(t, ctx, c, outMsg{Type: "request", ID: "e", Method: "GET", Path: "/api/v1/missing"})
		m := readMsg(t, ctx, c)
		if m.Status != 404 || !strings.Contains(string(m.Error), "not_found") {
			t.Fatalf("api error: %+v", m)
		}
	})
	t.Run("identity mismatch", func(t *testing.T) {
		ctx := testCtx(t)
		fc := &fakeCaller{call: func(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
			t.Fatal("caller must not be called")
			return nil, nil
		}}
		c, done := connect(t, ctx, SocketOptions{Caller: fc, ExpectedControlID: "good", InsecureSkipVerify: true}, nil)
		defer done()
		writeMsg(t, ctx, c, outMsg{Type: "request", ID: "x", Method: "GET", Path: "/api/v1/x", ExpectedControlID: "bad"})
		m := readMsg(t, ctx, c)
		if m.Status != http.StatusConflict || atomic.LoadInt32(&fc.callCount) != 0 {
			t.Fatalf("identity: %+v calls=%d", m, atomic.LoadInt32(&fc.callCount))
		}
	})
}

func TestValidateRejects(t *testing.T) {
	ctx := testCtx(t)
	fc := &fakeCaller{call: func(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
		t.Fatal("caller must not be called")
		return nil, nil
	}, stream: func(context.Context, string, func(Event) error) error {
		t.Fatal("events must not be called")
		return nil
	}}
	opts := SocketOptions{Caller: fc, InsecureSkipVerify: true, Validate: func(method, path string) error {
		if path == "/api/v1/bad" || strings.Contains(path, "..") || !strings.HasPrefix(path, "/") {
			return forbiddenErr()
		}
		return nil
	}}
	c, done := connect(t, ctx, opts, nil)
	defer done()
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "v1", Method: "GET", Path: "/api/v1/bad"})
	m := readMsg(t, ctx, c)
	if m.Status != http.StatusForbidden && !strings.Contains(string(m.Error), "forbidden") {
		t.Fatalf("request validate: %+v", m)
	}
	writeMsg(t, ctx, c, outMsg{Type: "subscribe", ID: "s", Path: "/api/v1/bad"})
	m = readMsg(t, ctx, c)
	if m.Type == "event" || (m.Status != http.StatusForbidden && !strings.Contains(string(m.Error), "forbidden")) {
		t.Fatalf("subscribe validate: %+v", m)
	}
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "v2", Method: "GET", Path: "/api/v1/../secret"})
	m = readMsg(t, ctx, c)
	if m.Status < 400 {
		t.Fatalf("traversal validate: %+v", m)
	}
	if atomic.LoadInt32(&fc.callCount) != 0 || atomic.LoadInt32(&fc.streamCount) != 0 {
		t.Fatalf("caller called: req=%d ev=%d", atomic.LoadInt32(&fc.callCount), atomic.LoadInt32(&fc.streamCount))
	}
}

func TestSubscribeUnsubscribe(t *testing.T) {
	ctx := testCtx(t)
	producerDone := make(chan struct{})
	fc := &fakeCaller{
		call: func(context.Context, string, string, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"fast":true}`), nil
		},
		stream: func(ctx context.Context, path string, receive func(Event) error) error {
			if err := receive(Event{Type: "change", ID: "e1", Data: json.RawMessage(`{"n":1}`)}); err != nil {
				return err
			}
			<-ctx.Done()
			close(producerDone)
			return ctx.Err()
		},
	}
	c, done := connect(t, ctx, SocketOptions{Caller: fc, InsecureSkipVerify: true}, nil)
	defer done()
	writeMsg(t, ctx, c, outMsg{Type: "subscribe", ID: "s1", Path: "/api/v1/ev"})
	m := readMsg(t, ctx, c)
	if m.Type != "response" || m.ID != "s1" || m.Status != 200 {
		t.Fatalf("subscribe ack: %+v", m)
	}
	m = readMsg(t, ctx, c)
	if m.Type != "event" || m.ID != "s1" || m.Event != "change" || m.EventID != "e1" || string(m.Data) != `{"n":1}` {
		t.Fatalf("event: %+v", m)
	}
	writeMsg(t, ctx, c, outMsg{Type: "unsubscribe", ID: "s1"})
	m = readMsg(t, ctx, c)
	if m.Type != "response" || m.ID != "s1" || m.Status != 200 {
		t.Fatalf("unsubscribe ack: %+v", m)
	}
	select {
	case <-producerDone:
	case <-ctx.Done():
		t.Fatal("producer not canceled")
	}
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "r2", Method: "GET", Path: "/api/v1/fast"})
	m = readMsg(t, ctx, c)
	if m.Type != "response" || m.ID != "r2" || m.Status != 200 {
		t.Fatalf("next response: %+v", m)
	}
}

func TestCancelBlocked(t *testing.T) {
	ctx := testCtx(t)
	slowStarted := make(chan struct{})
	slowDone := make(chan struct{})
	fc := &fakeCaller{call: func(ctx context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		if path == "/api/v1/slow" {
			close(slowStarted)
			<-ctx.Done()
			close(slowDone)
			return nil, ctx.Err()
		}
		return json.RawMessage(`{"ok":true}`), nil
	}}
	c, done := connect(t, ctx, SocketOptions{Caller: fc, InsecureSkipVerify: true}, nil)
	defer done()
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "S", Method: "GET", Path: "/api/v1/slow"})
	select {
	case <-slowStarted:
	case <-ctx.Done():
		t.Fatal("slow request did not start")
	}
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "F", Method: "GET", Path: "/api/v1/fast1"})
	for {
		m := readMsg(t, ctx, c)
		if m.ID == "F" {
			if m.Type != "response" || m.Status != 200 {
				t.Fatalf("fast response: %+v", m)
			}
			break
		}
	}
	writeMsg(t, ctx, c, outMsg{Type: "cancel", ID: "S"})
	for {
		m := readMsg(t, ctx, c)
		if m.ID == "S" && m.Type == "response" && m.Status == 200 {
			break
		}
	}
	select {
	case <-slowDone:
	case <-ctx.Done():
		t.Fatal("slow not canceled")
	}
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "F2", Method: "GET", Path: "/api/v1/fast2"})
	for {
		m := readMsg(t, ctx, c)
		if m.ID == "F2" {
			if m.Type != "response" || m.Status != 200 {
				t.Fatalf("post-cancel response: %+v", m)
			}
			break
		}
	}
}

func TestDuplicateID(t *testing.T) {
	ctx := testCtx(t)
	started := make(chan struct{}, 1)
	fc := &fakeCaller{call: func(ctx context.Context, method, path string, raw json.RawMessage) (json.RawMessage, error) {
		if path == "/api/v1/slow" {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return json.RawMessage(`{"ok":true}`), nil
	}}
	c, done := connect(t, ctx, SocketOptions{Caller: fc, InsecureSkipVerify: true}, nil)
	defer done()
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "dup", Method: "GET", Path: "/api/v1/slow"})
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("slow request did not start")
	}
	writeMsg(t, ctx, c, outMsg{Type: "request", ID: "dup", Method: "GET", Path: "/api/v1/fast"})
	m := readMsg(t, ctx, c)
	if m.Status != http.StatusConflict {
		t.Fatalf("duplicate id: %+v", m)
	}
}

func TestDisconnect(t *testing.T) {
	ctx := testCtx(t)
	canceled := make(chan struct{})
	fc := &fakeCaller{stream: func(ctx context.Context, path string, receive func(Event) error) error {
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	}}
	c, done := connect(t, ctx, SocketOptions{Caller: fc, InsecureSkipVerify: true}, nil)
	defer done()
	writeMsg(t, ctx, c, outMsg{Type: "subscribe", ID: "s", Path: "/api/v1/stream"})
	m := readMsg(t, ctx, c)
	if m.Type != "response" || m.ID != "s" || m.Status != 200 {
		t.Fatalf("ack: %+v", m)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("stream not canceled on disconnect")
	}
}

func TestHandshake(t *testing.T) {
	ctx := testCtx(t)
	t.Run("missing pot v1", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ServeWebSocket(w, r, SocketOptions{Caller: &fakeCaller{}, InsecureSkipVerify: true})
		}))
		defer srv.Close()
		hdr := http.Header{}
		hdr.Set("Origin", srv.URL)
		c, resp, err := websocket.Dial(ctx, wsURL(srv), &websocket.DialOptions{HTTPHeader: hdr})
		if err == nil {
			_ = c.CloseNow()
			t.Fatal("expected missing subprotocol rejection")
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	})
	t.Run("cross origin", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ServeWebSocket(w, r, SocketOptions{Caller: &fakeCaller{}, OriginPatterns: []string{"allowed.example"}})
		}))
		defer srv.Close()
		hdr := http.Header{}
		hdr.Set("Origin", "http://evil.example")
		c, resp, err := websocket.Dial(ctx, wsURL(srv), &websocket.DialOptions{Subprotocols: []string{"pot.v1"}, HTTPHeader: hdr})
		if err == nil {
			_ = c.CloseNow()
			t.Fatal("expected cross-origin rejection")
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	})
}
