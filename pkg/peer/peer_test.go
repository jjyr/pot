package peer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testIdentity(t *testing.T, id string) (Identity, ed25519.PrivateKey) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return NewIdentity(id, pub), key
}
func testServer(t *testing.T, authorize Authorizer, attached func(*Session)) (string, Identity, *Server) {
	t.Helper()
	id, key := testIdentity(t, "server")
	server := &Server{Identity: id, PrivateKey: key, Authorize: authorize, OnSession: attached}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		_ = l.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down")
		}
	})
	return "tcp://" + l.Addr().String(), id, server
}
func TestDuplexPinnedSessionLargeMessagesAndError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientID, key := testIdentity(t, "client")
	accepted := make(chan *Session, 1)
	endpoint, serverID, _ := testServer(t, func(ctx context.Context, remote Identity, service string, auth json.RawMessage) (Handler, error) {
		if remote != clientID || service != "mail" || string(auth) != `{"connection":"bound"}` {
			return nil, ErrUnauthorized
		}
		return func(ctx context.Context, r Request) (json.RawMessage, error) {
			if r.Method == "fail" {
				return nil, &RemoteError{Message: "denied", Code: "scope", Data: json.RawMessage(`{"reason":"binding"}`)}
			}
			return r.Params, nil
		}, nil
	}, func(s *Session) { accepted <- s })
	dialCtx, finishDial := context.WithCancel(ctx)
	client, err := Dial(dialCtx, endpoint, DialOptions{Identity: clientID, PrivateKey: key, Expected: &serverID, Service: "mail", Auth: json.RawMessage(`{"connection":"bound"}`), Handler: func(ctx context.Context, r Request) (json.RawMessage, error) { return r.Params, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	finishDial() // Cancelling establishment must not destroy the live connection.
	server := <-accepted
	large, _ := json.Marshal(strings.Repeat("数据", 400000))
	out, err := client.Call(ctx, Request{"large", "echo", large})
	if err != nil || !bytes.Equal(out, large) {
		t.Fatalf("large roundtrip: %v (%d)", err, len(out))
	}
	out, err = server.Call(ctx, Request{"reverse", "echo", json.RawMessage(`{"ok":true}`)})
	if err != nil || string(out) != `{"ok":true}` {
		t.Fatalf("reverse: %s %v", out, err)
	}
	_, err = client.Call(ctx, Request{"fail", "fail", json.RawMessage("null")})
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "scope" || string(remote.Data) != `{"reason":"binding"}` {
		t.Fatalf("lost service fault: %v", err)
	}
	if err = client.Notify(ctx, Request{"effect", "execute", json.RawMessage("null")}); err == nil {
		t.Fatal("effect allowed as notification")
	}
}
func TestWrongPinAndServiceCannotAuthorize(t *testing.T) {
	var admissions atomic.Int32
	endpoint, serverID, _ := testServer(t, func(ctx context.Context, remote Identity, service string, auth json.RawMessage) (Handler, error) {
		admissions.Add(1)
		if service != "mail" {
			return nil, ErrUnauthorized
		}
		return nil, nil
	}, nil)
	client, key := testIdentity(t, "client")
	wrong, _ := testIdentity(t, "server")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Dial(ctx, endpoint, DialOptions{Identity: client, PrivateKey: key, Expected: &wrong, Service: "mail"}); err == nil {
		t.Fatal("wrong key accepted")
	}
	if admissions.Load() != 0 {
		t.Fatal("admitted before key confirmation")
	}
	if _, err := Dial(ctx, endpoint, DialOptions{Identity: client, PrivateKey: key, Expected: &serverID, Service: "execution"}); err == nil {
		t.Fatal("unregistered service authorized")
	}
	if admissions.Load() != 1 {
		t.Fatal("missing service admission")
	}
}

func TestAdmissionHasHandshakeDeadline(t *testing.T) {
	checked := make(chan bool, 1)
	endpoint, serverID, _ := testServer(t, func(ctx context.Context, remote Identity, service string, auth json.RawMessage) (Handler, error) {
		deadline, ok := ctx.Deadline()
		checked <- ok && time.Until(deadline) > 0 && time.Until(deadline) <= 10*time.Second
		return nil, ErrUnauthorized
	}, nil)
	client, key := testIdentity(t, "client")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Dial(ctx, endpoint, DialOptions{Identity: client, PrivateKey: key, Expected: &serverID, Service: "mail"})
	if err == nil {
		t.Fatal("rejected admission accepted")
	}
	select {
	case bounded := <-checked:
		if !bounded {
			t.Fatal("admission lacks bounded context")
		}
	case <-ctx.Done():
		t.Fatal("admission not called")
	}
}
func TestUnknownOutcomeIsNotRetried(t *testing.T) {
	var executions atomic.Int32
	endpoint, serverID, _ := testServer(t, func(ctx context.Context, remote Identity, service string, auth json.RawMessage) (Handler, error) {
		return func(ctx context.Context, r Request) (json.RawMessage, error) {
			executions.Add(1)
			<-ctx.Done()
			return nil, ctx.Err()
		}, nil
	}, nil)
	client, key := testIdentity(t, "client")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s, err := Dial(ctx, endpoint, DialOptions{Identity: client, PrivateKey: key, Expected: &serverID, Service: "execution"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	callCtx, stop := context.WithTimeout(ctx, 40*time.Millisecond)
	defer stop()
	_, err = s.Call(callCtx, Request{"original-run", "execute", json.RawMessage("null")})
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("expected uncertain: %v", err)
	}
	if executions.Load() != 1 {
		t.Fatalf("sent %d times", executions.Load())
	}
}

func TestLateResponseCannotCompleteAnotherAttemptWithSameRequestID(t *testing.T) {
	serverID, _ := testIdentity(t, "server")
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := newSession(ctx, a, serverID, "execution", nil, nil)
	client.start()
	defer client.Close()
	finished := make(chan error, 1)
	go func() {
		var requests [2]frame
		for index := range requests {
			raw, err := readPacket(b, MaxFrame)
			if err != nil {
				finished <- err
				return
			}
			if err = decode(raw, &requests[index]); err != nil {
				finished <- err
				return
			}
		}
		for index, request := range requests {
			value := json.RawMessage(`"old"`)
			if index == 1 {
				value = json.RawMessage(`"new"`)
			}
			raw, _ := json.Marshal(frame{Kind: "response", ID: request.ID, Correlation: request.Correlation, Method: request.Method, Params: value})
			if err := writePacket(b, raw, MaxFrame); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	firstCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err := client.Call(firstCtx, Request{"same-id", "lookup", json.RawMessage(`"old-input"`)})
	stop()
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("first result should be uncertain: %v", err)
	}
	result, err := client.Call(ctx, Request{"same-id", "lookup", json.RawMessage(`"new-input"`)})
	if err != nil || string(result) != `"new"` {
		t.Fatalf("late response crossed attempts: %s %v", result, err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
}
func TestFrameBoundsAndTruncation(t *testing.T) {
	for _, size := range []uint32{0, MaxFrame + 1} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], size)
		if _, err := readPacket(bytes.NewReader(header[:]), MaxFrame); err == nil {
			t.Fatalf("accepted size %d", size)
		}
	}
	var raw bytes.Buffer
	if err := writePacket(&raw, []byte("hello"), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := readPacket(bytes.NewReader(raw.Bytes()[:raw.Len()-1]), 10); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated: %v", err)
	}
}
func TestEndpointPolicy(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:8793", "tcp://8.8.8.8:80", "tcp://0.0.0.0:42", "tcp://user@127.0.0.1:1", "tcp://127.0.0.1:1/path", "tcp://127.0.0.1:1?query", "tcp://127.0.0.1:1#secret", "tcp://127.0.0.1:0", "tcp://[fe80::1%25en0]:42"} {
		if _, err := ValidateEndpoint(context.Background(), endpoint); err == nil {
			t.Errorf("accepted %s", endpoint)
		}
	}
	for _, endpoint := range []string{"tcp://127.0.0.1:8793", "tcp://100.64.0.1:8793", "tcp://192.168.1.2:42", "tcp://[::1]:42", "tcp://[fd00::123]:42"} {
		if _, err := ValidateEndpoint(context.Background(), endpoint); err != nil {
			t.Errorf("rejected %s: %v", endpoint, err)
		}
	}
}

type observedConn struct {
	net.Conn
	once    sync.Once
	writing chan struct{}
}

func (c *observedConn) Write(raw []byte) (int, error) {
	if len(raw) > 1024*1024 {
		c.once.Do(func() { close(c.writing) })
	}
	return c.Conn.Write(raw)
}
func duplexWithQueuedHeartbeats(t *testing.T, pings bool) (error, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	left, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	right, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	defer right.Close()
	left.(*net.TCPConn).SetWriteBuffer(4096)
	right.(*net.TCPConn).SetWriteBuffer(4096)
	a, ak := testIdentity(t, "a")
	b, bk := testIdentity(t, "b")
	clientConfig, err := tlsConfig(a, ak, &b, false)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig, err := tlsConfig(b, bk, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	securedLeft, securedRight := tls.Client(left, clientConfig), tls.Server(right, serverConfig)
	handshakeCtx, stopHandshake := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopHandshake()
	ready := make(chan error, 1)
	go func() { ready <- securedRight.HandshakeContext(handshakeCtx) }()
	if err = securedLeft.HandshakeContext(handshakeCtx); err != nil {
		t.Fatal(err)
	}
	if err = <-ready; err != nil {
		t.Fatal(err)
	}
	l := &observedConn{Conn: securedLeft, writing: make(chan struct{})}
	r := &observedConn{Conn: securedRight, writing: make(chan struct{})}
	handler := func(context.Context, Request) (json.RawMessage, error) { return json.RawMessage(`{"ok":true}`), nil }
	sa := newSession(context.Background(), l, b, "svc", nil, handler)
	sb := newSession(context.Background(), r, a, "svc", nil, handler)
	defer sa.Close()
	defer sb.Close()
	if pings {
		if err = sa.send(context.Background(), frame{Kind: "ping"}); err != nil {
			t.Fatal(err)
		}
		if err = sb.send(context.Background(), frame{Kind: "ping"}); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(strings.Repeat("x", 2*1024*1024))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	results := make(chan error, 2)
	go func() { _, err := sa.Call(ctx, Request{"a", "echo", raw}); results <- err }()
	go func() { _, err := sb.Call(ctx, Request{"b", "echo", raw}); results <- err }()
	select {
	case <-l.writing:
	case <-time.After(time.Second):
		t.Fatal("left did not write")
	}
	select {
	case <-r.writing:
	case <-time.After(time.Second):
		t.Fatal("right did not write")
	}
	sa.start()
	sb.start()
	return <-results, <-results
}

func TestHeartbeatDoesNotBlockDuplexLargeWrites(t *testing.T) {
	first, second := duplexWithQueuedHeartbeats(t, true)
	if first != nil || second != nil {
		t.Fatalf("heartbeat blocked duplex writes: %v, %v", first, second)
	}
}

func TestPeerNodeIdentityRejectsOldWireFormat(t *testing.T) {
	public, _, _ := ed25519.GenerateKey(rand.Reader)
	identity := NewIdentity("logical-node", public)
	raw, err := json.Marshal(identity)
	if err != nil || identity.NodeID != "logical-node" || !strings.Contains(string(raw), `"node_id":"logical-node"`) || strings.Contains(string(raw), `"id":`) {
		t.Fatal(string(raw), err)
	}
	var old Identity
	if err = decode([]byte(`{"id":"logical-node","public_key":"`+identity.PublicKey+`"}`), &old); err == nil {
		t.Fatal("old peer identity accepted")
	}
	for _, version := range []int{1, 2} {
		if validHello(hello{version, identity, nil, "message", json.RawMessage("null")}) {
			t.Fatal("old handshake version accepted", version)
		}
	}
}
