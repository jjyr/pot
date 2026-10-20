package agentlink

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
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
	"sync/atomic"
	"testing"
	"time"
)

type sqlMailbox struct {
	*assistant.Mailbox
	db *sql.DB
}

func (m sqlMailbox) Pending(ctx context.Context, local, peer protocol.AgentRef) ([]protocol.Envelope, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT envelope FROM agent_messages WHERE direction='out' AND state='queued' AND json_extract(envelope,'$.from.node_id')=? AND json_extract(envelope,'$.from.kind')=? AND json_extract(envelope,'$.from.agent_id')=? AND json_extract(envelope,'$.to.node_id')=? AND json_extract(envelope,'$.to.kind')=? AND json_extract(envelope,'$.to.agent_id')=? ORDER BY sequence LIMIT ?`, local.NodeID, local.Kind, local.AgentID, peer.NodeID, peer.Kind, peer.AgentID, protocol.MailPageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []protocol.Envelope{}
	for rows.Next() {
		var raw []byte
		var value protocol.Envelope
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
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
	mail, err := assistant.OpenMailbox(filepath.Join(f.root, "tasks.sqlite"), f.id)
	if err != nil {
		f.t.Fatal(err)
	}
	db, err := storage.OpenDatabase(filepath.Join(f.root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mail = sqlMailbox{mail, db}
	f.service, err = New(Options{DB: f.db, Secrets: f.secret, Mailbox: f.mail, HostIdentity: peer.NewIdentity(f.id, f.key.Public().(ed25519.PublicKey)), HostKey: f.key, Identity: func(_ context.Context, id string) (Identity, error) {
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
	f.service.closeSessions()
	f.mail.Mailbox.Close()
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
	inv, err := b.service.Invite(context.Background(), "assistant", b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	value, err := a.service.Connect(context.Background(), "assistant", inv.Address)
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
	if c.SchemaVersion != 3 || c.PeerHost != b.service.options.HostIdentity || c.State != "ready" || c.LastSeen == nil {
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
	inv, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	b.dropPair.Store(true)
	c, err := a.service.Connect(ctx, "assistant", inv.Address)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	a.restart()
	b.restart()
	recovered, err := a.service.Connect(ctx, "assistant", inv.Address)
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
func TestTCPInvitationClaimBindsSignedHostAndExactAssistantRoute(t *testing.T) {
	ctx := context.Background()
	a, b, c := newFixture(t, "control-a"), newFixture(t, "control-b"), newFixture(t, "control-c")
	inv, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	again, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	if inv.Address != again.Address {
		t.Fatal("unclaimed invitation was not reused")
	}
	parsed, _ := parseAddress(ctx, inv.Address)
	connection, err := a.service.Connect(ctx, "assistant", inv.Address)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.service.Connect(ctx, "assistant", inv.Address); !errors.Is(err, ErrConflict) {
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
	for _, change := range []func(*protocol.Envelope){func(e *protocol.Envelope) { e.From.AgentID = "other" }, func(e *protocol.Envelope) { e.To.AgentID = "other" }, func(e *protocol.Envelope) { e.From.Kind = "user" }, func(e *protocol.Envelope) { e.Kind = "plugin_event" }} {
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
	inv, _ := b.service.Invite(ctx, "other", b.endpoint)
	parsed, _ := parseAddress(ctx, inv.Address)
	parsed.Host = peer.NewIdentity(b.id, wrongKey.Public().(ed25519.PublicKey))
	raw, _ := json.Marshal(parsed)
	address := b.endpoint + "#" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err = a.service.Connect(ctx, "other", address); err == nil {
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
func TestTCPListenerRevocationAndPendingBacklogIsolation(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	other, _ := a.service.options.Identity(ctx, "other")
	for index := 0; index < protocol.MailPageSize+1; index++ {
		if _, err := a.mail.Send(ctx, fmt.Sprintf("other-%d", index), other.Address, protocol.SendMail{To: b.identity().Address, Text: "not bound"}, nil); err != nil {
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
	inv, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	parsed, _ := parseAddress(ctx, inv.Address)
	if _, err := b.db.Exec(`UPDATE agent_link_invitations SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), parsed.Invitation); err != nil {
		t.Fatal(err)
	}
	if _, err := a.service.Connect(ctx, "assistant", inv.Address); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	fresh, _ := b.service.Invite(ctx, "assistant", b.endpoint)
	if _, err := a.service.Connect(ctx, "assistant", fresh.Address); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"https://example.test", "http://127.0.0.1:8792", "tcp://127.0.0.1:9999/other", "tcp://user@127.0.0.1:9999", "tcp://127.0.0.1:9999?x=1"} {
		if _, err := b.service.Invite(ctx, "assistant", raw); err == nil {
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

func TestNodeIdentityUpgradeReopensOriginalSignedPairAndReceipts(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "upgrade-a"), newFixture(t, "upgrade-b")
	invitation, err := b.service.Invite(ctx, "assistant", b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := a.service.Connect(ctx, "assistant", invitation.Address)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := a.service.secret(ctx, connection.ID)
	if err != nil || credential.Pair == nil {
		t.Fatal(credential, err)
	}
	oldPair, _ := json.Marshal(credential.Pair)
	oldPair = []byte(strings.ReplaceAll(string(oldPair), `"node_id":`, `"control_id":`))
	oldHost, _ := json.Marshal(a.service.options.HostIdentity)
	oldHost = []byte(strings.ReplaceAll(string(oldHost), `"node_id":`, `"id":`))
	oldClaim := digest(append(append(append([]byte("["), oldPair...), ','), append(oldHost, ']')...))
	remoteConnection, err := b.service.Get(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	remoteConnection.Local.Name = "Renamed after invitation was copied"
	if err = b.service.update(ctx, remoteConnection); err != nil {
		t.Fatal(err)
	}
	endpointA, endpointB := a.endpoint, b.endpoint
	a.stop()
	b.stop()
	// Recreate the exact previous persisted format, including encrypted outgoing
	// pair intent. Every database and key belongs to this private test fixture.
	for _, fixture := range []*fixture{a, b} {
		links, err := fixture.service.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range links {
			raw, _ := json.Marshal(link)
			legacy := strings.ReplaceAll(string(raw), `"node_id":`, `"control_id":`)
			legacy = strings.Replace(legacy, `"peer_host":{"control_id":`, `"peer_host":{"id":`, 1)
			legacy = strings.Replace(legacy, `"schema_version":3`, `"schema_version":2`, 1)
			routeRaw, _ := json.Marshal([]protocol.AgentRef{link.Local.Address, link.Peer.Address})
			oldRoute := digest([]byte(strings.ReplaceAll(string(routeRaw), `"node_id":`, `"control_id":`)))
			if _, err = fixture.db.Exec("UPDATE agent_link_connections SET metadata=?,route=? WHERE id=?", legacy, oldRoute, link.ID); err != nil {
				t.Fatal(err)
			}
			ref := "agent-link/connection/" + link.ID
			secret, err := fixture.secret.Get(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			if err = fixture.secret.Delete(ctx, ref); err != nil {
				t.Fatal(err)
			}
			if err = fixture.secret.Put(ctx, ref, []byte(strings.ReplaceAll(string(secret), `"node_id":`, `"control_id":`))); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = fixture.db.Exec(`UPDATE agent_link_invitations SET identity=replace(identity,'"node_id":','"control_id":');
ALTER TABLE codex_agent_host RENAME COLUMN node_id TO control_id;
DROP TABLE node_identity_secret_migration;
DROP TABLE agent_link_claim_digest_formats;
DROP TABLE display_resources;
DROP TABLE provider_metadata;
DROP TABLE extension_settings;
DROP TABLE mcp_servers;
ALTER TABLE assistant_definitions DROP COLUMN deleted;
ALTER TABLE assistant_profiles DROP COLUMN character;
DELETE FROM pot_migrations WHERE id >= '0009_';`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = b.db.Exec("UPDATE agent_link_invitations SET claim_hash=? WHERE id=?", oldClaim, credential.Pair.Invitation); err != nil {
		t.Fatal(err)
	}
	a.close()
	b.close()
	a.open()
	b.open()
	a.start(strings.TrimPrefix(endpointA, "tcp://"))
	b.start(strings.TrimPrefix(endpointB, "tcp://"))
	if err = a.service.Sync(ctx, connection.ID); err != nil {
		t.Fatal("original pair failed after upgrade", err)
	}
	current, err := a.service.Get(ctx, connection.ID)
	if err != nil || current.ID != connection.ID || current.PeerHost != connection.PeerHost || current.State != "ready" || current.SchemaVersion != 3 {
		t.Fatal(current, err)
	}
	sender := current.Local.Address
	message, err := a.mail.Send(ctx, "migration-receipt", sender, protocol.SendMail{To: current.Peer.Address, Text: "original route after upgrade"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.service.Sync(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	records, err := b.mail.History(ctx, "assistant", 0)
	if err != nil || len(records) != 1 || records[0].Message != message {
		t.Fatal(records, err)
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
	invite, err := b.service.Invite(ctx, "assistant", b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := a.service.Connect(ctx, "assistant", invite.Address)
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
