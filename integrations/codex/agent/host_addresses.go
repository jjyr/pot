package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/peer"
)

type hostAddress struct {
	Endpoint      string `json:"endpoint"`
	Kind          string `json:"kind"`
	Interface     string `json:"interface,omitempty"`
	RequiresSetup bool   `json:"requires_setup,omitempty"`
}

func interfaceAddresses(interfaces []net.Interface, addresses func(net.Interface) ([]net.Addr, error)) []hostAddress {
	result := []hostAddress{{Endpoint: "tcp://" + hostPeerListen, Kind: "loopback"}}
	seen := map[string]bool{result[0].Endpoint: true}
	tailnet := netip.MustParsePrefix("100.64.0.0/10")
	tailnetIPv6 := netip.MustParsePrefix("fd7a:115c:a1e0::/48")
	for _, device := range interfaces {
		if device.Flags&net.FlagUp == 0 || device.Flags&net.FlagLoopback != 0 {
			continue
		}
		list, err := addresses(device)
		if err != nil {
			continue
		}
		for _, item := range list {
			prefix, err := netip.ParsePrefix(item.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr().Unmap()
			kind := "private"
			if tailnet.Contains(ip) || tailnetIPv6.Contains(ip) {
				kind = "tailscale"
			} else if !ip.IsPrivate() {
				if !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() {
					continue
				}
				kind = "public"
			}
			endpoint := "tcp://" + net.JoinHostPort(ip.String(), "8793")
			if !seen[endpoint] {
				seen[endpoint] = true
				result = append(result, hostAddress{Endpoint: endpoint, Kind: kind, Interface: device.Name})
			}
		}
	}
	return result
}

// Discovery reads interfaces and existing Serve state; it does not publish ports.
func discoverHostAddresses(ctx context.Context) []hostAddress {
	devices, _ := net.Interfaces()
	result := interfaceAddresses(devices, func(device net.Interface) ([]net.Addr, error) { return device.Addrs() })
	if binary, err := exec.LookPath("tailscale"); err == nil {
		if address, err := discoverTailscaleAddress(ctx, binary); err == nil {
			result = append(result, address)
		}
	}
	return result
}

func discoverTailscaleAddress(ctx context.Context, binary string) (hostAddress, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, binary, "status", "--json").Output()
	if err != nil {
		return hostAddress{}, err
	}
	var status struct {
		BackendState string
		Self         *struct{ DNSName string }
	}
	if json.Unmarshal(raw, &status) != nil || status.BackendState != "Running" || status.Self == nil {
		return hostAddress{}, agentlink.ErrUnavailable
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if dns == "" {
		return hostAddress{}, agentlink.ErrUnavailable
	}
	raw, err = exec.CommandContext(ctx, binary, "serve", "status", "--json").Output()
	if err != nil {
		return hostAddress{}, err
	}
	var config struct {
		TCP map[string]struct {
			TCPForward, TerminateTLS string
			HTTP, HTTPS              bool
		}
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return hostAddress{}, err
	}
	ports := make([]string, 0, len(config.TCP))
	for port := range config.TCP {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	for _, port := range ports {
		m := config.TCP[port]
		if m.TCPForward == hostPeerListen && m.TerminateTLS == "" && !m.HTTP && !m.HTTPS {
			return hostAddress{Endpoint: "tcp://" + net.JoinHostPort(dns, port), Kind: "tailscale"}, nil
		}
	}
	// Show the discovered name even before the owner enables its Serve mapping.
	if _, occupied := config.TCP["8793"]; occupied {
		return hostAddress{}, agentlink.ErrConflict
	}
	return hostAddress{Endpoint: "tcp://" + net.JoinHostPort(dns, "8793"), Kind: "tailscale", RequiresSetup: true}, nil
}

func (h *agentHost) addresses(ctx context.Context) []hostAddress {
	h.discoveryMu.Lock()
	defer h.discoveryMu.Unlock()
	if h.discover != nil {
		return h.discover(ctx)
	}
	if time.Since(h.discoveryAt) > 10*time.Second {
		h.discovered = discoverHostAddresses(ctx)
		h.discoveryAt = time.Now()
	}
	result := append([]hostAddress{}, h.discovered...)
	seen := map[string]bool{}
	for _, a := range result {
		seen[a.Endpoint] = true
	}
	rows, err := h.db.QueryContext(ctx, "SELECT endpoint FROM codex_agent_endpoints UNION SELECT endpoint FROM codex_agent_host WHERE endpoint<>''")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var endpoint string
			if rows.Scan(&endpoint) == nil && !seen[endpoint] {
				seen[endpoint] = true
				result = append(result, hostAddress{Endpoint: endpoint, Kind: "configured"})
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if left, right := addressPriority(result[i]), addressPriority(result[j]); left != right {
			return left < right
		}
		return result[i].Endpoint < result[j].Endpoint
	})
	return result
}

func addressPriority(address hostAddress) int {
	endpoint, err := url.Parse(address.Endpoint)
	if err != nil || endpoint.Hostname() == "" {
		return 5
	}
	ip, err := netip.ParseAddr(endpoint.Hostname())
	if err != nil {
		if address.Kind == "tailscale" {
			return 0
		}
		return 5
	}
	ip = ip.Unmap()
	if address.Kind == "tailscale" || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip) {
		return 1
	}
	if ip.IsLoopback() {
		return 4
	}
	if ip.IsPrivate() {
		return 3
	}
	if ip.IsGlobalUnicast() && !ip.IsLinkLocalUnicast() {
		return 2
	}
	return 5
}

