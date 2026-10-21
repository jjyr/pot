package control

import (
	"context"
	"net"
	"net/netip"
	"os"
	"runtime"
	"sort"
	"strings"
)

func (s *Service) ControlOrigin() string {
	if s.agentLinks == nil {
		return ""
	}
	s.agentLinks.mu.Lock()
	defer s.agentLinks.mu.Unlock()
	return s.agentLinks.config.Endpoint
}

func (s *Service) localMachine(_ context.Context) (map[string]any, error) {
	policy, _, err := s.Permissions.Policy(s.options.RuntimeID)
	if err != nil {
		return nil, err
	}
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
	name := hostname
	if name == "" && len(ips) > 0 {
		name = ips[0]
	}
	if name == "" {
		name = runtime.GOOS
	}
	return map[string]any{"id": s.options.RuntimeID, "runtime_id": s.options.RuntimeID, "name": name, "hostname": hostname, "ip_addresses": ips, "os": runtime.GOOS, "connected": true, "role": "node", "tags": s.Machines.LocalLabels(), "policy": policy}, nil
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
func (s *Service) MachineList(ctx context.Context) ([]map[string]any, error) {
	local, err := s.localMachine(ctx)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{local}
	return items, nil
}
