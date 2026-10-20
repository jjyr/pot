package peer

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

const protocolVersion = 4

type hello struct {
	Version  int             `cbor:"version"`
	Identity Identity        `cbor:"identity"`
	Expected *Identity       `cbor:"expected"`
	Service  string          `cbor:"service"`
	Auth     json.RawMessage `cbor:"auth"`
}

type acceptance struct {
	Version  int      `cbor:"version"`
	Identity Identity `cbor:"identity"`
	Error    string   `cbor:"error,omitempty"`
}

func handshakeContext(ctx context.Context, c net.Conn) func() {
	deadline := time.Now().Add(10 * time.Second)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		deadline = t
	}
	_ = c.SetDeadline(deadline)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()); close(done) })
	return func() {
		if !stop() {
			<-done
		}
		_ = c.SetDeadline(time.Time{})
	}
}
func writeHandshake(c net.Conn, value any) error {
	raw, err := wireEncoding.Marshal(value)
	if err != nil {
		return err
	}
	return writePacket(c, raw, maxHandshake)
}
func validHello(h hello) bool {
	if h.Version != protocolVersion || h.Identity.Validate() != nil || len(h.Service) < 1 || len(h.Service) > 128 || len(h.Auth) > 32*1024 || !json.Valid(h.Auth) {
		return false
	}
	if h.Expected != nil {
		return h.Expected.Validate() == nil
	}
	// An unpinned endpoint may only discover an untrusted identity. Never send
	// capabilities or expose a reverse business handler before establishing trust.
	return h.Service == "identity" && string(h.Auth) == "null"
}

// Dial authenticates a peer over TLS 1.3 on direct TCP. A nil Expected permits
// only capability-free identity discovery, whose result does not establish trust.
// It never retries a request or falls back to another protocol.
func Dial(ctx context.Context, endpoint string, options DialOptions) (*Session, error) {
	if validLocal(options.Identity, options.PrivateKey) != nil {
		return nil, ErrUnauthorized
	}
	if options.Expected != nil {
		pin := *options.Expected
		options.Expected = &pin
	}
	if options.Auth == nil {
		options.Auth = json.RawMessage("null")
	}
	if !validHello(hello{protocolVersion, options.Identity, options.Expected, options.Service, options.Auth}) || options.Expected == nil && options.Handler != nil {
		return nil, ErrUnauthorized
	}
	u, addresses, err := resolveEndpoint(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var c net.Conn
	for _, address := range addresses {
		c, err = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(address.IP.String(), u.Port()))
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	s, err := dialConn(ctx, c, options)
	if err != nil {
		_ = c.Close()
	}
	return s, err
}
func dialConn(ctx context.Context, c net.Conn, o DialOptions) (*Session, error) {
	finish := handshakeContext(ctx, c)
	defer func() { finish() }()
	config, err := tlsConfig(o.Identity, o.PrivateKey, o.Expected, false)
	if err != nil {
		return nil, err
	}
	secured := tls.Client(c, config)
	if err = secured.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("%w: TLS handshake: %v", ErrUnauthorized, err)
	}
	remote, err := certificateIdentity(secured.ConnectionState(), false)
	if err != nil {
		return nil, err
	}
	if o.Auth == nil {
		o.Auth = json.RawMessage("null")
	}
	h := hello{protocolVersion, o.Identity, o.Expected, o.Service, o.Auth}
	if !validHello(h) || o.Expected == nil && o.Handler != nil {
		return nil, ErrUnauthorized
	}
	if err = writeHandshake(secured, h); err != nil {
		return nil, err
	}
	raw, err := readPacket(secured, maxHandshake)
	if err != nil {
		return nil, err
	}
	var accepted acceptance
	if decode(raw, &accepted) != nil || accepted.Version != protocolVersion || accepted.Identity != remote {
		return nil, ErrUnauthorized
	}
	if accepted.Error != "" {
		return nil, &RemoteError{Message: accepted.Error, Code: "unauthorized"}
	}
	lifetime := o.Context
	if lifetime == nil {
		lifetime = context.Background()
	}
	finish()
	finish = func() {}
	s, err := newSession(lifetime, secured, remote, o.Service, o.Auth, o.Handler, true)
	if err != nil {
		return nil, err
	}
	s.start()
	return s, nil
}

// Server hosts service-specific admission and handlers on one listener. It never
// grants one service's permissions to another service. OnSession registers the
// session before receiving requests and must return promptly.
type Server struct {
	Identity   Identity
	PrivateKey ed25519.PrivateKey
	Authorize  Authorizer
	OnSession  func(*Session)
}

func (s *Server) Accept(ctx context.Context, c net.Conn) (_ *Session, err error) {
	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()
	config, err := tlsConfig(s.Identity, s.PrivateKey, nil, true)
	if err != nil {
		return nil, err
	}
	admissionCtx, cancelAdmission := context.WithTimeout(ctx, 10*time.Second)
	defer cancelAdmission()
	finish := handshakeContext(admissionCtx, c)
	defer func() { finish() }()
	secured := tls.Server(c, config)
	if err = secured.HandshakeContext(admissionCtx); err != nil {
		return nil, fmt.Errorf("%w: TLS handshake: %v", ErrUnauthorized, err)
	}
	remote, err := certificateIdentity(secured.ConnectionState(), true)
	if err != nil {
		return nil, err
	}
	raw, err := readPacket(secured, maxHandshake)
	if err != nil {
		return nil, err
	}
	var h hello
	// The application identity must be the identity that actually completed
	// mutual TLS, not a public key or node ID claimed in arbitrary metadata.
	if decode(raw, &h) != nil || !validHello(h) || h.Identity != remote || h.Expected != nil && *h.Expected != s.Identity {
		return nil, ErrUnauthorized
	}
	var handler Handler
	if s.Authorize == nil {
		err = ErrUnauthorized
	} else {
		handler, err = s.Authorize(admissionCtx, remote, h.Service, append(json.RawMessage(nil), h.Auth...))
	}
	a := acceptance{Version: protocolVersion, Identity: s.Identity}
	if err != nil {
		a.Error = boundedError(err).Message
	}
	if sendErr := writeHandshake(secured, a); sendErr != nil {
		return nil, sendErr
	}
	if err != nil {
		return nil, err
	}
	finish()
	finish = func() {}
	conn, err := newSession(ctx, secured, remote, h.Service, h.Auth, handler, false)
	if err != nil {
		return nil, err
	}
	if s.OnSession != nil {
		s.OnSession(conn)
	}
	conn.start()
	return conn, nil
}

// Serve bounds unauthenticated handshakes and closes established sessions on
// cancellation. Closing the listener also tears down sessions owned by Serve.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if validLocal(s.Identity, s.PrivateKey) != nil {
		return ErrUnauthorized
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	capacity := make(chan struct{}, 128)
	for {
		c, err := listener.Accept()
		if err != nil {
			canceled := ctx.Err() != nil
			cancel()
			if canceled {
				return nil
			}
			return err
		}
		select {
		case capacity <- struct{}{}:
		default:
			_ = c.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-capacity }()
			session, err := s.Accept(ctx, c)
			if err == nil {
				<-session.Done()
			}
		}()
	}
}
