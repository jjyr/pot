package agentlink

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/peer"
)

func previewCounts(t *testing.T, f *fixture) []int {
	t.Helper()
	result := []int{}
	for _, query := range []string{`SELECT COUNT(*) FROM agent_link_connections`, `SELECT COUNT(*) FROM secrets`, `SELECT COUNT(*) FROM agent_link_invitations WHERE claim_hash!=''`} {
		var count int
		if err := f.db.QueryRow(query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result = append(result, count)
	}
	return result
}

func TestPreviewVerifiesLiveIdentityWithoutClaimOrPersistentState(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "preview-a"), newFixture(t, "preview-b")
	invitation, err := b.service.Invite(ctx, "assistant", b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	beforeA, beforeB := previewCounts(t, a), previewCounts(t, b)
	// A copied name is display data, not authoritative. The card must contain
	// the identity's current name read from the actual authenticated endpoint.
	original := b.service.options.Identity
	b.service.options.Identity = func(ctx context.Context, id string) (Identity, error) {
		identity, err := original(ctx, id)
		identity.Name = "Current remote name"
		return identity, err
	}
	for range 2 {
		result, err := a.service.Preview(ctx, invitation.Address)
		if err != nil || result.Identity.Name != "Current remote name" || result.Identity.Address != b.identity().Address || result.Host != b.service.options.HostIdentity || result.Endpoint != b.endpoint || result.Fingerprint != hostFingerprint(b.service.options.HostIdentity) || !strings.HasPrefix(result.Fingerprint, "SHA256:") {
			t.Fatal(result, err)
		}
	}
	for index, side := range []*fixture{a, b} {
		before := [][]int{beforeA, beforeB}[index]
		after := previewCounts(t, side)
		for j := range before {
			if before[j] != after[j] {
				t.Fatal("preview changed persisted state", before, after)
			}
		}
		side.service.mu.Lock()
		count := len(side.service.sessions)
		side.service.mu.Unlock()
		if count != 0 {
			t.Fatal("preview attached a durable link session")
		}
	}
	// Reading the card leaves the same invitation available for the one real add.
	connection, err := a.service.Connect(ctx, "assistant", invitation.Address)
	if err != nil || connection.State != "ready" {
		t.Fatal(connection, err)
	}
	if _, err = a.service.Preview(ctx, invitation.Address); !errors.Is(err, ErrUsed) {
		t.Fatal("claimed address not clearly rejected", err)
	}
}

func TestPreviewRejectsExpiredInvalidAndDifferentTargetAddresses(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "preview-a"), newFixture(t, "preview-b")
	invitation, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	value, _ := parseAddress(ctx, invitation.Address)
	if _, err := b.db.Exec(`UPDATE agent_link_invitations SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UnixMilli(), value.Invitation); err != nil {
		t.Fatal(err)
	}
	if _, err := a.service.Preview(ctx, invitation.Address); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	fresh, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	value, _ = parseAddress(ctx, fresh.Address)
	for _, change := range []func(*address){func(v *address) { v.Code = token() }, func(v *address) { v.Identity.Address.AgentID = "other" }, func(v *address) { v.Host = a.service.options.HostIdentity }} {
		changed := value
		change(&changed)
		raw, _ := json.Marshal(changed)
		input := b.endpoint + "#" + base64.RawURLEncoding.EncodeToString(raw)
		if _, err := a.service.Preview(ctx, input); err == nil {
			t.Fatal("unverified target was previewed")
		}
	}
	if _, err := a.service.Preview(ctx, strings.Repeat("x", 16385)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestPreviewPinsActualServerKeyAndHasNoOtherServiceAuthority(t *testing.T) {
	ctx := context.Background()
	a, b, other := newFixture(t, "preview-a"), newFixture(t, "preview-b"), newFixture(t, "another-host")
	invitation, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	value, _ := parseAddress(ctx, invitation.Address)
	value.Endpoint = other.endpoint
	raw, _ := json.Marshal(value)
	if _, err := a.service.Preview(ctx, other.endpoint+"#"+base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("endpoint key mismatch was accepted", err)
	}
	value, _ = parseAddress(ctx, invitation.Address)
	auth, _ := json.Marshal(sessionAuth{Preview: &previewRequest{Invitation: value.Invitation, Target: value.Identity}})
	session, err := peer.Dial(ctx, b.endpoint, peer.DialOptions{Identity: a.service.options.HostIdentity, PrivateKey: a.key, Expected: &value.Host, Service: ServiceName, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, method := range []string{"identity", "exchange", "revoke", "execute", "pair"} {
		if _, err = session.Call(ctx, peer.Request{ID: newID(), Method: method, Params: json.RawMessage(`{}`)}); err == nil {
			t.Fatal("preview session acquired other method", method)
		}
	}
	if _, err = session.Call(ctx, peer.Request{ID: newID(), Method: "preview", Params: json.RawMessage(`{"extra":true}`)}); err == nil {
		t.Fatal("expanded preview request accepted")
	}
	// Admission must still be current when the card is read, not just at handshake.
	if _, err = b.db.Exec(`UPDATE agent_link_invitations SET claim_hash='used' WHERE id=?`, value.Invitation); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(struct {
		Code string `json:"code"`
	}{Code: value.Code})
	if _, err = session.Call(ctx, peer.Request{ID: newID(), Method: "preview", Params: params}); err == nil || transportError(err) != ErrUsed {
		t.Fatal("stale preview admission was reused", err)
	}
}

func TestPreviewAndConnectDoNotRevealCodesBeforeServerKeyProof(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "private-a"), newFixture(t, "private-b")
	invitation, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	original, _ := parseAddress(ctx, invitation.Address)
	for _, operation := range []string{"preview", "connect"} {
		t.Run(operation, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			captured := make(chan []byte, 1)
			go func() {
				connection, err := listener.Accept()
				if err != nil {
					return
				}
				defer connection.Close()
				connection.SetDeadline(time.Now().Add(time.Second))
				var header [4]byte
				if _, err = io.ReadFull(connection, header[:]); err != nil {
					return
				}
				size := binary.BigEndian.Uint32(header[:])
				if size > 65536 {
					return
				}
				raw := make([]byte, size)
				if _, err = io.ReadFull(connection, raw); err == nil {
					captured <- raw
				}
			}()
			value := original
			value.Endpoint = "tcp://" + listener.Addr().String()
			raw, _ := json.Marshal(value)
			address := value.Endpoint + "#" + base64.RawURLEncoding.EncodeToString(raw)
			if operation == "preview" {
				_, err = a.service.Preview(ctx, address)
			} else {
				_, err = a.service.Connect(ctx, "assistant", address)
			}
			if err == nil {
				t.Fatal("unauthenticated endpoint accepted")
			}
			select {
			case hello := <-captured:
				if strings.Contains(string(hello), original.Code) {
					t.Fatal("initial hello revealed invitation capability to unverified host")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no captured hello")
			}
		})
	}
}

func TestPendingPairSessionRequiresCodeBeforeClaimOrMessaging(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "pending-a"), newFixture(t, "pending-b")
	invitation, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	value, _ := parseAddress(ctx, invitation.Address)
	metadata := pairRequest{ID: newID(), Invitation: value.Invitation, Local: a.identity(), Peer: value.Identity}
	auth, _ := json.Marshal(sessionAuth{ID: metadata.ID, Pair: &metadata})
	session, err := peer.Dial(ctx, b.endpoint, peer.DialOptions{Identity: a.service.options.HostIdentity, PrivateKey: a.key, Expected: &value.Host, Service: ServiceName, Auth: auth})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if counts := previewCounts(t, b); counts[0] != 0 || counts[2] != 0 {
		t.Fatal("handshake claimed invitation", counts)
	}
	if _, err = session.Call(ctx, peer.Request{ID: newID(), Method: "exchange", Params: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("unclaimed pair session gained message access")
	}
	wrong, _ := json.Marshal(struct {
		Code string `json:"code"`
	}{Code: token()})
	if _, err = session.Call(ctx, peer.Request{ID: newID(), Method: "pair", Params: wrong}); err == nil {
		t.Fatal("wrong code claimed binding")
	}
	correct, _ := json.Marshal(struct {
		Code string `json:"code"`
	}{Code: value.Code})
	if _, err = session.Call(ctx, peer.Request{ID: newID(), Method: "pair", Params: correct}); err != nil {
		t.Fatal(err)
	}
	if counts := previewCounts(t, b); counts[0] != 1 || counts[2] != 1 {
		t.Fatal("correct code did not claim exactly one binding", counts)
	}
}

func TestPreviewHonorsCallerDeadlineAgainstUnresponsiveEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	a, b := newFixture(t, "preview-a"), newFixture(t, "preview-b")
	invitation, _ := b.service.Invite(context.Background(), "assistant", b.endpoint)
	value, _ := parseAddress(context.Background(), invitation.Address)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			accepted <- connection
		}
	}()
	value.Endpoint = "tcp://" + listener.Addr().String()
	raw, _ := json.Marshal(value)
	started := time.Now()
	if _, err = a.service.Preview(ctx, value.Endpoint+"#"+base64.RawURLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("unresponsive server was previewed")
	}
	if time.Since(started) > time.Second {
		t.Fatal("preview ignored caller deadline")
	}
	select {
	case connection := <-accepted:
		connection.Close()
	default:
	}
}

func TestHistoricalClaimDigestRetainsOriginalNamesAndCapability(t *testing.T) {
	a, b := newFixture(t, "claim-a"), newFixture(t, "claim-b")
	ctx := context.Background()
	local, _ := a.service.options.Identity(ctx, "assistant")
	remote, _ := b.service.options.Identity(ctx, "assistant")
	request := pairRequest{ID: newID(), Invitation: newID(), Local: local, Peer: remote, Code: token()}
	// Derive the actual previous-wire bytes independently of the frozen codec.
	oldPair, _ := json.Marshal(request)
	oldPair = []byte(strings.ReplaceAll(string(oldPair), `"node_id":`, `"control_id":`))
	oldHost, _ := json.Marshal(a.service.options.HostIdentity)
	oldHost = []byte(strings.ReplaceAll(string(oldHost), `"node_id":`, `"id":`))
	expected := digest(append(append(append([]byte("["), oldPair...), ','), append(oldHost, ']')...))
	actual, err := claimDigest(request, a.service.options.HostIdentity, 1)
	if err != nil || actual != expected {
		t.Fatal(actual, expected, err)
	}
	for _, change := range []string{"name", "code", "sender", "host"} {
		modified := request
		host := a.service.options.HostIdentity
		switch change {
		case "name":
			modified.Peer.Name = "different display name"
		case "code":
			modified.Code = token()
		case "sender":
			modified.Local.Address.AgentID = "other"
		case "host":
			host.PublicKey = b.service.options.HostIdentity.PublicKey
		}
		value, err := claimDigest(modified, host, 1)
		if err != nil || value == expected {
			t.Fatal("historical claim weakened "+change, value, err)
		}
	}
}
