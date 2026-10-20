package peer

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

func usableAddress(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	return a.IsLoopback() || a.IsGlobalUnicast() && !a.IsLinkLocalUnicast()
}

// ValidateEndpoint accepts direct unicast TCP endpoints, including public IPs.
// Dial resolves again and dials a validated numeric address. The signed handshake
// checks the invitation or saved identity pin independently of DNS and IP range.
func ValidateEndpoint(ctx context.Context, raw string) (string, error) {
	u, _, err := resolveEndpoint(ctx, raw)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
func resolveEndpoint(ctx context.Context, raw string) (*url.URL, []net.IPAddr, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "tcp" || u.Host == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, nil, fmt.Errorf("invalid TCP endpoint")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Hostname() == "" || strings.Contains(u.Hostname(), "%") {
		return nil, nil, fmt.Errorf("invalid TCP port or host")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, nil, fmt.Errorf("TCP endpoint resolution failed")
	}
	for _, addr := range addresses {
		if !usableAddress(addr.IP) {
			return nil, nil, fmt.Errorf("TCP endpoint requires a unicast address")
		}
	}
	u.Host = net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.Itoa(port))
	return u, addresses, nil
}
