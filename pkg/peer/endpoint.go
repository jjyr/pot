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

var tailnetRange = netip.MustParsePrefix("100.64.0.0/10")

func privateAddress(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || tailnetRange.Contains(a)
}

// ValidateEndpoint only accepts private, direct TCP endpoints. DNS must resolve
// entirely to private addresses. Dial resolves again and dials a validated IP;
// neither proxy environments nor DNS rebinding can redirect credentials publicly.
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
		return nil, nil, fmt.Errorf("invalid private TCP endpoint")
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
		if !privateAddress(addr.IP) {
			return nil, nil, fmt.Errorf("TCP endpoint requires a private network")
		}
	}
	u.Host = net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.Itoa(port))
	return u, addresses, nil
}
