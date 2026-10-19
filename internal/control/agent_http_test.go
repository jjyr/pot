package control

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestDirectHTTPAgentListenerPersistsAndKeepsOwnerAPIsPrivate(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "plain-a"), agentControl(t, "plain-b")
	available, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + available.Addr().String()
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
	result, err := a.service.sendAssistantMail(ctx, "http-mail", "main", protocol.SendMail{To: protocol.AgentRef{ControlID: "plain-b", Kind: "assistant", AgentID: "main"}, Text: "private network message"})
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
	client := agentlink.NewHTTPClient(nil)
	for _, path := range []string{"/api/v1/agent-links", "/api/v1/runtime/control", "/api/v1/machines"} {
		request, _ := http.NewRequestWithContext(ctx, "GET", endpoint+path, nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatal("peer listener exposed owner API", path, response.StatusCode)
		}
	}
	response, err := client.Get(endpoint + agentlink.Prefix + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
}
