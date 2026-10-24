package peer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/testutil"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testTLSClient(t *testing.T, endpoint string, identity Identity, key ed25519.PrivateKey, expected *Identity, change func(*tls.Config)) *tls.Conn {
	t.Helper()
	config, err := tlsConfig(identity, key, expected, false)
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(config)
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", strings.TrimPrefix(endpoint, "tcp://"), config)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	t.Cleanup(func() { conn.Close() })
	return conn
}

func testRawTLSServer(t *testing.T, config *tls.Config, handle func(*tls.Conn) error) (string, <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 3*time.Second))
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer raw.Close()
		_ = raw.SetDeadline(time.Now().Add(3 * time.Second))
		conn := tls.Server(raw, config)
		if err = conn.HandshakeContext(ctx); err == nil && handle != nil {
			err = handle(conn)
		}
		done <- err
	}()
	t.Cleanup(func() { cancel(); ln.Close() })
	return "tcp://" + ln.Addr().String(), done
}

func TestPrivateKeyCannotBorrowAnotherPublicPin(t *testing.T) {
	victim, _ := testIdentity(t, "victim")
	_, unrelated := testIdentity(t, "unrelated")
	public, _ := base64.RawURLEncoding.DecodeString(victim.PublicKey)
	forged := append(ed25519.PrivateKey(nil), unrelated...)
	copy(forged[ed25519.SeedSize:], public)
	if validLocal(victim, forged) == nil {
		t.Fatal("unrelated seed with copied public suffix accepted")
	}
	if _, err := tlsConfig(victim, forged, nil, true); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("forged local identity generated a TLS configuration", err)
	}
}

func TestTLSApplicationIdentityMustMatchProvedClientKey(t *testing.T) {
	for _, field := range []string{"node", "key"} {
		t.Run(field, func(t *testing.T) {
			var admissions atomic.Int32
			endpoint, server, _ := testServer(t, func(context.Context, Identity, string, json.RawMessage) (Handler, error) {
				admissions.Add(1)
				return nil, nil
			}, nil)
			attacker, key := testIdentity(t, "attacker")
			victim, _ := testIdentity(t, "victim")
			conn := testTLSClient(t, endpoint, attacker, key, &server, nil)
			claimed := attacker
			if field == "node" {
				claimed.NodeID = victim.NodeID
			} else {
				claimed.PublicKey = victim.PublicKey
			}
			if err := writeHandshake(conn, hello{protocolVersion, claimed, &server, "mail", json.RawMessage("null")}); err != nil {
				t.Fatal(err)
			}
			if _, err := readPacket(conn, maxHandshake); err == nil {
				t.Fatal("application identity spoof accepted")
			}
			if admissions.Load() != 0 {
				t.Fatal("spoof reached service admission")
			}
		})
	}
}

