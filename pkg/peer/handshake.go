package peer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

type hello struct {
	Version  int             `json:"version"`
	Identity Identity        `json:"identity"`
	Nonce    string          `json:"nonce"`
	Expected *Identity       `json:"expected"`
	Service  string          `json:"service"`
	Auth     json.RawMessage `json:"auth"`
}
type challenge struct {
	Version  int      `json:"version"`
	Identity Identity `json:"identity"`
	Nonce    string   `json:"nonce"`
	Hello    string   `json:"hello"`
}
type confirmation struct {
	Session string `json:"session"`
}
type acceptance struct {
	Session string `json:"session"`
	Error   string `json:"error,omitempty"`
}

func nonce() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
func validNonce(n string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(n)
	return err == nil && len(raw) == 32
}
func hash(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write(p)
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
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
func writeSigned(c net.Conn, key ed25519.PrivateKey, domain string, value any) ([]byte, error) {
	raw, err := seal(key, domain, value)
	if err == nil {
		err = writePacket(c, raw, maxHandshake)
	}
	return raw, err
}
func validHello(h hello) bool {
	return h.Version == 2 && h.Identity.Validate() == nil && validNonce(h.Nonce) && len(h.Service) > 0 && len(h.Service) <= 128 && len(h.Auth) <= 32*1024 && json.Valid(h.Auth) && (h.Expected == nil || h.Expected.Validate() == nil)
}

// Dial authenticates a peer over direct TCP. A nil Expected is suitable only
// for untrusted identity discovery; service admission must use a trusted pin.
// It never retries a request or falls back to another protocol.
func Dial(ctx context.Context, endpoint string, options DialOptions) (*Session, error) {
	if validLocal(options.Identity, options.PrivateKey) != nil {
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
	if o.Auth == nil {
		o.Auth = json.RawMessage("null")
	}
	h := hello{2, o.Identity, nonce(), o.Expected, o.Service, o.Auth}
	if !validHello(h) {
		return nil, fmt.Errorf("invalid peer handshake")
	}
	hraw, err := writeSigned(c, o.PrivateKey, "pot-peer/client-hello/v2", h)
	if err != nil {
		return nil, err
	}
	craw, err := readPacket(c, maxHandshake)
	if err != nil {
		return nil, err
	}
	var envelope signed
	var ch challenge
	if decode(craw, &envelope) != nil || decode(envelope.Payload, &ch) != nil || ch.Version != 2 || !validNonce(ch.Nonce) || ch.Hello != hash(hraw) || open(craw, ch.Identity, "pot-peer/server-challenge/v2", &ch) != nil || (o.Expected != nil && *o.Expected != ch.Identity) {
		return nil, ErrUnauthorized
	}
	sid := hash(hraw, craw)
	if _, err = writeSigned(c, o.PrivateKey, "pot-peer/client-confirm/v2", confirmation{sid}); err != nil {
		return nil, err
	}
	araw, err := readPacket(c, maxHandshake)
	if err != nil {
		return nil, err
	}
	var accepted acceptance
	if open(araw, ch.Identity, "pot-peer/server-accept/v2", &accepted) != nil || accepted.Session != sid {
		return nil, ErrUnauthorized
	}
	if accepted.Error != "" {
		return nil, &RemoteError{Message: accepted.Error, Code: "unauthorized"}
	}
	lifetime := o.Context
	if lifetime == nil {
		lifetime = context.Background()
	}
	// Clear handshake deadlines before starting either session goroutine.
	finish()
	finish = func() {}
	s := newSession(lifetime, c, o.Identity, o.PrivateKey, ch.Identity, sid, o.Service, o.Auth, o.Handler, "client", "server")
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
	if validLocal(s.Identity, s.PrivateKey) != nil {
		return nil, ErrUnauthorized
	}
	admissionCtx, cancelAdmission := context.WithTimeout(ctx, 10*time.Second)
	defer cancelAdmission()
	finish := handshakeContext(admissionCtx, c)
	defer func() { finish() }()
	hraw, err := readPacket(c, maxHandshake)
	if err != nil {
		return nil, err
	}
	var envelope signed
	var h hello
	if decode(hraw, &envelope) != nil || decode(envelope.Payload, &h) != nil || !validHello(h) || open(hraw, h.Identity, "pot-peer/client-hello/v2", &h) != nil || (h.Expected != nil && *h.Expected != s.Identity) {
		return nil, ErrUnauthorized
	}
	craw, err := writeSigned(c, s.PrivateKey, "pot-peer/server-challenge/v2", challenge{2, s.Identity, nonce(), hash(hraw)})
	if err != nil {
		return nil, err
	}
	sid := hash(hraw, craw)
	fraw, err := readPacket(c, maxHandshake)
	if err != nil {
		return nil, err
	}
	var confirmed confirmation
	if open(fraw, h.Identity, "pot-peer/client-confirm/v2", &confirmed) != nil || confirmed.Session != sid {
		return nil, ErrUnauthorized
	}
	var handler Handler
	if s.Authorize == nil {
		err = ErrUnauthorized
	} else {
		handler, err = s.Authorize(admissionCtx, h.Identity, h.Service, append(json.RawMessage(nil), h.Auth...))
	}
	a := acceptance{Session: sid}
	if err != nil {
		a.Error = boundedError(err).Message
	}
	if _, sendErr := writeSigned(c, s.PrivateKey, "pot-peer/server-accept/v2", a); sendErr != nil {
		return nil, sendErr
	}
	if err != nil {
		return nil, err
	}
	finish()
	finish = func() {}
	conn := newSession(ctx, c, s.Identity, s.PrivateKey, h.Identity, sid, h.Service, h.Auth, handler, "server", "client")
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
