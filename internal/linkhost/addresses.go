package linkhost

import (
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
)

type Address struct {
	Endpoint      string `json:"endpoint"`
	Kind          string `json:"kind"`
	Interface     string `json:"interface,omitempty"`
	RequiresSetup bool   `json:"requires_setup,omitempty"`
}

func InterfaceAddresses(listen string, interfaces []net.Interface, addresses func(net.Interface) ([]net.Addr, error)) []Address {
	_, port, _ := net.SplitHostPort(listen)
	result := []Address{{Endpoint: "tcp://" + net.JoinHostPort("127.0.0.1", port), Kind: "loopback"}}
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
			endpoint := "tcp://" + net.JoinHostPort(ip.String(), port)
			if !seen[endpoint] {
				seen[endpoint] = true
				result = append(result, Address{Endpoint: endpoint, Kind: kind, Interface: device.Name})
			}
		}
	}
	return result
}

// Discovery reads interfaces and existing Serve state; it does not publish ports.
func DiscoverAddresses(ctx context.Context, listen string) []Address {
	devices, _ := net.Interfaces()
	result := InterfaceAddresses(listen, devices, func(device net.Interface) ([]net.Addr, error) { return device.Addrs() })
	if binary, err := exec.LookPath("tailscale"); err == nil {
		if address, err := DiscoverTailscaleAddress(ctx, binary, listen); err == nil {
			result = append(result, address)
		}
	}
	return result
}

func DiscoverTailscaleAddress(ctx context.Context, binary, listen string) (Address, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, binary, "status", "--json").Output()
	if err != nil {
		return Address{}, err
	}
	var status struct {
		BackendState string
		Self         *struct{ DNSName string }
	}
	if json.Unmarshal(raw, &status) != nil || status.BackendState != "Running" || status.Self == nil {
		return Address{}, agentlink.ErrUnavailable
	}
	dns := strings.TrimSuffix(status.Self.DNSName, ".")
	if dns == "" {
		return Address{}, agentlink.ErrUnavailable
	}
	raw, err = exec.CommandContext(ctx, binary, "serve", "status", "--json").Output()
	if err != nil {
		return Address{}, err
	}
	var config struct {
		TCP map[string]struct {
			TCPForward, TerminateTLS string
			HTTP, HTTPS              bool
		}
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return Address{}, err
	}
	ports := make([]string, 0, len(config.TCP))
	for port := range config.TCP {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	for _, port := range ports {
		m := config.TCP[port]
		if m.TCPForward == listen && m.TerminateTLS == "" && !m.HTTP && !m.HTTPS {
			return Address{Endpoint: "tcp://" + net.JoinHostPort(dns, port), Kind: "tailscale"}, nil
		}
	}
	_, port, _ := net.SplitHostPort(listen)
	// Show the discovered name even before the owner enables its Serve mapping.
	if _, occupied := config.TCP[port]; occupied {
		return Address{}, agentlink.ErrConflict
	}
	return Address{Endpoint: "tcp://" + net.JoinHostPort(dns, port), Kind: "tailscale", RequiresSetup: true}, nil
}

func AddressPriority(address Address) int {
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
