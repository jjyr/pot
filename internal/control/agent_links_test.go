package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	service *Service
	server  *httptest.Server
}

func agentControl(t *testing.T, id string) *agentControlFixture {
	t.Helper()
	fixture := &agentControlFixture{}
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fixture.service.AgentLinkHandler().ServeHTTP(w, r) }))
	manager, err := execution.New(id, "boot-"+id, execution.Limits{MaxActive: 1, MaxQueued: 8})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture.service, err = Open(Options{Root: t.TempDir(), RuntimeID: id, BootID: "boot-" + id, Manager: manager, Keys: keys, ControlHTTPClient: fixture.server.Client()})
	if err != nil {
		fixture.server.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { fixture.server.Close(); fixture.service.Close() })
	if _, err = fixture.service.Registry.Create(context.Background(), "create-main", "Main"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func connectAgentControls(t *testing.T, a, b *agentControlFixture) agentlink.Connection {
	t.Helper()
	ctx := context.Background()
	if _, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "configure", AssistantID: "main", Mode: "https", Endpoint: b.server.URL}); err != nil {
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
	// Even a valid connection credential cannot access owner or execution APIs.
	var ref string
	if err = a.service.integrationDB.QueryRow(`SELECT secret_ref FROM agent_link_connections WHERE id=?`, connection.ID).Scan(&ref); err != nil {
		t.Fatal(err)
	}
	credential, err := a.service.integrationSecrets.Get(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(credential)
	var token struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(credential, &token); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/machines", "/api/v1/assistant-mail", "/api/v1/agent-links", "/api/v1/control"} {
		request, err := http.NewRequestWithContext(ctx, "POST", b.server.URL+path, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token.Token)
		response, err := b.server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatal("peer listener exposed another API", path, response.StatusCode)
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
	server := httptest.NewServer(owner.ControlServer(nil))
	defer server.Close()
	invitation, err := owner.Machines.Invitation(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	connector := controlplane.Connector{Directory: executionRuntime.Machines}
	identity, err := connector.Preview(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = connector.Enroll(ctx, server.URL, identity, invitation.Code, "join-runner"); err != nil {
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
	for _, path := range []string{agentlink.Prefix + "/ready", agentlink.Prefix + "/pair", agentlink.Prefix + "/exchange"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		executionRuntime.AgentLinkHandler().ServeHTTP(recorder, request)
		if recorder.Code != 404 {
			t.Fatal("Runner exposed message listener", path, recorder.Code)
		}
	}
}
