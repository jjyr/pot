package consumer_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/peer"
)

func TestStandalonePeerService(t *testing.T) {
	serverPub, serverKey, _ := ed25519.GenerateKey(rand.Reader)
	clientPub, clientKey, _ := ed25519.GenerateKey(rand.Reader)
	serverID := peer.NewIdentity("external-server", serverPub)
	if serverID.NodeID != "external-server" {
		t.Fatal(serverID)
	}
	clientID := peer.NewIdentity("external-client", clientPub)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := peer.Server{Identity: serverID, PrivateKey: serverKey, Authorize: func(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
		if remote != clientID || service != "external-service" {
			return nil, peer.ErrUnauthorized
		}
		return func(ctx context.Context, r peer.Request) (json.RawMessage, error) { return r.Params, nil }, nil
	}}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(ctx, l) }()
	defer func() { cancel(); _ = l.Close(); <-done }()
	session, err := peer.Dial(ctx, "tcp://"+l.Addr().String(), peer.DialOptions{Identity: clientID, PrivateKey: clientKey, Expected: &serverID, Service: "external-service", Context: ctx})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	response, err := session.Call(ctx, peer.Request{ID: "external-call", Method: "echo", Params: json.RawMessage(`{"from":"external-module"}`)})
	if err != nil || string(response) != `{"from":"external-module"}` {
		t.Fatalf("standalone transport: %s %v", response, err)
	}
}
