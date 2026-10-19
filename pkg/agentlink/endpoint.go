package agentlink

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

type endpointPolicy struct {
	lookup func(context.Context, string, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
}

func defaultEndpointPolicy() endpointPolicy {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return endpointPolicy{lookup: net.DefaultResolver.LookupNetIP, dial: dialer.DialContext}
}

// ValidateEndpoint accepts an HTTPS origin or an HTTP origin reachable only on
// a private network. HTTP DNS names must resolve entirely to private addresses.
// TLS certificates are not required for HTTP over a trusted VPN or tailnet.
// Actual HTTP connections are checked again and pinned by NewHTTPClient.
func ValidateEndpoint(ctx context.Context, raw string) (string, error) {
	return validateEndpointPolicy(ctx, raw, defaultEndpointPolicy())
}

func validateEndpointPolicy(ctx context.Context, raw string, policy endpointPolicy) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return "", ErrInvalid
	}
	if port := u.Port(); port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return "", ErrInvalid
		}
	}
	switch u.Scheme {
	case "https":
	case "http":
		if _, err = privateAddresses(ctx, u.Hostname(), policy); err != nil {
			return "", err
		}
	default:
		return "", ErrInvalid
	}
	u.Path = ""
	return u.String(), nil
}

var tailscaleRange = netip.MustParsePrefix("100.64.0.0/10")

func privateAddress(address netip.Addr) bool {
	address = address.Unmap()
	return address.IsValid() && (address.IsLoopback() || address.IsPrivate() || tailscaleRange.Contains(address))
}

func privateAddresses(ctx context.Context, host string, policy endpointPolicy) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		if !privateAddress(literal) {
			return nil, ErrInvalid
		}
		return []netip.Addr{literal.Unmap()}, nil
	}
	addresses, err := policy.lookup(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrUnavailable
	}
	for _, address := range addresses {
		if !privateAddress(address) {
			return nil, ErrInvalid
		}
	}
	return addresses, nil
}

// NewHTTPClient preserves the caller's HTTPS transport, including custom CA
// roots, and refuses redirects. Its HTTP transport bypasses HTTP proxies and
// checks private-network DNS again at each new connection, dialing the exact
// validated numeric IP rather than performing a second hostname resolution.
// A caller-provided transport cannot relax this HTTP boundary.
func NewHTTPClient(base *http.Client) *http.Client {
	return newHTTPClientPolicy(base, defaultEndpointPolicy())
}

func newHTTPClientPolicy(base *http.Client, policy endpointPolicy) *http.Client {
	client := http.Client{Timeout: 15 * time.Second}
	if base != nil {
		client = *base
	}
	if client.Timeout == 0 {
		client.Timeout = 15 * time.Second
	}
	secure := client.Transport
	if secure == nil {
		secure = http.DefaultTransport
	}
	private := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          32,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ExpectContinueTimeout: time.Second,
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(target)
			if err != nil {
				return nil, ErrInvalid
			}
			addresses, err := privateAddresses(ctx, host, policy)
			if err != nil {
				return nil, err
			}
			for _, address := range addresses {
				connection, dialErr := policy.dial(ctx, network, net.JoinHostPort(address.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
			return nil, ErrUnavailable
		},
	}
	client.Transport = &endpointTransport{secure: secure, private: private}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrInvalid }
	return &client
}

type endpointTransport struct {
	secure  http.RoundTripper
	private *http.Transport
}

func (t *endpointTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL == nil || request.URL.User != nil {
		return nil, ErrInvalid
	}
	switch request.URL.Scheme {
	case "https":
		return t.secure.RoundTrip(request)
	case "http":
		// Literal public addresses are rejected even before the transport can
		// reuse a pooled connection. DNS validation is pinned in DialContext.
		if address, err := netip.ParseAddr(request.URL.Hostname()); err == nil && !privateAddress(address) {
			return nil, ErrInvalid
		}
		return t.private.RoundTrip(request)
	default:
		return nil, ErrInvalid
	}
}

func (t *endpointTransport) CloseIdleConnections() {
	t.private.CloseIdleConnections()
	if closer, ok := t.secure.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}
