package agentlink

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func endpointTestPolicy(addresses ...string) endpointPolicy {
	values := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		values = append(values, netip.MustParseAddr(address))
	}
	return endpointPolicy{lookup: func(context.Context, string, string) ([]netip.Addr, error) { return values, nil }, dial: (&net.Dialer{}).DialContext}
}

func TestEndpointAcceptsPrivateHTTPAndKeepsPublicHTTPS(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{
		"http://127.0.0.1:8792", "http://[::1]:8792", "http://10.20.30.40", "http://172.16.0.1", "http://172.31.255.254", "http://192.168.22.1:8000/", "http://100.64.0.1", "http://100.127.255.254", "http://[fd7a:115c:a1e0::1]:8792", "http://[fc00::12]", "http://[::ffff:192.168.1.2]", "https://public.example:443",
	} {
		t.Run(raw, func(t *testing.T) {
			value, err := ValidateEndpoint(ctx, raw)
			if err != nil || value != strings.TrimSuffix(raw, "/") {
				t.Fatal(value, err)
			}
		})
	}
	for _, raw := range []string{
		"http://8.8.8.8", "http://203.0.113.5", "http://172.15.255.255", "http://172.32.0.1", "http://100.63.255.255", "http://100.128.0.1", "http://169.254.169.254", "http://0.0.0.0", "http://[2001:4860:4860::8888]", "http://[fe80::1]", "http://[::]", "http://[::ffff:203.0.113.5]", "http://user:pass@192.168.1.2", "http://192.168.1.2?token=secret", "http://192.168.1.2?", "http://192.168.1.2/other", "http://192.168.1.2/%2f", "http://192.168.1.2#secret", "http://192.168.1.2:65536", "http://192.168.1.2:0", "ftp://192.168.1.2",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ValidateEndpoint(ctx, raw); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid or public origin accepted", raw, err)
			}
		})
	}
}

func TestEndpointDNSMustResolveEntirelyToPrivateNetwork(t *testing.T) {
	ctx := context.Background()
	for _, host := range []string{"runner.vpn.example", "agent.tailnet.ts.net", "localhost"} {
		value, err := validateEndpointPolicy(ctx, "http://"+host+":8792", endpointTestPolicy("100.100.1.2", "fd7a:115c:a1e0::2"))
		if err != nil || value != "http://"+host+":8792" {
			t.Fatal(value, err)
		}
	}
	for _, addresses := range [][]string{{"203.0.113.1"}, {"10.1.2.3", "203.0.113.1"}, {"fd00::1", "2001:4860:4860::8888"}} {
		if _, err := validateEndpointPolicy(ctx, "http://looks-private.vpn.example", endpointTestPolicy(addresses...)); !errors.Is(err, ErrInvalid) {
			t.Fatal(addresses, err)
		}
	}
	if _, err := validateEndpointPolicy(ctx, "http://missing.vpn.example", endpointTestPolicy()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	policy := endpointTestPolicy()
	policy.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		t.Fatal("HTTPS URL validation must preserve normal TLS resolution")
		return nil, nil
	}
	if _, err := validateEndpointPolicy(ctx, "https://public.example", policy); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateHTTPPinsDNSAndCannotUseCallerProxyOrDialer(t *testing.T) {
	var calls, proxyCalls, unsafeDialCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Host != "agent.tailnet.ts.net:8792" || r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("HTTP authority or authenticated request changed")
		}
		io.WriteString(w, "private-only")
	}))
	defer server.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		http.Error(w, "must not receive credentials", 500)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	base := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { proxyCalls.Add(1); return proxyURL, nil }, DialContext: func(context.Context, string, string) (net.Conn, error) {
		unsafeDialCalls.Add(1)
		return nil, errors.New("must not use caller HTTP dialer")
	}}}
	policy := endpointTestPolicy("100.100.12.34")
	policy.dial = func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "100.100.12.34:8792" {
			t.Error("destination was not pinned to verified numeric IP", target)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	client := newHTTPClientPolicy(base, policy)
	defer client.CloseIdleConnections()
	request, _ := http.NewRequest("GET", "http://agent.tailnet.ts.net:8792/identity", nil)
	request.Header.Set("Authorization", "Bearer private-token")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "private-only" || calls.Load() != 1 || proxyCalls.Load() != 0 || unsafeDialCalls.Load() != 0 {
		t.Fatal(string(body), calls.Load(), proxyCalls.Load(), unsafeDialCalls.Load(), err)
	}
}

