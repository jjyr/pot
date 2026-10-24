package agentlink

import (
	"bytes"
	"context"
	"github.com/jjyr/pot/internal/testutil"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type wireCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *wireCapture) Write(raw []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(raw)
}

func TestTLSPreviewPairAndMessagesDoNotExposeCapabilitiesOnWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 10*time.Second))
	defer cancel()
	a, b := newFixture(t, "tls-a"), newFixture(t, "tls-b")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wire wireCapture
	var mu sync.Mutex
	var connections []net.Conn
	var workers sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", b.listener.Addr().String())
			if err != nil {
				client.Close()
				continue
			}
			mu.Lock()
			connections = append(connections, client, server)
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				done := make(chan struct{})
				go func() {
					_, _ = io.Copy(io.MultiWriter(client, &wire), server)
					_ = client.Close()
					close(done)
				}()
				_, _ = io.Copy(io.MultiWriter(server, &wire), client)
				_ = server.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-acceptDone
		mu.Lock()
		for _, conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	invitation, err := b.service.Invite(ctx, "tcp://"+listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	address, err := parseAddress(ctx, invitation.Address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.service.Preview(ctx, invitation.Address); err != nil {
		t.Fatal(err)
	}
	// This is the vulnerable window in the old signed-but-cleartext protocol:
	// Preview has sent the capability, while the invitation remains unused.
	if counts := previewCounts(t, b); counts[0] != 0 || counts[2] != 0 {
		t.Fatal("preview claimed invitation", counts)
	}
	wire.mu.Lock()
	leaked := bytes.Contains(wire.buffer.Bytes(), []byte(address.Code))
	wire.mu.Unlock()
	if leaked {
		t.Fatal("preview exposed the still-unused capability to the TCP relay")
	}
	connection, err := a.service.Connect(ctx, invitation.Address)
	if err != nil || connection.State != "ready" {
		t.Fatal(connection, err)
	}
	const outgoing = "private-message-from-tls-a"
	const incoming = "private-message-from-tls-b"
	send(t, a, b, "tls-outgoing", outgoing)
	send(t, b, a, "tls-incoming", incoming)
	if err = a.service.Sync(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	if err = b.service.Sync(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, a, "tls-incoming") != 1 || mailCount(t, b, "tls-outgoing") != 1 {
		t.Fatal("relay interrupted bidirectional message delivery")
	}
	wire.mu.Lock()
	defer wire.mu.Unlock()
	for _, secret := range []string{address.Code, outgoing, incoming} {
		if bytes.Contains(wire.buffer.Bytes(), []byte(secret)) {
			t.Fatal("TCP relay observed plaintext capability or message")
		}
	}
}
