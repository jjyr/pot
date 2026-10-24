package peer

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/testutil"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMalformedStreamDoesNotPoisonSession(t *testing.T) {
	var mu sync.Mutex
	received := make(map[string]bool)
	client, _ := sessionPair(t, func(_ context.Context, r Request) (json.RawMessage, error) {
		mu.Lock()
		received[r.ID] = true
		mu.Unlock()
		return r.Params, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 3*time.Second))
	defer cancel()
	for _, tc := range []struct {
		id     string
		header frame
		body   []byte
	}{
		{"wrong-kind", frame{Kind: "response", ID: "wrong-kind", Method: "echo", Size: 4, RawSize: 4}, []byte("null")},
		{"bad-json", frame{Kind: "request", ID: "bad-json", Method: "echo", Size: 4, RawSize: 4}, []byte("xxxx")},
		{"trailing", frame{Kind: "request", ID: "trailing", Method: "echo", Size: 4, RawSize: 4}, []byte("nullx")},
		{"truncated", frame{Kind: "request", ID: "truncated", Method: "echo", Size: 4, RawSize: 4}, []byte("nul")},
	} {
		stream, err := client.mux.OpenStream(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = stream.SetDeadline(time.Now().Add(time.Second))
		if err = writeAll(stream, messageBytes(t, tc.header, tc.body)); err != nil {
			t.Fatal(err)
		}
		_ = stream.CloseWrite()
		if _, err = stream.Read(make([]byte, 1)); err == nil || err == io.EOF {
			t.Fatalf("malformed stream not reset: %s %v", tc.id, err)
		}
		_ = stream.Close()
		out, err := client.Call(ctx, Request{"healthy", "echo", json.RawMessage(`true`)})
		if err != nil || string(out) != "true" {
			t.Fatalf("malformed stream poisoned session: %v", err)
		}
		mu.Lock()
		dispatched := received[tc.id]
		mu.Unlock()
		if dispatched {
			t.Fatal("malformed stream reached handler", tc.id)
		}
	}
}

func TestCapacityAndAbandonedWaitsDoNotCancelBusinessWork(t *testing.T) {
	started := make(chan struct{}, maxPending)
	finished := make(chan struct{}, maxPending)
	unblock := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(unblock) })
	client, server := sessionPair(t, func(ctx context.Context, r Request) (json.RawMessage, error) {
		if r.Method == "hold" {
			started <- struct{}{}
			select {
			case <-unblock:
			case <-ctx.Done():
			}
			finished <- struct{}{}
		}
		return r.Params, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 8*time.Second))
	defer cancel()
	calls, stop := context.WithCancel(ctx)
	defer stop()
	results := make(chan error, maxPending)
	for n := range maxPending {
		go func() {
			_, err := client.Call(calls, Request{fmt.Sprint(n), "hold", json.RawMessage("null")})
			results <- err
		}()
	}
	for range maxPending {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("requests did not start")
		}
	}
	for _, id := range []string{"0", "overflow"} {
		if _, err := client.Call(ctx, Request{id, "echo", json.RawMessage("null")}); err == nil || errors.Is(err, ErrUncertain) {
			t.Fatalf("unbounded/duplicate outgoing request: %v", err)
		}
	}
	stop()
	for range maxPending {
		if err := <-results; !errors.Is(err, ErrUncertain) {
			t.Fatalf("cancelled wait result: %v", err)
		}
	}
	select {
	case <-finished:
		t.Fatal("abandoned wait cancelled business work")
	default:
	}
	// Resetting abandoned streams frees stream IDs, but their running handlers
	// still occupy admission slots until the business operation returns.
	if _, err := client.Call(ctx, Request{"overload", "echo", json.RawMessage("null")}); !errors.Is(err, ErrUncertain) {
		t.Fatalf("inbound handler capacity bypassed: %v", err)
	}
	once.Do(func() { close(unblock) })
	for range maxPending {
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("business work did not finish")
		}
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for len(server.handlers) > 0 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("handler slots not released")
		}
	}
	if _, err := client.Call(ctx, Request{"recovered", "echo", json.RawMessage("null")}); err != nil {
		t.Fatal("session did not recover", err)
	}
}

