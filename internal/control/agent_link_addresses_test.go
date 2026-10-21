package control

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/linkhost"
)

func TestPotDiscoveredAddressInvitationsRetainListenersAcrossRestart(t *testing.T) {
	ctx := context.Background()
	a, b := agentControl(t, "discovery-a"), agentControl(t, "discovery-b")
	h := b.service.agentLinks
	endpoints := []linkhost.Address{{Endpoint: b.endpoint, Kind: "loopback"}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoints = append(endpoints, linkhost.Address{Endpoint: "tcp://" + listener.Addr().String(), Kind: "private"})
	listener.Close()
	h.discovered, h.discoveryAt = endpoints, time.Now()
	value, err := b.service.AgentLinks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(value.(map[string]any)["addresses"].([]linkhost.Address)) != 2 {
		t.Fatal(value)
	}
	var count int
	if err = b.service.integrationDB.QueryRow("SELECT COUNT(*) FROM agent_link_endpoints").Scan(&count); err != nil || count != 0 {
		t.Fatal("discovery enabled access", count, err)
	}
	var invitations []string
	for _, address := range endpoints {
		value, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite", Endpoint: address.Endpoint})
		if err != nil {
			t.Fatal(err)
		}
		invitation := value.(map[string]any)["address"].(string)
		parsed, _ := url.Parse(invitation)
		if "tcp://"+parsed.Host != address.Endpoint {
			t.Fatal("wrong invitation endpoint", invitation)
		}
		invitations = append(invitations, invitation)
	}
	if _, err = b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite", Endpoint: "tcp://8.8.8.8:8793"}); err == nil {
		t.Fatal("undiscovered address admitted")
	}
	options := b.service.options
	if err = b.service.Close(); err != nil {
		t.Fatal(err)
	}
	b.service, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range endpoints {
		if err = b.service.probeAgentEndpoint(ctx, address.Endpoint); err != nil {
			t.Fatal("listener not restored", address.Endpoint, err)
		}
	}
	// The first invitation remains usable after copying a second address and restarting.
	if _, err = a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "connect", Address: invitations[0]}); err != nil {
		t.Fatal(err)
	}
}

func TestPotSelectedListenerClosesOnPersistenceFailure(t *testing.T) {
	b := agentControl(t, "discovery-failure")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	b.service.agentLinks.setup.Lock()
	defer b.service.agentLinks.setup.Unlock()
	b.service.integrationDB.Close()
	if err = b.service.enableAgentAddress(context.Background(), "tcp://"+address); err == nil {
		t.Fatal("closed database accepted address")
	}
	listener, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatal("failed copy left listener enabled", err)
	}
	listener.Close()
}

func TestPotTailscaleInvitationPairsWithoutServePermission(t *testing.T) {
	a, b := agentControl(t, "tailnet-a"), agentControl(t, "tailnet-b")
	binary := filepath.Join(t.TempDir(), "tailscale")
	script := `#!/bin/sh
if [ "$1" = status ]; then echo '{"BackendState":"Running","Self":{"DNSName":"localhost."}}'; exit 0; fi
if [ "$2" = status ]; then echo '{}'; exit 0; fi
echo mutation > "$0.mutated"
exit 1
`
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(binary))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	listener.Close()
	address, err := linkhost.DiscoverTailscaleAddress(context.Background(), binary, listen)
	if err != nil {
		t.Fatal(err)
	}
	b.service.agentLinks.discovered = []linkhost.Address{address}
	b.service.agentLinks.discoveryAt = time.Now()
	value, err := b.service.ManageAgentLink(context.Background(), AgentLinkCommand{Action: "invite", Endpoint: address.Endpoint})
	if err != nil {
		t.Fatal("direct Tailscale invitation required Serve permission", err)
	}
	if _, err = a.service.ManageAgentLink(context.Background(), AgentLinkCommand{Action: "connect", Address: value.(map[string]any)["address"].(string)}); err != nil {
		t.Fatal("direct Tailscale invitation failed to pair", err)
	}
	if _, err = os.Stat(binary + ".mutated"); !os.IsNotExist(err) {
		t.Fatal("invitation attempted to modify Tailscale")
	}
}
