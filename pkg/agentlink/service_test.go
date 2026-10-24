package agentlink

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"sync"
	"sync/atomic"

	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"testing"
	"time"
)

type sqlMailbox struct {
	*assistant.Mailbox
	db *sql.DB
}

func (m sqlMailbox) PendingNode(ctx context.Context, local, peer string) ([]protocol.Envelope, error) {
	return m.Mailbox.PendingNode(ctx, local, peer)
}

type fixture struct {
	t                      *testing.T
	root, id, endpoint     string
	db                     *sql.DB
	secret                 *secrets.Store
	keys                   *secrets.MemoryKey
	key                    ed25519.PrivateKey
	mail                   sqlMailbox
	service                *Service
	listener               net.Listener
	cancel                 context.CancelFunc
	done                   chan struct{}
	dropPair, dropExchange atomic.Bool
	accepted               atomic.Int32
	exchanges              atomic.Int32
}

func newFixture(t *testing.T, id string) *fixture {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	f := &fixture{t: t, root: t.TempDir(), id: id, key: key, keys: keys}
	f.open()
	f.start("127.0.0.1:0")
	t.Cleanup(func() { f.stop(); f.close() })
	return f
}
func (f *fixture) open() {
	f.t.Helper()
	var err error
	f.db, err = storage.OpenDatabase(filepath.Join(f.root, "control.sqlite"), storage.ControlDatabase)
	if err != nil {
		f.t.Fatal(err)
	}
	f.secret, err = secrets.Open(filepath.Join(f.root, "control.sqlite"), f.keys)
	if err != nil {
		f.t.Fatal(err)
	}
	db, err := storage.OpenDatabase(filepath.Join(f.root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		f.t.Fatal(err)
	}
	mail, err := assistant.OpenMailbox(db, f.id, assistant.MailboxOptions{})
	if err != nil {
		db.Close()
		f.t.Fatal(err)
	}
	f.mail = sqlMailbox{mail, db}
	f.service, err = New(Options{DB: f.db, Secrets: f.secret, Mailbox: f.mail, HostIdentity: peer.NewIdentity(f.id, f.key.Public().(ed25519.PublicKey)), HostKey: f.key, Identity: func(_ context.Context, id string) (Identity, error) {
		if id == "" {
			return Identity{Address: protocol.AgentRef{NodeID: f.id, Kind: "node", AgentID: f.id}, Name: f.id, Adapter: "pot"}, nil
		}
		if id != "assistant" && id != "other" {
			return Identity{}, ErrNotFound
		}
		return Identity{Address: protocol.AgentRef{NodeID: f.id, Kind: "assistant", AgentID: id}, Name: f.id + " " + id, Adapter: "pot"}, nil
	}})
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) start(address string) {
	f.t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		f.t.Fatal(err)
	}
	f.listener = listener
	f.endpoint = "tcp://" + listener.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan struct{})
	server := peer.Server{Identity: f.service.options.HostIdentity, PrivateKey: f.key, Authorize: func(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
		handler, err := f.service.Authorize(ctx, remote, service, auth)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
			if request.Method == "exchange" {
				f.exchanges.Add(1)
			}
			raw, err := handler(ctx, request)
			if err == nil && (request.Method == "exchange" && f.dropExchange.Swap(false) || request.Method == "pair" && f.dropPair.Swap(false)) {
				return nil, ErrUnavailable
			}
			return raw, err
		}, nil
	}, OnSession: func(session *peer.Session) { f.accepted.Add(1); f.service.Attach(session) }}
	go func() { defer close(f.done); _ = server.Serve(ctx, listener) }()
}
func (f *fixture) stop() { f.cancel(); f.listener.Close(); f.service.closeSessions(); <-f.done }
func (f *fixture) close() {
	f.service.Close()
	f.mail.db.Close()
	f.secret.Close()
	f.db.Close()
}
func (f *fixture) restart() {
	address := f.listener.Addr().String()
	f.stop()
	f.close()
	f.open()
	f.start(address)
}
func (f *fixture) identity() Identity {
	value, _ := f.service.options.Identity(context.Background(), "assistant")
	return value
}
func connect(t *testing.T, a, b *fixture) Connection {
	t.Helper()
	inv, err := b.service.Invite(context.Background(), b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	value, err := a.service.Connect(context.Background(), inv.Address)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func send(t *testing.T, a, b *fixture, id, text string) protocol.Envelope {
	t.Helper()
	message, err := a.mail.Send(context.Background(), id, a.identity().Address, protocol.SendMail{To: b.identity().Address, Text: text}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
func mailCount(t *testing.T, f *fixture, id string) int {
	t.Helper()
	var count int
	if err := f.mail.db.QueryRow(`SELECT COUNT(*) FROM agent_messages WHERE id=?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestTCPConnectionUsesOneSignedSessionForBothDirectionsAndRestart(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	c := connect(t, a, b)
	if c.SchemaVersion != 5 || c.PeerHost != b.service.options.HostIdentity || c.State != "ready" || c.LastSeen == nil {
		t.Fatal(c)
	}
	remote, err := b.service.Get(ctx, c.ID)
	if err != nil || remote.Endpoint != "" || remote.PeerHost != a.service.options.HostIdentity {
		t.Fatal(remote, err)
	}
	send(t, a, b, "outgoing", "from a")
	send(t, b, a, "incoming", "from b")
	if err = a.service.Sync(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = b.service.Sync(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, a, "incoming") != 1 || mailCount(t, b, "outgoing") != 1 || b.accepted.Load() != 1 {
		t.Fatal("not delivered on one persistent bidirectional session", b.accepted.Load())
	}
	idleExchanges := a.exchanges.Load() + b.exchanges.Load()
	for range 3 {
		if err = a.service.Sync(ctx, c.ID); err != nil {
			t.Fatal(err)
		}
	}
	if b.accepted.Load() != 1 || a.exchanges.Load()+b.exchanges.Load() != idleExchanges {
		t.Fatal("idle exchange opened TCP sessions or polled peer", b.accepted.Load(), a.exchanges.Load(), b.exchanges.Load())
	}
	a.restart()
	b.restart()
	send(t, b, a, "after-restart", "queued while offline")
	if err = a.service.Sync(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err = b.service.Sync(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, a, "after-restart") != 1 {
		t.Fatal(err)
	}
}
func TestTCPPairAndDeliveryResponseLossRecoverOriginalReceipts(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	inv, _ := b.service.Invite(ctx, b.endpoint)
	b.dropPair.Store(true)
	c, err := a.service.Connect(ctx, inv.Address)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	a.restart()
	b.restart()
	recovered, err := a.service.Connect(ctx, inv.Address)
	if err != nil || recovered.ID != c.ID {
		t.Fatal(recovered, err)
	}
	send(t, a, b, "durable-original", "one immutable original")
	b.dropExchange.Store(true)
	if err = a.service.Sync(ctx, c.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if mailCount(t, b, "durable-original") != 1 {
		t.Fatal("receiver did not commit")
	}
	a.restart()
	b.restart()
	if err = a.service.Sync(ctx, c.ID); err != nil || mailCount(t, b, "durable-original") != 1 {
		t.Fatal(err)
	}
}
func TestTCPInvitationClaimBindsSignedHostAndNodeRoute(t *testing.T) {
	ctx := context.Background()
	a, b, c := newFixture(t, "control-a"), newFixture(t, "control-b"), newFixture(t, "control-c")
	inv, _ := b.service.Invite(ctx, b.endpoint)
	again, _ := b.service.Invite(ctx, b.endpoint)
	if inv.Address != again.Address {
		t.Fatal("unclaimed invitation was not reused")
	}
	parsed, _ := parseAddress(ctx, inv.Address)
	connection, err := a.service.Connect(ctx, inv.Address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.service.Connect(ctx, inv.Address); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for _, f := range []*fixture{a, b} {
		if _, err = f.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(f.root, "control.sqlite"))
		if err != nil || strings.Contains(string(raw), parsed.Code) {
			t.Fatal("plaintext invitation persisted", err)
		}
	}
	session, err := a.service.ensureSession(ctx, connection)
	if err != nil {
		t.Fatal(err)
	}
	message := send(t, a, b, "bound-message", "peer reference")
	for _, change := range []func(*protocol.Envelope){func(e *protocol.Envelope) { e.From.NodeID = "other-node" }, func(e *protocol.Envelope) { e.To.NodeID = "other-node" }, func(e *protocol.Envelope) { e.From.Kind = "runtime" }, func(e *protocol.Envelope) { e.Kind = "plugin_event" }} {
		forged := message
		change(&forged)
		var response exchangeResponse
		if err = a.service.call(ctx, session, "exchange", exchangeRequest{ID: connection.ID, Messages: []protocol.Envelope{forged}}, &response); !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if mailCount(t, b, message.ID) != 0 {
		t.Fatal("forged route admitted")
	}
	var response exchangeResponse
	if err = a.service.call(ctx, session, "exchange", exchangeRequest{ID: newID()}, &response); !errors.Is(err, ErrInvalid) {
		t.Fatal("session authorized another connection", err)
	}
	if err = a.service.Sync(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
}
func TestTCPRejectsAnotherHostKeyEvenWhenNodeIDIsSame(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	_, wrongKey, _ := ed25519.GenerateKey(rand.Reader)
	auth, _ := json.Marshal(sessionAuth{ID: connection.ID})
	session, err := peer.Dial(ctx, b.endpoint, peer.DialOptions{Identity: peer.NewIdentity(a.id, wrongKey.Public().(ed25519.PublicKey)), PrivateKey: wrongKey, Expected: &b.service.options.HostIdentity, Service: ServiceName, Auth: auth})
	if err == nil {
		session.Close()
		t.Fatal("another signing key claimed established control identity")
	}
	inv, _ := b.service.Invite(ctx, b.endpoint)
	parsed, _ := parseAddress(ctx, inv.Address)
	parsed.Host = peer.NewIdentity(b.id, wrongKey.Public().(ed25519.PublicKey))
	raw, _ := json.Marshal(parsed)
	address := b.endpoint + "#" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err = a.service.Connect(ctx, address); err == nil {
		t.Fatal("invitation public-key pin was ignored")
	}
}
func TestTCPRevokeFencesMailAndPropagatesAcrossReconnect(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	b.stop()
	if err := a.service.Revoke(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	value, _ := a.service.Get(ctx, connection.ID)
	if value.State != "revoked" || !value.RevocationPending {
		t.Fatal(value)
	}
	a.restart()
	b.start(strings.TrimPrefix(b.endpoint, "tcp://"))
	value, _ = a.service.Get(ctx, connection.ID)
	if err := a.service.propagateRevoke(ctx, value); err != nil {
		value, _ = a.service.Get(ctx, connection.ID)
		if retry := a.service.propagateRevoke(ctx, value); retry != nil {
			t.Fatal(err, retry)
		}
	}
	for _, f := range []*fixture{a, b} {
		value, _ := f.service.Get(ctx, connection.ID)
		if value.State != "revoked" || value.RevocationPending {
			t.Fatal(value)
		}
	}
	renewed := connect(t, a, b)
	if renewed.ID == connection.ID {
		t.Fatal("reused revoked connection identity")
	}
}
func TestTCPFailedRevocationDiscardsSessionAndPreservesPending(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	if err := a.service.revokeLocal(ctx, connection, true); err != nil {
		t.Fatal(err)
	}
	pending, err := a.service.Get(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Cancelling an unsent notification preserves the pending tombstone.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.service.propagateRevoke(canceled, pending); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if a.service.session(connection.ID) != nil {
		t.Fatal("cancelled revocation retained a reusable transport")
	}
	pending, err = a.service.Get(ctx, connection.ID)
	if err != nil || pending.State != "revoked" || !pending.RevocationPending {
		t.Fatal("failed attempt cleared the durable revocation", pending, err)
	}
	remote, err := b.service.Get(ctx, connection.ID)
	if err != nil || remote.State == "revoked" || remote.RevocationPending {
		t.Fatal("canceled request reached the remote", remote, err)
	}
	// The receiver closes its session while applying revocation. If that loses
	// the response, a subsequent fresh handshake observes the durable tombstone.
	for range 2 {
		err = a.service.propagateRevoke(ctx, pending)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrUnavailable) && !errors.Is(err, peer.ErrUncertain) {
			t.Fatal(err)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []*fixture{a, b} {
		value, err := f.service.Get(ctx, connection.ID)
		if err != nil || value.State != "revoked" || value.RevocationPending {
			t.Fatal(value, err)
		}
	}
}
func TestTCPListenerRevocationAndPendingBacklogIsolation(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	other, _ := a.service.options.Identity(ctx, "other")
	for index := 0; index < protocol.MailPageSize+1; index++ {
		if _, err := a.mail.Send(ctx, fmt.Sprintf("other-%d", index), other.Address, protocol.SendMail{To: protocol.AgentRef{NodeID: "another-node", Kind: "assistant", AgentID: "main"}, Text: "another node"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	send(t, a, b, "bound-late", "not starved")
	if err := a.service.Sync(ctx, connection.ID); err != nil || mailCount(t, b, "bound-late") != 1 || mailCount(t, b, "other-0") != 0 {
		t.Fatal(err)
	}
	if err := b.service.Revoke(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	// Closing the remote socket is observed asynchronously. An idle Sync can
	// still see its live session before the reader processes EOF; it must then
	// reconnect, discover the remote tombstone and fence further delivery.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := a.service.Sync(ctx, connection.ID)
		if errors.Is(err, ErrRevoked) {
			break
		}
		if err != nil && !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("remote revocation was not observed", err)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestTCPInvitationExpiryRepairAndNoHTTPCompatibility(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	inv, _ := b.service.Invite(ctx, b.endpoint)
	parsed, _ := parseAddress(ctx, inv.Address)
	if _, err := b.db.Exec(`UPDATE agent_link_invitations SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), parsed.Invitation); err != nil {
		t.Fatal(err)
	}
	if _, err := a.service.Connect(ctx, inv.Address); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	fresh, _ := b.service.Invite(ctx, b.endpoint)
	if _, err := a.service.Connect(ctx, fresh.Address); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://example.test", "http://127.0.0.1:8792", "tcp://127.0.0.1:9999/other", "tcp://user@127.0.0.1:9999", "tcp://127.0.0.1:9999?x=1"} {
		if _, err := b.service.Invite(ctx, raw); err == nil {
			t.Fatal("obsolete or expanded endpoint accepted", raw)
		}
	}
	u, _ := url.Parse(fresh.Address)
	u.RawQuery = "x=1"
	if _, err := parseAddress(ctx, u.String()); err == nil {
		t.Fatal("query in invitation accepted")
	}
}

func TestTCPListenerPushesWithoutDialerPollingAndIdleHasNoBusinessRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := newFixture(t, "push-a"), newFixture(t, "push-b")
	connection := connect(t, a, b)
	b.service.options.PollInterval = 100 * time.Millisecond
	done := make(chan struct{})
	go func() { defer close(done); b.service.Run(ctx) }()
	defer func() { cancel(); <-done }()
	updates := a.mail.Changes.Subscribe()
	defer updates.Close()
	send(t, b, a, "listener-push", "sent using the existing incoming session")
	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()
	for mailCount(t, a, "listener-push") == 0 {
		if _, err := updates.Next(waitCtx); err != nil {
			t.Fatal("listener did not push independently of dialer", err)
		}
	}
	// An empty local outbox never produces a remote exchange; peer's signed
	// heartbeat, rather than empty business polls, maintains socket liveness.
	before := b.exchanges.Load()
	for range 5 {
		if err := a.service.Sync(ctx, connection.ID); err != nil {
			t.Fatal(err)
		}
	}
	if b.exchanges.Load() != before || b.accepted.Load() != 1 {
		t.Fatal("idle business poll or replacement connection", before, b.exchanges.Load(), b.accepted.Load())
	}
}

type memoryNodeSecrets map[string][]byte

func (m memoryNodeSecrets) Put(_ context.Context, id string, value []byte) error {
	m[id] = append([]byte(nil), value...)
	return nil
}
func (m memoryNodeSecrets) Get(_ context.Context, id string) ([]byte, error) {
	value, ok := m[id]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}
func (m memoryNodeSecrets) Delete(_ context.Context, id string) error { delete(m, id); return nil }
func TestFreshCustomSecretStoreCanReopenAfterConnecting(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "custom-a"), newFixture(t, "custom-b")
	options := a.service.options
	options.Secrets = memoryNodeSecrets{}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	a.service = service
	invite, err := b.service.Invite(ctx, b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := a.service.Connect(ctx, invite.Address)
	if err != nil {
		t.Fatal(err)
	}
	a.service.closeSessions()
	a.service, err = New(options)
	if err != nil {
		t.Fatal("fresh custom store acquired a false migration obligation", err)
	}
	if _, err = a.service.Get(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
}

func TestInitialSchemaReopenRetainsSignedConnection(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "anchor-a"), newFixture(t, "anchor-b")
	c := connect(t, a, b)
	message := send(t, a, b, "before-schema-reopen", "preserve original receipt")
	if err := a.service.Sync(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	a.stop()
	b.stop()
	endpoints := []string{a.endpoint, b.endpoint}
	for _, side := range []*fixture{a, b} {
		side.close()
	}
	for index, side := range []*fixture{a, b} {
		side.open()
		side.start(strings.TrimPrefix(endpoints[index], "tcp://"))
	}
	if err := a.service.Sync(ctx, c.ID); err != nil {
		t.Fatal("established node pins no longer reconnect", err)
	}
	for _, side := range []*fixture{a, b} {
		current, err := side.service.Get(ctx, c.ID)
		if err != nil || current.ID != c.ID || current.SchemaVersion != 5 || current.Local.Address.Kind != "node" || current.Peer.Address.Kind != "node" {
			t.Fatal(current, err)
		}
	}
	if mailCount(t, b, message.ID) != 1 {
		t.Fatal("reopen replayed or removed accepted message")
	}
	next := send(t, a, b, "after-schema-reopen", "same signed connection")
	if err := a.service.Sync(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, b, next.ID) != 1 {
		t.Fatal("reopened connection could not deliver")
	}
}

func TestConcurrentReconnectUsesOneAuthenticatedSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := newFixture(t, "dial-a"), newFixture(t, "dial-b")
	a.service.options.Backend = &testBackend{f: a}
	b.service.options.Backend = &testBackend{f: b}
	connection := connect(t, a, b)
	a.service.closeSessions()
	before := b.accepted.Load()
	const workers = 16
	start := make(chan struct{})
	type result struct {
		session *peer.Session
		err     error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var accepted Directory
			err := a.service.callConnection(ctx, connection, "directory", struct{}{}, &accepted)
			if err == nil && accepted.NodeID != b.id {
				err = ErrConflict
			}
			results <- result{a.service.session(connection.ID), err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var first *peer.Session
	for value := range results {
		if value.err != nil {
			t.Fatal(value.err)
		}
		if first == nil {
			first = value.session
		}
		if value.session != first {
			t.Fatal("concurrent callers replaced each other's authenticated session")
		}
	}
	if got := b.accepted.Load() - before; got != 1 {
		t.Fatalf("opened %d reconnect sessions, want 1", got)
	}
	if _, err := first.Call(ctx, peer.Request{ID: "identity-after-reconnect", Method: "identity", Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatal("shared session was closed", err)
	}
}

func TestEnsureSessionAfterCloseDoesNotReadReleasedStores(t *testing.T) {
	a, b := newFixture(t, "closed-a"), newFixture(t, "closed-b")
	connection := connect(t, a, b)
	a.service.Close()
	a.db.Close()
	a.secret.Close()
	a.service.options.DB = nil
	connection.LastSeen = nil // A cold connection would otherwise read its persisted credential.
	if session, err := a.service.ensureSession(context.Background(), connection); session != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatal("closed service accessed released stores", session, err)
	}
}

func TestSyncWaitingForConnectionPassHonorsCancellation(t *testing.T) {
	a, b := newFixture(t, "sync-a"), newFixture(t, "sync-b")
	c := connect(t, a, b)
	f, err := a.service.forwarder(c)
	if err != nil {
		t.Fatal(err)
	}
	f.syncGate <- struct{}{}
	defer func() { <-f.syncGate }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.service.Sync(ctx, c.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestForwarderRejectedColdDialReturnsErrorWithoutTypedNilTransport(t *testing.T) {
	a := newFixture(t, "cold-dial")
	err := a.service.callConnection(t.Context(), Connection{ID: "missing-connection"}, "identity", struct{}{}, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestPromotedPairSessionSurvivesPendingHandshakeExpiry(t *testing.T) {
	a, b := newFixture(t, "expiry-a"), newFixture(t, "expiry-b")
	c := connect(t, a, b)
	session := b.service.session(c.ID)
	if session == nil {
		t.Fatal("pair was not promoted")
	}
	// Invoke the actual expiry callback deterministically after promotion.
	b.service.expirePending(session)
	if _, err := session.Call(t.Context(), peer.Request{ID: "after-handshake-expiry", Method: "identity", Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatal("pending timer retired an authenticated session", err)
	}
	if b.service.session(c.ID) != session {
		t.Fatal("expiry replaced the promoted session")
	}
}

type metadataBarrierBackend struct {
	*testBackend
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (b *metadataBarrierBackend) Directory(ctx context.Context) (Directory, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
		return b.testBackend.Directory(ctx)
	case <-ctx.Done():
		return Directory{}, ctx.Err()
	}
}
func TestInitialForegroundMetadataCoalescesPeriodicSampling(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	a, b := newFixture(t, "metadata-a"), newFixture(t, "metadata-b")
	c := connect(t, a, b) // No Backend yet: the initial saved directory is empty.
	a.service.options.Backend = &testBackend{f: a}
	backend := &metadataBarrierBackend{testBackend: &testBackend{f: b}, entered: make(chan struct{}, 2), release: make(chan struct{})}
	b.service.options.Backend = backend
	var once sync.Once
	release := func() { once.Do(func() { close(backend.release) }) }
	defer release()
	first := make(chan error, 1)
	go func() { first <- a.service.refreshMetadata(ctx, c) }()
	select {
	case <-backend.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var workers sync.WaitGroup
	defer workers.Wait()
	defer release()
	if a.service.tryRefreshMetadata(ctx, c, &workers) {
		t.Error("periodic metadata invalidated an unfinished first foreground directory")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := a.service.refreshMetadata(cancelled, c); !errors.Is(err, context.Canceled) {
		t.Error("metadata wait ignored cancellation", err)
	}
	release()
	if err := <-first; err != nil {
		t.Fatal("initial metadata failed", err)
	}
	current, err := a.service.Get(ctx, c.ID)
	if err != nil || current.Directory == nil {
		t.Fatal("initial directory missing", current.Directory, err)
	}
	if !a.service.tryRefreshMetadata(ctx, c, &workers) {
		t.Fatal("periodic sampling did not resume")
	}
	workers.Wait()
}