func TestNotifyDeliversOnlyEventHint(t *testing.T) {
	events := make(chan Request, 1)
	client, _ := sessionPair(t, func(_ context.Context, r Request) (json.RawMessage, error) { events <- r; return nil, nil })
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 3*time.Second))
	defer cancel()
	if err := client.Notify(ctx, Request{"hint", "event.changed", json.RawMessage(`{"revision":2}`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-events:
		if r.Method != "event.changed" || string(r.Params) != `{"revision":2}` {
			t.Fatal(r)
		}
	case <-ctx.Done():
		t.Fatal("event not delivered")
	}
}

func BenchmarkTLSEcho(b *testing.B) {
	// Set up through ordinary public APIs, once per payload size. This includes
	// TLS and Yamux processing, excluding connection establishment.
	for _, size := range []int{256, 4096, 1024 * 1024} {
		text, _ := json.Marshal(strings.Repeat("document content ", size/17))
		random := make([]byte, size*3/4)
		if _, err := rand.Read(random); err != nil {
			b.Fatal(err)
		}
		binary, _ := json.Marshal(random)
		for _, tc := range []struct {
			name string
			body []byte
		}{{"compressible", text}, {"random", binary}} {
			b.Run(stringSize(size)+"/"+tc.name, func(b *testing.B) {
				id, key := testIdentity(b, "client")
				serverID, serverKey := testIdentity(b, "server")
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					b.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				server := &Server{Identity: serverID, PrivateKey: serverKey, Authorize: func(context.Context, Identity, string, json.RawMessage) (Handler, error) {
					return func(_ context.Context, r Request) (json.RawMessage, error) { return r.Params, nil }, nil
				}}
				done := make(chan struct{})
				go func() { defer close(done); _ = server.Serve(ctx, ln) }()
				defer func() { cancel(); _ = ln.Close(); <-done }()
				client, err := Dial(ctx, "tcp://"+ln.Addr().String(), DialOptions{Identity: id, PrivateKey: key, Expected: &serverID, Service: "echo"})
				if err != nil {
					b.Fatal(err)
				}
				defer client.Close()
				payload := tc.body
				b.ReportAllocs()
				b.SetBytes(int64(len(payload)))
				b.ResetTimer()
				for b.Loop() {
					if _, err := client.Call(ctx, Request{"echo", "echo", payload}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestInitialWindowMemoryDenialClosesExhaustedSession(t *testing.T) {
	client, _ := sessionPair(t, func(_ context.Context, r Request) (json.RawMessage, error) { return r.Params, nil })
	client.memory.mu.Lock()
	client.memory.limit = client.memory.used
	client.memory.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Call(ctx, Request{"denied", "echo", json.RawMessage("null")}); !errors.Is(err, errMemoryCapacity) {
		t.Fatal("memory limit bypassed", err)
	}
	select {
	case <-client.Done():
	case <-ctx.Done():
		t.Fatal("exhausted session retained SYN slots")
	}
}

func TestBoundedErrorsRemainValidCBORText(t *testing.T) {
	for _, err := range []error{
		errors.New(strings.Repeat("中", 2000)),
		&RemoteError{Message: strings.Repeat("中", 2000), Code: strings.Repeat("中", 100)},
		errors.New("bad\xfftext"),
	} {
		bounded := boundedError(err)
		raw, e := wireEncoding.Marshal(bounded)
		var result RemoteError
		if e != nil || decode(raw, &result) != nil || len(result.Message) > 4096 || len(result.Code) > 128 {
			t.Fatalf("error does not roundtrip: %v", e)
		}
	}
}
