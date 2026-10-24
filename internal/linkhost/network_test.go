package linkhost

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/peer"
)

func TestPeerRegistryRejectsUnknownServiceAndIdentityMethod(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 5*time.Second))
	defer cancel()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity := peer.NewIdentity("host", public)
	registry := NewRegistry(identity, key)
	listener, err := ListenPeer(ctx, "127.0.0.1:0", registry.Server())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	endpoint := "tcp://" + listener.Addr()
	if err = Probe(ctx, endpoint, identity, key, identity); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"owner", "execution", "missing"} {
		session, err := peer.Dial(ctx, endpoint, peer.DialOptions{Identity: identity, PrivateKey: key, Expected: &identity, Service: service})
		if err == nil {
			session.Close()
			t.Fatal("unregistered service accepted", service)
		}
	}
	session, err := peer.Dial(ctx, endpoint, peer.DialOptions{Identity: identity, PrivateKey: key, Expected: &identity, Service: "identity"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err = session.Call(ctx, peer.Request{ID: "unknown", Method: "execute", Params: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("identity service admitted unknown operation")
	}
	raw, err := session.Call(ctx, peer.Request{ID: "identity", Method: "identity", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	var got peer.Identity
	if json.Unmarshal(raw, &got) != nil || got != identity {
		t.Fatal("identity changed", string(raw))
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done():
	case <-ctx.Done():
		t.Fatal("listener shutdown left authenticated session live")
	}
}
