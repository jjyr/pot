package linkhost

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"sync"

	"github.com/jjyr/pot/pkg/peer"
)

// TCPListenAddress locates this host's interface for an explicitly configured
// peer endpoint. The owner HTTP server is never attached to this address.
func TCPListenAddress(ctx context.Context, endpoint string) (string, error) {
	endpoint, err := peer.ValidateEndpoint(ctx, endpoint)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(endpoint)
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return "", err
	}
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, address := range addresses {
		if _, err := peer.ValidateEndpoint(ctx, "tcp://"+net.JoinHostPort(address.IP.String(), u.Port())); err != nil {
			return "", err
		}
	}
	for _, address := range addresses {
		if address.IP.IsLoopback() {
			return net.JoinHostPort(address.IP.String(), u.Port()), nil
		}
		for _, item := range interfaces {
			ip, _, e := net.ParseCIDR(item.String())
			if e == nil && ip.Equal(address.IP) {
				return net.JoinHostPort(ip.String(), u.Port()), nil
			}
		}
	}
	return "", nil
}

// Registry owns service dispatch for one host identity. Registering a service
// does not grant another service's authority; each authorizer validates its own
// invitation, message route or execution membership before any handler runs.
type Registry struct {
	mu       sync.RWMutex
	identity peer.Identity
	key      ed25519.PrivateKey
	services map[string]registration
}
type registration struct {
	authorize func(context.Context, peer.Identity, string, json.RawMessage) (peer.Handler, error)
	attach    func(*peer.Session)
}

func NewRegistry(identity peer.Identity, key ed25519.PrivateKey) *Registry {
	r := &Registry{identity: identity, key: append(ed25519.PrivateKey(nil), key...), services: map[string]registration{}}
	_ = r.Register("identity", func(context.Context, peer.Identity, string, json.RawMessage) (peer.Handler, error) {
		return func(_ context.Context, request peer.Request) (json.RawMessage, error) {
			if request.Method != "identity" {
				return nil, peer.ErrUnauthorized
			}
			return json.Marshal(identity)
		}, nil
	}, nil)
	return r
}
func (r *Registry) Register(name string, authorize func(context.Context, peer.Identity, string, json.RawMessage) (peer.Handler, error), attach func(*peer.Session)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" || authorize == nil {
		return errors.New("peer service registration required")
	}
	if _, ok := r.services[name]; ok {
		return errors.New("duplicate peer service")
	}
	r.services[name] = registration{authorize, attach}
	return nil
}
func (r *Registry) Server() *peer.Server {
	return &peer.Server{Identity: r.identity, PrivateKey: r.key, Authorize: func(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
		r.mu.RLock()
		entry, ok := r.services[service]
		r.mu.RUnlock()
		if !ok {
			return nil, peer.ErrUnauthorized
		}
		return entry.authorize(ctx, remote, service, auth)
	}, OnSession: func(session *peer.Session) {
		r.mu.RLock()
		entry, ok := r.services[session.Service()]
		r.mu.RUnlock()
		if !ok {
			session.Close()
			return
		}
		if entry.attach != nil {
			entry.attach(session)
		}
	}}
}

// Probe authenticates the exact expected host over the same TCP handshake as
// other services. No unauthenticated HTTP readiness endpoint is involved.
func Probe(ctx context.Context, endpoint string, identity peer.Identity, key ed25519.PrivateKey, expected peer.Identity) error {
	session, err := peer.Dial(ctx, endpoint, peer.DialOptions{Identity: identity, PrivateKey: key, Expected: &expected, Service: "identity", Context: ctx})
	if err != nil {
		return err
	}
	session.Close()
	return nil
}

type PeerListener struct {
	listener net.Listener
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.Mutex
	sessions map[*peer.Session]struct{}
	closed   bool
	once     sync.Once
	closeErr error
}

func ListenPeer(ctx context.Context, address string, server *peer.Server) (*PeerListener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &PeerListener{listener: listener, cancel: cancel, done: make(chan struct{}), sessions: map[*peer.Session]struct{}{}}
	config := *server
	original := config.OnSession
	config.OnSession = func(session *peer.Session) {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			session.Close()
			return
		}
		p.sessions[session] = struct{}{}
		p.mu.Unlock()
		if original != nil {
			original(session)
		}
		go func() { <-session.Done(); p.mu.Lock(); delete(p.sessions, session); p.mu.Unlock() }()
	}
	go func() { defer close(p.done); _ = config.Serve(ctx, listener) }()
	return p, nil
}
func (p *PeerListener) Addr() string { return p.listener.Addr().String() }
func (p *PeerListener) Close() error {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		sessions := make([]*peer.Session, 0, len(p.sessions))
		for session := range p.sessions {
			sessions = append(sessions, session)
		}
		p.mu.Unlock()
		p.cancel()
		p.closeErr = p.listener.Close()
		for _, session := range sessions {
			session.Close()
		}
		<-p.done
		if errors.Is(p.closeErr, net.ErrClosed) {
			p.closeErr = nil
		}
	})
	return p.closeErr
}
