package linkhost

import (
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

// ConfigureTailscale publishes only the explicitly selected private TCP port.
// It never changes unrelated Serve mappings, enables Funnel or terminates TLS.
func ConfigureTailscale(ctx context.Context, listen string) (string, error) {
	binary, err := exec.LookPath("tailscale")
	if err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_missing"}
	}
	return ConfigureTailscaleBinary(ctx, binary, listen)
}
func ConfigureTailscaleBinary(ctx context.Context, binary, listen string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "0" {
		return "", protocol.APIError{Code: "agent_link_invalid_listen"}
	}
	raw, err := exec.CommandContext(ctx, binary, "status", "--json").Output()
	if err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_login_required"}
	}
	var status struct {
		BackendState string
		Self         *struct{ DNSName string }
	}
	if json.Unmarshal(raw, &status) != nil || status.BackendState != "Running" || status.Self == nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_login_required"}
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if dns == "" {
		return "", protocol.APIError{Code: "agent_link_tailscale_dns_required"}
	}
	type mapping struct {
		TCPForward   string
		TerminateTLS string
		HTTP         bool
		HTTPS        bool
	}
	read := func() (map[string]mapping, error) {
		raw, e := exec.CommandContext(ctx, binary, "serve", "status", "--json").Output()
		if e != nil {
			return nil, e
		}
		var config struct{ TCP map[string]mapping }
		e = json.Unmarshal(raw, &config)
		return config.TCP, e
	}
	mappings, err := read()
	if err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_unavailable"}
	}
	find := func(config map[string]mapping) string {
		for external, m := range config {
			if m.TCPForward == listen && m.TerminateTLS == "" && !m.HTTP && !m.HTTPS {
				return "tcp://" + net.JoinHostPort(dns, external)
			}
		}
		return ""
	}
	if endpoint := find(mappings); endpoint != "" {
		return endpoint, nil
	}
	if _, ok := mappings[port]; ok {
		return "", protocol.APIError{Code: "agent_link_tailscale_conflict"}
	}
	if _, err = exec.CommandContext(ctx, binary, "serve", "--bg", "--tcp="+port, "tcp://"+listen).CombinedOutput(); err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_setup_required"}
	}
	mappings, err = read()
	if err != nil {
		return "", protocol.APIError{Code: "agent_link_tailscale_unavailable"}
	}
	if endpoint := find(mappings); endpoint != "" {
		return endpoint, nil
	}
	return "", protocol.APIError{Code: "agent_link_tailscale_setup_required"}
}