func TestPrivateHTTPRechecksDNSBeforeDialAndRejectsRebinding(t *testing.T) {
	ctx := context.Background()
	var lookups, dials atomic.Int32
	policy := endpointTestPolicy()
	policy.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.1")}, nil
	}
	policy.dial = func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("public target must not be dialed")
	}
	endpoint, err := validateEndpointPolicy(ctx, "http://changing.vpn.example", policy)
	if err != nil {
		t.Fatal(err)
	}
	client := newHTTPClientPolicy(nil, policy)
	defer client.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(ctx, "POST", endpoint+Prefix+"/pair", strings.NewReader(`{"private":"credentials"}`))
	request.Header.Set("Authorization", "Bearer private-token")
	if _, err = client.Do(request); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if lookups.Load() != 2 || dials.Load() != 0 {
		t.Fatal("DNS validation did not fence network connection", lookups.Load(), dials.Load())
	}
}

func TestPrivateHTTPRejectsPublicLiteralEvenWithCustomTransport(t *testing.T) {
	var called atomic.Bool
	base := &http.Client{Transport: endpointRoundTripFunc(func(*http.Request) (*http.Response, error) {
		called.Store(true)
		return nil, errors.New("unsafe custom transport")
	})}
	client := NewHTTPClient(base)
	defer client.CloseIdleConnections()
	if _, err := client.Get("http://203.0.113.1/private"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if called.Load() {
		t.Fatal("caller transport bypassed HTTP private-network boundary")
	}
}

func TestEndpointClientRetainsTLSVerificationAndCallerCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "TLS verified") }))
	defer server.Close()
	trusted := NewHTTPClient(server.Client())
	defer trusted.CloseIdleConnections()
	response, err := trusted.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "TLS verified" {
		t.Fatal(string(body), err)
	}
	untrusted := NewHTTPClient(nil)
	defer untrusted.CloseIdleConnections()
	if response, err = untrusted.Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("TLS verification was disabled")
	}
	var called atomic.Bool
	custom := NewHTTPClient(&http.Client{Transport: endpointRoundTripFunc(func(*http.Request) (*http.Response, error) {
		called.Store(true)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("trusted embedding"))}, nil
	})})
	response, err = custom.Get("https://public.example")
	if err != nil || !called.Load() {
		t.Fatal("HTTPS custom transport was lost", err)
	}
	response.Body.Close()
}

func TestEndpointClientNeverFollowsHTTPOrHTTPSRedirect(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); io.WriteString(w, "not reached") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := NewHTTPClient(nil)
	defer client.CloseIdleConnections()
	response, err := client.Get(redirect.URL)
	if response != nil {
		response.Body.Close()
	}
	if !errors.Is(err, ErrInvalid) || targetCalls.Load() != 0 {
		t.Fatal(targetCalls.Load(), err)
	}
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://203.0.113.1/", http.StatusFound)
	}))
	defer secure.Close()
	secureClient := NewHTTPClient(secure.Client())
	defer secureClient.CloseIdleConnections()
	response, err = secureClient.Get(secure.URL)
	if response != nil {
		response.Body.Close()
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestInvitationURLRetainsNarrowPathAndRejectsQueries(t *testing.T) {
	ctx := context.Background()
	host := newFixture(t, "private-http-origin")
	invitation, err := host.service.Invite(ctx, "assistant", host.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*url.URL){func(u *url.URL) { u.ForceQuery = true }, func(u *url.URL) { u.RawQuery = "token=other" }, func(u *url.URL) { u.User = url.User("other") }, func(u *url.URL) { u.Path = "/owner" }, func(u *url.URL) { u.RawPath = "/api/v1/agent-link/%63onnect" }} {
		value, err := url.Parse(invitation.Address)
		if err != nil {
			t.Fatal(err)
		}
		change(value)
		if _, err = parseAddress(ctx, value.String()); !errors.Is(err, ErrInvalid) {
			t.Fatal("expanded invitation URL accepted", err)
		}
	}
}

type endpointRoundTripFunc func(*http.Request) (*http.Response, error)

func (f endpointRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
