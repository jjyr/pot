package control

import (
	"context"
	"encoding/json"
	"errors"
	peertransport "github.com/jjyr/pot/pkg/peer"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

// Background observation shares the peer's one-operation admission limit.
// Retry only a confirmed capacity rejection, which precedes remote execution;
// retain the exact original request and return every other outcome unchanged.
func callAgentWhenReady(ctx context.Context, service *Service, owner string, operation agentlink.Operation, call agentlink.Call) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		value, err := service.CallAgent(ctx, owner, operation, call)
		var api protocol.APIError
		if !errors.Is(err, agentlink.ErrCapacity) && !(errors.As(err, &api) && api.Code == agentlink.ErrCapacity.Error()) {
			return value, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

type agentControlFixture struct {
	service  *Service
	endpoint string
}

func agentControl(t *testing.T, id string) *agentControlFixture {
	t.Helper()
	fixture := &agentControlFixture{}
	manager, err := execution.New(id, "boot-"+id, execution.Limits{MaxActive: 1, MaxQueued: 8})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture.service, err = Open(Options{Root: t.TempDir(), RuntimeID: id, BootID: "boot-" + id, Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	fixture.endpoint = controlPeerEndpoint(fixture.service)
	t.Cleanup(func() { fixture.service.Close() })
	if _, err = legacyMainAssistant(context.Background(), fixture.service, "create-main", "Main"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func connectAgentControls(t *testing.T, a, b *agentControlFixture) agentlink.Connection {
	t.Helper()
	ctx := context.Background()
	if _, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "configure", Mode: "address", Endpoint: b.endpoint}); err != nil {
		t.Fatal(err)
	}
	invitation, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite"})
	if err != nil {
		t.Fatal(err)
	}
	address := invitation.(map[string]any)["address"].(string)
	value, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "connect", Address: address})
	if err != nil {
		t.Fatal(err)
	}
	connection := value.(agentlink.Connection)
	if connection.State != "ready" || connection.LastSeen == nil {
		t.Fatalf("connection did not complete real exchange: %+v", connection)
	}
	return connection
}

func awaitAgentLinkMail(t *testing.T, s *Service, id string) protocol.MailRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	updates := s.Mailbox.Changes.Subscribe()
	defer updates.Close()
	for {
		records, err := s.Mailbox.History(ctx, "main", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Message.ID == id {
				return record
			}
		}
		if _, err = updates.Next(ctx); err != nil {
			t.Fatalf("message %s was not delivered by Control host: %v", id, err)
		}
	}
}

func TestAgentLinkControlsExchangeWithoutAppAndDoNotGrantExecution(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "link-a"), agentControl(t, "link-b")
	peer := protocol.AgentRef{NodeID: "link-b", Kind: "assistant", AgentID: "main"}
	if _, err := a.service.sendAssistantMail(ctx, "before-link", "main", protocol.SendMail{To: peer, Text: "not connected"}); err == nil {
		t.Fatal("remote communication admitted before connection")
	}
	connection := connectAgentControls(t, a, b)
	// There is no App or owner relay in this fixture. The Control's default
	// plugin host alone carries both directions through one published endpoint.
	first, err := a.service.sendAssistantMail(ctx, "a-to-b", "main", protocol.SendMail{To: peer, Text: "peer information only"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.service.sendAssistantMail(ctx, "b-to-a", "main", protocol.SendMail{To: first.From, Text: "independent remote message"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		s       *Service
		message protocol.Envelope
	}{{b.service, first}, {a.service, second}} {
		record := awaitAgentLinkMail(t, item.s, item.message.ID)
		if record.Message.Kind != "message" || record.Message.From != item.message.From || record.Message.To != item.message.To || record.Message.Text != item.message.Text || record.State != "queued" {
			t.Fatalf("message provenance or processing fact changed: %+v", record)
		}
		conversation, err := item.s.Conversation(ctx, "main")
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := conversation.Snapshot(ctx)
		if err != nil || len(snapshot.Messages) != 0 || snapshot.Turn != nil {
			t.Fatal("peer delivery entered user chat", snapshot, err)
		}
	}
	for _, item := range []struct{ local, remote *Service }{{a.service, b.service}, {b.service, a.service}} {
		if _, _, err = item.local.Permissions.Policy(item.remote.options.RuntimeID); err == nil {
			t.Fatal("Agent Link granted execution policy")
		}
		peers, err := item.local.agentLinkPeers(ctx)
		if err != nil || len(peers) != 1 || peers[0].Address.NodeID != item.remote.options.RuntimeID {
			t.Fatal(peers, err)
		}
		links, err := item.local.AgentLinks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(links)
		if strings.Contains(string(raw), "secret_ref") || strings.Contains(string(raw), "invitation_token") || strings.Contains(string(raw), "access_token") {
			t.Fatal("connection list exposed credential material")
		}
	}
	remote, err := b.service.agentLinks.service.Get(ctx, connection.ID)
	if err != nil || remote.Endpoint != "" {
		t.Fatal("reverse callback address unexpectedly required", remote, err)
	}
}

func TestAgentLinkControlBindingRevocationAndNarrowListener(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "binding-a"), agentControl(t, "binding-b")
	connection := connectAgentControls(t, a, b)
	other, err := a.service.Registry.Create(ctx, "other-assistant", "Other")
	if err != nil {
		t.Fatal(err)
	}
	peer := protocol.AgentRef{NodeID: "binding-b", Kind: "assistant", AgentID: "main"}
	if _, err = a.service.sendAssistantMail(ctx, "another-local", other.ID, protocol.SendMail{To: peer, Text: "another local assistant"}); err != nil {
		t.Fatal("node link excluded another local assistant", err)
	}
	wrongPeer := peer
	wrongPeer.AgentID = "other"
	if _, err = a.service.sendAssistantMail(ctx, "wrong-peer", "main", protocol.SendMail{To: wrongPeer, Text: "wrong target assistant"}); err == nil {
		t.Fatal("binding reached another remote assistant")
	}
	// An authenticated Agent identity and valid message binding cannot enter
	// membership, execution or owner services on the shared listener.
	identity, key := a.service.Machines.TransportIdentity()
	expected, _ := b.service.Machines.TransportIdentity()
	auth, _ := json.Marshal(map[string]string{"id": connection.ID})
	for _, service := range []string{"execution", "owner", "unknown"} {
		session, e := peertransport.Dial(ctx, b.endpoint, peertransport.DialOptions{Identity: identity, PrivateKey: key, Expected: &expected, Service: service, Auth: auth})
		if e == nil {
			session.Close()
			t.Fatal("message binding granted service authority", service)
		}
	}
	if _, err = a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "delete", ID: connection.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.service.sendAssistantMail(ctx, "after-revoke", "main", protocol.SendMail{To: peer, Text: "must stay local"}); err == nil {
		t.Fatal("revoked connection accepted new message")
	}
	if _, err = b.service.sendAssistantMail(ctx, "reverse-after-revoke", "main", protocol.SendMail{To: connection.Local.Address, Text: "must stay remote"}); err == nil {
		t.Fatal("remote side retained revoked communication")
	}
	for _, s := range []*Service{a.service, b.service} {
		peers, err := s.agentLinkPeers(ctx)
		if err != nil || len(peers) != 0 {
			t.Fatal("revoked peer remains visible", peers, err)
		}
	}
}

func TestMissingAssistantCannotPublishIdentity(t *testing.T) {
	s := taskControl(t, "empty-node", "")
	if _, err := s.agentLinkIdentity(context.Background(), "main"); err == nil {
		t.Fatal("missing assistant published identity")
	}
}

func TestAgentLinkControlPreviewDoesNotClaimInvitation(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "preview-a"), agentControl(t, "preview-b")
	if _, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "configure", Mode: "address", Endpoint: b.endpoint}); err != nil {
		t.Fatal(err)
	}
	invitation, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite"})
	if err != nil {
		t.Fatal(err)
	}
	invite := invitation.(map[string]any)
	address := invite["address"].(string)
	value, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "preview", Address: address})
	if err != nil {
		t.Fatal(err)
	}
	preview := value.(map[string]any)
	if len(preview) != 4 || !reflect.DeepEqual(preview["peer"], map[string]any{"id": "preview-b", "node_id": "preview-b", "kind": "pot", "name": "Pot"}) || preview["endpoint"] != b.endpoint || preview["expires_at_ms"] != invite["expires_at_ms"] {
		t.Fatalf("unexpected preview projection: %#v", preview)
	}
	fingerprint, ok := preview["fingerprint"].(string)
	if !ok || !strings.HasPrefix(fingerprint, "SHA256:") || len(fingerprint) != 50 {
		t.Fatal("missing host fingerprint", fingerprint)
	}
	for _, service := range []*Service{a.service, b.service} {
		links, err := service.agentLinks.service.List(ctx)
		if err != nil || len(links) != 0 {
			t.Fatal("preview created a connection", links, err)
		}
	}
	var count, claimed int
	if err := b.service.integrationDB.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(CASE WHEN claim_hash!='' OR connection_id!='' THEN 1 ELSE 0 END),0) FROM agent_link_invitations").Scan(&count, &claimed); err != nil || count != 1 || claimed != 0 {
		t.Fatal("preview consumed its invitation", count, claimed, err)
	}
	if _, err = a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "connect", Address: address}); err != nil {
		t.Fatal("preview prevented subsequent connection", err)
	}
	_, err = a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "preview", Address: address})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != "agent_link_invitation_used" {
		t.Fatal("used invitation did not expose an actionable error", err)
	}
}

func TestAgentLinkControlPreviewExpiredInvitation(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "expired-a"), agentControl(t, "expired-b")
	if _, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "configure", Mode: "address", Endpoint: b.endpoint}); err != nil {
		t.Fatal(err)
	}
	invitation, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.service.integrationDB.ExecContext(ctx, "UPDATE agent_link_invitations SET expires_at=?", time.Now().Add(-time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	_, err = a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "preview", Address: invitation.(map[string]any)["address"].(string)})
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != "agent_link_invitation_expired" {
		t.Fatal("expired invitation did not expose an actionable error", err)
	}
}
