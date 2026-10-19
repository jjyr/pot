package control

import (
	"context"
	"encoding/json"
	peertransport "github.com/jjyr/pot/pkg/peer"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

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
	if _, err = fixture.service.Registry.Create(context.Background(), "create-main", "Main"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func connectAgentControls(t *testing.T, a, b *agentControlFixture) agentlink.Connection {
	t.Helper()
	ctx := context.Background()
	if _, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "configure", AssistantID: "main", Mode: "address", Endpoint: b.endpoint}); err != nil {
		t.Fatal(err)
	}
	invitation, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite", AssistantID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	address := invitation.(map[string]any)["address"].(string)
	value, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "connect", AssistantID: "main", Address: address})
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
	peer := protocol.AgentRef{ControlID: "link-b", Kind: "assistant", AgentID: "main"}
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
		machines, err := item.local.Machines.Machines(ctx)
		if err != nil || len(machines) != 0 {
			t.Fatal("Agent Link created Runner membership", machines, err)
		}
		if item.local.Machines.Membership() != nil {
			t.Fatal("Agent Link changed Runtime ownership")
		}
		if _, _, err = item.local.Permissions.Policy(item.remote.options.RuntimeID); err == nil {
			t.Fatal("Agent Link granted execution policy")
		}
		port := controlplane.RemotePort{Directory: item.local.Machines, RuntimeID: item.remote.options.RuntimeID}
		if _, err = port.Launch(ctx, runner.Launch{}); err == nil {
			t.Fatal("Agent Link exposed remote execution")
		}
		peers, err := item.local.agentLinkPeers(ctx, "main")
		if err != nil || len(peers) != 1 || peers[0].Address.ControlID != item.remote.options.RuntimeID {
			t.Fatal(peers, err)
		}
		links, err := item.local.AgentLinks(ctx, "main")
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
	peer := protocol.AgentRef{ControlID: "binding-b", Kind: "assistant", AgentID: "main"}
	if _, err = a.service.sendAssistantMail(ctx, "wrong-local", other.ID, protocol.SendMail{To: peer, Text: "wrong local assistant"}); err == nil {
		t.Fatal("another local assistant used the binding")
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
	for _, service := range []string{controlplane.ExecutionService, "owner", "unknown"} {
		session, e := peertransport.Dial(ctx, b.endpoint, peertransport.DialOptions{Identity: identity, PrivateKey: key, Expected: &expected, Service: service, Auth: auth})
		if e == nil {
			session.Close()
			t.Fatal("message binding granted service authority", service)
		}
	}
	if _, err = a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "disconnect", ID: connection.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.service.sendAssistantMail(ctx, "after-revoke", "main", protocol.SendMail{To: peer, Text: "must stay local"}); err == nil {
		t.Fatal("revoked connection accepted new message")
	}
	if _, err = b.service.sendAssistantMail(ctx, "reverse-after-revoke", "main", protocol.SendMail{To: connection.Local.Address, Text: "must stay remote"}); err == nil {
		t.Fatal("remote side retained revoked communication")
	}
	for _, s := range []*Service{a.service, b.service} {
		peers, err := s.agentLinkPeers(ctx, "main")
		if err != nil || len(peers) != 0 {
			t.Fatal("revoked peer remains visible", peers, err)
		}
	}
}

func TestAgentLinkRunnerAndMissingAssistantCannotPublishIdentity(t *testing.T) {
	ctx := context.Background()
	owner := taskControl(t, "membership-owner", "")
	executionRuntime := taskControl(t, "execution-runtime", "")
	if _, err := executionRuntime.agentLinkIdentity(ctx, "main"); err == nil {
		t.Fatal("unconfigured assistant acquired a link identity")
	}
	if err := owner.Machines.EnableControl(); err != nil {
		t.Fatal(err)
	}
	server := controlPeerEndpoint(owner)
	invitation, err := owner.Machines.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	connector := controlplane.Connector{Directory: executionRuntime.Machines}
	identity, err := connector.Preview(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connector.Enroll(ctx, server, identity, invitation.Code, "join-runner"); err != nil {
		t.Fatal(err)
	}
	if executionRuntime.Machines.Role() != "runner" {
		t.Fatal("fixture did not establish real Runner membership")
	}
	for _, action := range []string{"invite", "connect", "configure", "disconnect"} {
		if _, err = executionRuntime.ManageAgentLink(ctx, AgentLinkCommand{Action: action, AssistantID: "main"}); err == nil {
			t.Fatal("Runner admitted Agent management", action)
		}
	}
	hostIdentity, key := owner.Machines.TransportIdentity()
	expected, _ := executionRuntime.Machines.TransportIdentity()
	if session, e := peertransport.Dial(ctx, controlPeerEndpoint(executionRuntime), peertransport.DialOptions{Identity: hostIdentity, PrivateKey: key, Expected: &expected, Service: agentlink.ServiceName, Auth: json.RawMessage(`{}`)}); e == nil {
		session.Close()
		t.Fatal("Runner exposed message service")
	}
}
