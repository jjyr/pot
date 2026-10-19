package linkhost

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
)

// HTTPListenAddress locates a local interface for a direct HTTP peer endpoint.
// An empty address means an existing proxy owns the endpoint, or it uses HTTPS.
// The caller validates the endpoint with agentlink.ValidateEndpoint first.
func HTTPListenAddress(ctx context.Context, endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" {
		return "", nil
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return "", err
	}
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, address := range addresses {
		if _, err := agentlink.ValidateEndpoint(ctx, "http://"+net.JoinHostPort(address.IP.String(), port)); err != nil {
			continue
		}
		if address.IP.IsLoopback() {
			return net.JoinHostPort(address.IP.String(), port), nil
		}
		for _, item := range interfaces {
			ip, _, err := net.ParseCIDR(item.String())
			if err == nil && ip.Equal(address.IP) {
				return net.JoinHostPort(address.IP.String(), port), nil
			}
		}
	}
	return "", nil
}

// PeerListener serves only the supplied narrow Agent handler. Local owner UI
// and execution handlers never join this listener.
type PeerListener struct {
	server  *http.Server
	address string
	done    chan struct{}
}

func ListenPeer(address string, handler http.Handler) (*PeerListener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	p := &PeerListener{server: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}, address: listener.Addr().String(), done: make(chan struct{})}
	go func() { defer close(p.done); _ = p.server.Serve(listener) }()
	return p, nil
}

func (p *PeerListener) Addr() string { return p.address }
func (p *PeerListener) Close() error { err := p.server.Close(); <-p.done; return err }
