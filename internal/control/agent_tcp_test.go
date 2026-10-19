package control

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/peer"
	"net"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestDirectTCPAgentListenerPersistsAndKeepsOwnerAPIsPrivate(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "plain-a"), agentControl(t, "plain-b")
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "tcp://" + available.Addr().String()
	available.Close()
	value, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "configure", AssistantID: "main", Mode: "address", Endpoint: endpoint})
	if err != nil {
		t.Fatal(err)
	}
	self := value.(map[string]any)["self"].(map[string]any)
	if self["endpoint"] != endpoint || self["status"] != "ready" {
		t.Fatal(self)
	}
	invitation, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite", AssistantID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	link, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "connect", AssistantID: "main", Address: invitation.(map[string]any)["address"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	if link.(agentlink.Connection).State != "ready" {
		t.Fatal(link)
	}
	result, err := a.service.sendAssistantMail(ctx, "tcp-mail", "main", protocol.SendMail{To: protocol.AgentRef{NodeID: "plain-b", Kind: "assistant", AgentID: "main"}, Text: "private network message"})
	if err != nil {
		t.Fatal(err)
	}
	awaitAgentLinkMail(t, b.service, result.ID)
	options := b.service.options
	b.service.Close()
	b.service, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	identity, key := a.service.Machines.TransportIdentity()
	expected, _ := b.service.Machines.TransportIdentity()
	for _, service := range []string{"owner", "/api/v1/machines"} {
		session, e := peer.Dial(ctx, endpoint, peer.DialOptions{Identity: identity, PrivateKey: key, Expected: &expected, Service: service, Auth: json.RawMessage(`{}`)})
		if e == nil {
			session.Close()
			t.Fatal("peer listener exposed owner API", service)
		}
	}
	if err = b.service.probeAgentEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}

}

func TestMissingVPNInterfaceKeepsControlAndAccessSetupAvailable(t *testing.T) {
	ctx := context.Background()
	f := agentControl(t, "vpn-loss")
	if _, err := f.service.integrationDB.Exec(`UPDATE agent_link_host SET listen='100.127.255.254:63001',endpoint='tcp://100.127.255.254:63001',mode='address'`); err != nil {
		t.Fatal(err)
	}
	options := f.service.options
	f.service.Close()
	var err error
	f.service, err = Open(options)
	if err != nil {
		t.Fatal("missing VPN interface prevented Control startup", err)
	}
	value, err := f.service.AgentLinks(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	self := value.(map[string]any)["self"].(map[string]any)
	if self["status"] != "needs_access" || self["reason_code"] != "agent_link_listener_unavailable" {
		t.Fatal(self)
	}
	if _, err = f.service.Registry.Definitions(ctx); err != nil {
		t.Fatal("owner access unavailable", err)
	}
}