func (h *agentHost) selectAddress(ctx context.Context, endpoint string) (string, error) {
	var selected *hostAddress
	for _, address := range h.addresses(ctx) {
		if address.Endpoint == endpoint {
			selected = &address
			break
		}
	}
	if selected == nil {
		return "", agentlink.ErrInvalid
	}
	if selected.RequiresSetup {
		actual, err := configureHostTailscale(ctx)
		if err != nil {
			return "", err
		}
		h.discoveryMu.Lock()
		h.discoveryAt = time.Time{}
		h.discoveryMu.Unlock()
		if actual != endpoint {
			return "", agentlink.ErrConflict
		}
	}
	if err := h.enableAddress(ctx, endpoint); err != nil {
		return "", err
	}
	return endpoint, nil
}

func (h *agentHost) enableAddress(ctx context.Context, endpoint string) error {
	// Persist only after the selected listener authenticates as this exact host.
	if _, err := peer.ValidateEndpoint(ctx, endpoint); err != nil {
		return agentlink.ErrInvalid
	}
	h.accessMu.Lock()
	defer h.accessMu.Unlock()
	candidate, err := h.preparePeer(ctx, endpoint)
	if err != nil {
		return err
	}
	if _, err = h.db.ExecContext(ctx, "INSERT OR IGNORE INTO codex_agent_endpoints(endpoint) VALUES(?)", endpoint); err != nil {
		if candidate != nil {
			candidate.Close()
		}
		return err
	}
	if candidate != nil {
		if h.additionalPeers == nil {
			h.additionalPeers = make(map[string]*linkhost.PeerListener)
		}
		h.additionalPeers[endpoint] = candidate
	}
	return nil
}

func (h *agentHost) restoreAddresses(ctx context.Context) error {
	rows, err := h.db.QueryContext(ctx, "SELECT endpoint FROM codex_agent_endpoints ORDER BY endpoint")
	if err != nil {
		return err
	}
	var endpoints []string
	for rows.Next() {
		var endpoint string
		if err = rows.Scan(&endpoint); err != nil {
			break
		}
		endpoints = append(endpoints, endpoint)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	for _, endpoint := range endpoints {
		probe, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		// An unavailable interface must not stop the local management page or other addresses.
		_ = h.enableAddress(probe, endpoint)
		cancel()
	}
	return nil
}
