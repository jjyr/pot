package control

import (
	"net"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strings"
)

// LocalHost describes this node's host for presentation, never remote execution
// destinations or permission policy.
func (s *Service) LocalHost() map[string]any {
	hostname, _ := os.Hostname()
	hostname = strings.TrimSpace(hostname)
	addresses := []net.Addr{}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		values, _ := iface.Addrs()
		addresses = append(addresses, values...)
	}
	ips := machineIPAddresses(addresses)
	return map[string]any{"hostname": hostname, "ip_addresses": ips, "os": runtime.GOOS, "tags": s.Machines.LocalLabels()}
}

// These are observed local interface addresses, not advertised peer endpoints
// or proof that a machine can be reached from another network.
func machineIPAddresses(values []net.Addr) []string {
	seen := map[netip.Addr]bool{}
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value.String())
		if err != nil {
			continue
		}
		ip := prefix.Addr().Unmap()
		if ip.IsGlobalUnicast() && !ip.IsLoopback() {
			seen[ip] = true
		}
	}
	ips := make([]netip.Addr, 0, len(seen))
	for ip := range seen {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool { return ips[i].Compare(ips[j]) < 0 })
	result := make([]string, 0, len(ips))
	for _, ip := range ips {
		result = append(result, ip.String())
	}
	return result
}
