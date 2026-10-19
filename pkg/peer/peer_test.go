package peer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
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
func TestHandshakeReplayRequiresFreshChallenge(t *testing.T) {
	client, key := testIdentity(t, "client")
	serverID, serverKey := testIdentity(t, "server")
	var admissions atomic.Int32
	server := &Server{Identity: serverID, PrivateKey: serverKey, Authorize: func(ctx context.Context, remote Identity, service string, auth json.RawMessage) (Handler, error) {
		admissions.Add(1)
		return nil, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var firstHello, firstConfirmation []byte
	for attempt := 0; attempt < 2; attempt++ {
		a, b := net.Pipe()
		done := make(chan error, 1)
		go func() {
			s, err := server.Accept(ctx, a)
			if s != nil {
				s.Close()
			}
			done <- err
		}()
		if attempt == 0 {
			h := hello{1, client, nonce(), &serverID, "mail", json.RawMessage("null")}
			firstHello, _ = seal(key, "pot-peer/client-hello/v1", h)
		}
		if err := writePacket(b, firstHello, maxHandshake); err != nil {
			t.Fatal(err)
		}
		ch, err := readPacket(b, maxHandshake)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			firstConfirmation, _ = seal(key, "pot-peer/client-confirm/v1", confirmation{hash(firstHello, ch)})
		}
		if err = writePacket(b, firstConfirmation, maxHandshake); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			if _, err = readPacket(b, maxHandshake); err != nil {
				t.Fatal(err)
			}
		} else if _, err = readPacket(b, maxHandshake); err == nil {
			t.Fatal("captured handshake replay accepted")
		}
		_ = b.Close()
		err = <-done
		if attempt == 0 && err != nil {
			t.Fatal(err)
		}
		if attempt == 1 && err == nil {
			t.Fatal("replayed confirmation authorized")
		}
	}
	if admissions.Load() != 1 {
		t.Fatalf("admission replayed: %d", admissions.Load())
	}
}
func TestFrameTamperingReflectionAndCrossSessionReplay(t *testing.T) {
	for _, kind := range []string{"signature", "sequence", "session", "direction", "replay"} {
		t.Run(kind, func(t *testing.T) {
			local, localKey := testIdentity(t, "local")
			remote, remoteKey := testIdentity(t, "remote")
			a, b := net.Pipe()
			defer b.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			delivered := make(chan struct{}, 2)
			s := newSession(ctx, a, local, localKey, remote, "fresh-session", "mail", nil, func(ctx context.Context, r Request) (json.RawMessage, error) {
				delivered <- struct{}{}
				return nil, nil
			}, "server", "client")
			s.start()
			defer s.Close()
			f := frame{Session: "fresh-session", Direction: "client", Sequence: 1, Kind: "event", ID: "1", Method: "event.changed", Params: json.RawMessage("null")}
			switch kind {
			case "sequence":
				f.Sequence = 2
			case "session":
				f.Session = "old-session"
			case "direction":
				f.Direction = "server"
			}
			raw, _ := seal(remoteKey, "pot-peer/frame/v1", f)
			if kind == "signature" {
				raw, _ = seal(localKey, "pot-peer/frame/v1", f)
			}
			if err := writePacket(b, raw, MaxFrame); err != nil {
				t.Fatal(err)
			}
			if kind == "replay" {
				select {
				case <-delivered:
				case <-ctx.Done():
					t.Fatal("initial event not accepted")
				}
				_ = writePacket(b, raw, MaxFrame)
			}
			select {
			case <-s.Done():
			case <-ctx.Done():
				t.Fatal("invalid frame did not close session")
			}
			select {
			case <-delivered:
				t.Fatal("invalid/replayed frame reached handler")
			default:
			}
		})
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
