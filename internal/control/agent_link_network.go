package control

import (
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

// configureAgentTailscale is an explicit owner action. Reads never alter Serve,
// and an unrelated existing mapping is never reset or overwritten.
func configureAgentTailscale(ctx context.Context, listen string) (string, error) {
	command, err := exec.LookPath("tailscale")
	if err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_missing"}
	}
	return configureAgentTailscaleBinary(ctx, command, listen)
}
func configureAgentTailscaleBinary(ctx context.Context, command, listen string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	statusRaw, err := exec.CommandContext(ctx, command, "status", "--json").Output()
	if err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_login_required"}
	}
	var status struct {
		BackendState string
		Self         *struct{ DNSName string }
	}
	if json.Unmarshal(statusRaw, &status) != nil || status.BackendState != "Running" || status.Self == nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_login_required"}
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if dns == "" {
		return "", protocol.APIError{Code: "agent_link_tailscale_dns_required"}
	}
	existing := false
	read := func() (map[string]struct {
		Handlers map[string]struct{ Proxy string }
	}, error) {
		raw, e := exec.CommandContext(ctx, command, "serve", "status", "--json").Output()
		if e != nil {
			return nil, e
		}
		var config struct {
			Web map[string]struct {
				Handlers map[string]struct{ Proxy string }
			}
		}
		e = json.Unmarshal(raw, &config)
		var all map[string]json.RawMessage
		if e == nil {
			e = json.Unmarshal(raw, &all)
		}
		for _, v := range all {
			value := strings.TrimSpace(string(v))
			if value != "null" && value != "{}" && value != "[]" && value != "false" && value != `""` {
				existing = true
			}
		}
		return config.Web, e
	}
	mappings, err := read()
	if err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_unavailable"}
	}
	target := "http://" + listen
	for origin, mapping := range mappings {
		if mapping.Handlers["/"].Proxy == target {
			host, _, e := net.SplitHostPort(origin)
			if e == nil && host == dns {
				return "https://" + origin, nil
			}
		}
	}
	port := "443"
	if existing {
		return "", protocol.APIError{Code: "agent_link_tailscale_conflict"}
	}
	if _, err = exec.CommandContext(ctx, command, "serve", "--bg", "--https="+port, target).CombinedOutput(); err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_setup_required"}
	}
	mappings, err = read()
	if err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_unavailable"}
	}
	for origin, mapping := range mappings {
		if mapping.Handlers["/"].Proxy == target {
			host, _, e := net.SplitHostPort(origin)
			if e == nil && host == dns {
				return "https://" + origin, nil
			}
		}
	}
	return "", protocol.APIError{Code: "agent_link_tailscale_setup_required"}
}