func TestTLSApplicationIdentityMustMatchProvedServerKey(t *testing.T) {
	server, key := testIdentity(t, "server")
	config, _ := tlsConfig(server, key, nil, true)
	impostor, _ := testIdentity(t, "impostor")
	endpoint, done := testRawTLSServer(t, config, func(conn *tls.Conn) error {
		if _, err := readPacket(conn, maxHandshake); err != nil {
			return err
		}
		return writeHandshake(conn, acceptance{Version: protocolVersion, Identity: impostor})
	})
	client, clientKey := testIdentity(t, "client")
	if _, err := Dial(context.Background(), endpoint, DialOptions{Identity: client, PrivateKey: clientKey, Expected: &server, Service: "mail"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("TLS server's JSON identity spoof accepted", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCopiedPinnedCertificateWithoutPrivateKeyCannotReadAuth(t *testing.T) {
	victim, victimKey := testIdentity(t, "victim")
	_, attackerKey := testIdentity(t, "attacker")
	config, _ := tlsConfig(victim, victimKey, nil, true)
	// The attacker has copied the entire public certificate, but not its key.
	config.Certificates[0].PrivateKey = attackerKey
	var reachedApplication atomic.Bool
	endpoint, done := testRawTLSServer(t, config, func(conn *tls.Conn) error {
		reachedApplication.Store(true)
		_, err := readPacket(conn, maxHandshake)
		return err
	})
	client, clientKey := testIdentity(t, "client")
	if _, err := Dial(context.Background(), endpoint, DialOptions{Identity: client, PrivateKey: clientKey, Expected: &victim, Service: "mail", Auth: json.RawMessage(`{"capability":"must-stay-secret"}`)}); err == nil {
		t.Fatal("copied certificate authenticated without its private key")
	}
	if err := <-done; err == nil || reachedApplication.Load() {
		t.Fatal("copied pin reached application handshake", err)
	}
}

func TestTLSRejectsMissingClientCertificateBeforeAdmission(t *testing.T) {
	var admissions atomic.Int32
	endpoint, server, _ := testServer(t, func(context.Context, Identity, string, json.RawMessage) (Handler, error) {
		admissions.Add(1)
		return nil, nil
	}, nil)
	client, key := testIdentity(t, "client")
	conn := testTLSClient(t, endpoint, client, key, &server, func(config *tls.Config) { config.Certificates = nil })
	// TLS 1.3 clients may finish their handshake before receiving a server's
	// rejection of their final flight. No application acceptance may follow.
	_ = writeHandshake(conn, hello{protocolVersion, client, &server, "mail", json.RawMessage("null")})
	if _, err := readPacket(conn, maxHandshake); err == nil || admissions.Load() != 0 {
		t.Fatal("client without a key proof reached admission", err)
	}
}

func TestCopiedClientCertificateCannotBorrowSavedPin(t *testing.T) {
	var admissions atomic.Int32
	endpoint, server, _ := testServer(t, func(context.Context, Identity, string, json.RawMessage) (Handler, error) {
		admissions.Add(1)
		return nil, nil
	}, nil)
	victim, victimKey := testIdentity(t, "victim")
	_, attackerKey := testIdentity(t, "attacker")
	config, err := tlsConfig(victim, victimKey, &server, false)
	if err != nil {
		t.Fatal(err)
	}
	config.Certificates[0].PrivateKey = attackerKey
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", strings.TrimPrefix(endpoint, "tcp://"), config)
	if err == nil {
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_ = writeHandshake(conn, hello{protocolVersion, victim, &server, "mail", json.RawMessage("null")})
		if _, err = readPacket(conn, maxHandshake); err == nil {
			t.Fatal("client copied a pinned certificate without proving its private key")
		}
	}
	if admissions.Load() != 0 {
		t.Fatal("copied client pin reached admission")
	}
}

func TestTLSRejectsOldVersionAndWrongProtocol(t *testing.T) {
	for _, variant := range []string{"tls12", "alpn"} {
		t.Run(variant, func(t *testing.T) {
			server, key := testIdentity(t, "server")
			config, _ := tlsConfig(server, key, nil, true)
			if variant == "tls12" {
				config.MinVersion, config.MaxVersion = tls.VersionTLS12, tls.VersionTLS12
			} else {
				config.NextProtos = []string{"another-protocol"}
			}
			endpoint, done := testRawTLSServer(t, config, nil)
			client, clientKey := testIdentity(t, "client")
			if _, err := Dial(context.Background(), endpoint, DialOptions{Identity: client, PrivateKey: clientKey, Expected: &server, Service: "mail"}); err == nil {
				t.Fatal("incompatible TLS transport accepted")
			}
			if err := <-done; err == nil {
				t.Fatal("incompatible handshake succeeded")
			}
		})
	}
}

func TestUnpinnedDiscoveryCannotSendCapabilitiesOrExposeHandler(t *testing.T) {
	endpoint, _, _ := testServer(t, func(_ context.Context, _ Identity, service string, auth json.RawMessage) (Handler, error) {
		if service != "identity" || string(auth) != "null" {
			return nil, ErrUnauthorized
		}
		return nil, nil
	}, nil)
	client, key := testIdentity(t, "client")
	for _, options := range []DialOptions{
		{Identity: client, PrivateKey: key, Service: "mail"},
		{Identity: client, PrivateKey: key, Service: "identity", Auth: json.RawMessage(`{"secret":"capability"}`)},
		{Identity: client, PrivateKey: key, Service: "identity", Handler: func(context.Context, Request) (json.RawMessage, error) { return nil, nil }},
	} {
		if _, err := Dial(context.Background(), endpoint, options); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("unpinned endpoint gained authority", err)
		}
	}
	s, err := Dial(context.Background(), endpoint, DialOptions{Identity: client, PrivateKey: key, Service: "identity"})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestCertificateRenewalPreservesPinAndUsesFreshTLSHandshake(t *testing.T) {
	endpoint, server, host := testServer(t, func(context.Context, Identity, string, json.RawMessage) (Handler, error) { return nil, nil }, nil)
	client, key := testIdentity(t, "client")
	var previous []byte
	for range 2 {
		s, err := Dial(context.Background(), endpoint, DialOptions{Identity: client, PrivateKey: key, Expected: &server, Service: "mail"})
		if err != nil {
			t.Fatal(err)
		}
		state := s.conn.(*tls.Conn).ConnectionState()
		cert := state.PeerCertificates[0]
		if state.Version != tls.VersionTLS13 || state.DidResume || state.NegotiatedProtocol != peerALPN || bytes.Equal(previous, cert.Raw) {
			t.Fatal("certificate did not renew with a full TLS 1.3 handshake")
		}
		if public := cert.PublicKey.(ed25519.PublicKey); NewIdentity(host.Identity.NodeID, public) != server {
			t.Fatal("certificate renewal changed identity pin")
		}
		previous = cert.Raw
		s.Close()
	}
}

// wireConn records actual TLS ciphertext, with an optional one-shot on-path
// modification. The TLS implementation still owns framing and authentication.
type wireConn struct {
	net.Conn
	mu       sync.Mutex
	wire     bytes.Buffer
	incoming bytes.Buffer
	next     func([]byte) []byte
}

func (c *wireConn) Read(raw []byte) (int, error) {
	n, err := c.Conn.Read(raw)
	c.mu.Lock()
	c.incoming.Write(raw[:n])
	c.mu.Unlock()
	return n, err
}
func (c *wireConn) Write(raw []byte) (int, error) {
	c.mu.Lock()
	c.wire.Write(raw)
	transform := c.next
	c.next = nil
	c.mu.Unlock()
	n := len(raw)
	if transform != nil {
		raw = transform(append([]byte(nil), raw...))
	}
	_, err := c.Conn.Write(raw)
	if err != nil {
		return 0, err
	}
	return n, nil
}
func (c *wireConn) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.wire.Bytes()...)
}
func (c *wireConn) transform(next func([]byte) []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next = next
}

func testObservedSession(t *testing.T, endpoint string, server Identity, client Identity, key ed25519.PrivateKey) (*Session, *wireConn) {
	t.Helper()
	raw, err := net.Dial("tcp", strings.TrimPrefix(endpoint, "tcp://"))
	if err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{Conn: raw}
	s, err := dialConn(context.Background(), wire, DialOptions{Identity: client, PrivateKey: key, Expected: &server, Service: "mail", Auth: json.RawMessage(`{"capability":"secret-admission-value"}`)})
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, wire
}

func TestTLSWireTamperingReplayAndCrossConnectionReplay(t *testing.T) {
	for _, attack := range []string{"tamper", "replay", "cross-connection", "reflection"} {
		t.Run(attack, func(t *testing.T) {
			delivered := make(chan struct{}, 2)
			accepted := make(chan *Session, 2)
			endpoint, server, _ := testServer(t, func(context.Context, Identity, string, json.RawMessage) (Handler, error) {
				return func(context.Context, Request) (json.RawMessage, error) { delivered <- struct{}{}; return nil, nil }, nil
			}, func(s *Session) { accepted <- s })
			client, key := testIdentity(t, "client")
			s, wire := testObservedSession(t, endpoint, server, client, key)
			remote := <-accepted
			var record []byte
			wire.transform(func(raw []byte) []byte { record = append([]byte(nil), raw...); return raw })
			request := Request{ID: "first", Method: "event.changed", Params: json.RawMessage(`"private-business-content"`)}
			if err := s.Notify(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			select {
			case <-delivered:
			case <-time.After(time.Second):
				t.Fatal("initial event not delivered")
			}
			for _, secret := range []string{"secret-admission-value", "private-business-content"} {
				if bytes.Contains(wire.bytes(), []byte(secret)) {
					t.Fatal("TLS wire exposed plaintext")
				}
			}
			if attack == "cross-connection" {
				s.Close()
				s, wire = testObservedSession(t, endpoint, server, client, key)
				remote = <-accepted
			}
			if attack == "reflection" {
				wire.mu.Lock()
				wire.incoming.Reset()
				wire.mu.Unlock()
				if _, err := s.Call(context.Background(), Request{ID: "probe", Method: "probe", Params: json.RawMessage("null")}); err != nil {
					t.Fatal(err)
				}
				<-delivered
				wire.mu.Lock()
				record = append([]byte(nil), wire.incoming.Bytes()...)
				wire.mu.Unlock()
				if len(record) == 0 {
					t.Fatal("no encrypted server response captured")
				}
			}

			wire.transform(func(raw []byte) []byte {
				if attack == "tamper" {
					raw[len(raw)-1] ^= 1
					return raw
				}
				return record
			})
			request.ID = "second"
			_ = s.Notify(context.Background(), request)
			select {
			case <-remote.Done():
			case <-time.After(time.Second):
				t.Fatal("modified/replayed TLS record left session open")
			}
			select {
			case <-delivered:
				t.Fatal("modified/replayed record reached handler")
			default:
			}
		})
	}
}

func TestTLSHandshakeReplayCannotAuthorizeAnotherConnection(t *testing.T) {
	var admissions atomic.Int32
	endpoint, server, _ := testServer(t, func(context.Context, Identity, string, json.RawMessage) (Handler, error) {
		admissions.Add(1)
		return nil, nil
	}, nil)
	client, key := testIdentity(t, "client")
	s, wire := testObservedSession(t, endpoint, server, client, key)
	captured := wire.bytes()
	s.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(endpoint, "tcp://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err = conn.Write(captured); err != nil {
		t.Fatal(err)
	}
	// Drain the fresh server flight. The captured client's later handshake
	// records cannot authenticate under the new server's ephemeral secret.
	_, _ = io.Copy(io.Discard, conn)
	if admissions.Load() != 1 {
		t.Fatal("replayed handshake reached admission", admissions.Load())
	}
}

func TestTLSRejectsExpiredCertificate(t *testing.T) {
	server, key := testIdentity(t, "server")
	config, _ := tlsConfig(server, key, nil, true)
	cert, _ := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	cert.NotBefore = time.Now().Add(-2 * time.Hour)
	cert.NotAfter = time.Now().Add(-time.Hour)
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	config.Certificates[0].Certificate = [][]byte{der}
	endpoint, done := testRawTLSServer(t, config, nil)
	client, clientKey := testIdentity(t, "client")
	if _, err = Dial(context.Background(), endpoint, DialOptions{Identity: client, PrivateKey: clientKey, Expected: &server, Service: "mail"}); err == nil {
		t.Fatal("expired certificate accepted")
	}
	if err = <-done; err == nil {
		t.Fatal("expired certificate handshake succeeded")
	}
}

func TestTLSRejectsCleartextPeerHello(t *testing.T) {
	var admissions atomic.Int32
	endpoint, server, _ := testServer(t, func(context.Context, Identity, string, json.RawMessage) (Handler, error) {
		admissions.Add(1)
		return nil, nil
	}, nil)
	client, _ := testIdentity(t, "client")
	conn, err := net.Dial("tcp", strings.TrimPrefix(endpoint, "tcp://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if err = writeHandshake(conn, hello{2, client, &server, "mail", json.RawMessage("null")}); err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, conn)
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("cleartext peer was not promptly rejected")
	}
	if admissions.Load() != 0 {
		t.Fatal("cleartext protocol reached service admission")
	}
}
