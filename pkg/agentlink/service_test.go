package agentlink

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type sqlMailbox struct {
	*assistant.Mailbox
	db *sql.DB
}

func (m sqlMailbox) Pending(ctx context.Context, local, peer protocol.AgentRef) ([]protocol.Envelope, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT envelope FROM agent_messages WHERE direction='out' AND state='queued' AND json_extract(envelope,'$.from.control_id')=? AND json_extract(envelope,'$.from.agent_id')=? AND json_extract(envelope,'$.to.control_id')=? AND json_extract(envelope,'$.to.agent_id')=? ORDER BY sequence LIMIT ?`, local.ControlID, local.AgentID, peer.ControlID, peer.AgentID, protocol.MailPageSize)
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
	t            *testing.T
	root, id     string
	db           *sql.DB
	secret       *secrets.Store
	keys         *secrets.MemoryKey
	mail         sqlMailbox
	service      *Service
	server       *httptest.Server
	dropPair     atomic.Bool
	dropExchange atomic.Bool
}

func newFixture(t *testing.T, id string) *fixture {
	t.Helper()
	key, _ := secrets.NewMemoryKey(make([]byte, 32))
	f := &fixture{t: t, root: t.TempDir(), id: id, keys: key}
	f.open()
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == Prefix+"/pair" && f.dropPair.Swap(false) || r.URL.Path == Prefix+"/exchange" && f.dropExchange.Swap(false) {
			rec := httptest.NewRecorder()
			f.service.Handler().ServeHTTP(rec, r)
			if rec.Code != 200 {
				t.Errorf("expected durable acceptance before dropped response, got %d %s", rec.Code, rec.Body.String())
			}
			http.Error(w, "response lost", 503)
			return
		}
		f.service.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(func() { f.server.Close(); f.close() })
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
	m, err := assistant.OpenMailbox(filepath.Join(f.root, "tasks.sqlite"), f.id)
	if err != nil {
		f.t.Fatal(err)
	}
	db, err := storage.OpenDatabase(filepath.Join(f.root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		f.t.Fatal(err)
	}
	f.mail = sqlMailbox{m, db}
	f.service, err = New(Options{DB: f.db, Secrets: f.secret, Mailbox: f.mail, Identity: func(_ context.Context, id string) (Identity, error) {
		if id != "assistant" && id != "other" {
			return Identity{}, ErrNotFound
		}
		return Identity{Address: protocol.AgentRef{ControlID: f.id, Kind: "assistant", AgentID: id}, Name: f.id + " " + id, Adapter: "pot"}, nil
	}})
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) close()   { f.mail.Mailbox.Close(); f.mail.db.Close(); f.secret.Close(); f.db.Close() }
func (f *fixture) restart() { f.close(); f.open() }
func (f *fixture) identity() Identity {
	value, _ := f.service.options.Identity(context.Background(), "assistant")
	return value
}
func connect(t *testing.T, a, b *fixture) Connection {
	t.Helper()
	inv, err := b.service.Invite(context.Background(), "assistant", b.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := a.service.Connect(context.Background(), "assistant", inv.Address)
	if err != nil {
		t.Fatal(err)
	}
	return connection
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

func TestOneSidedConnectionDeliversBothDirectionsAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	if connection.State != "ready" || connection.LastSeen == nil {
		t.Fatal(connection)
	}
	remote, err := b.service.Get(ctx, connection.ID)
	if err != nil || remote.Endpoint != "" || remote.State != "ready" {
		t.Fatal(remote, err)
	}
	first := send(t, a, b, "message-a", "from a")
	send(t, b, a, "message-b", "from b")
	if err = a.service.Sync(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	if mailCount(t, a, "message-b") != 1 || mailCount(t, b, first.ID) != 1 {
		t.Fatal("bidirectional message missing")
	}
	for _, f := range []*fixture{a, b} {
		pending, err := f.mail.Pending(ctx, f.identity().Address, map[bool]protocol.AgentRef{true: b.identity().Address, false: a.identity().Address}[f == a])
		if err != nil || len(pending) != 0 {
			t.Fatal(pending, err)
		}
	}
	a.restart()
	b.restart()
	send(t, b, a, "after-restart", "survives without reverse pairing")
	if err = a.service.Sync(ctx, connection.ID); err != nil || mailCount(t, a, "after-restart") != 1 {
		t.Fatal(err)
	}
	values, _ := a.service.List(ctx)
	if len(values) != 1 || values[0].ID != connection.ID {
		t.Fatal(values)
	}
}

func TestLostPairAndDeliveryResponsesRecoverImmutableOriginal(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	inv, err := b.service.Invite(ctx, "assistant", b.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	b.dropPair.Store(true)
	connection, err := a.service.Connect(ctx, "assistant", inv.Address)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	a.restart()
	b.restart()
	recovered, err := a.service.Connect(ctx, "assistant", inv.Address)
	if err != nil || recovered.ID != connection.ID {
		t.Fatal(recovered, err)
	}
	send(t, a, b, "durable-original", "accepted before response was lost")
	b.dropExchange.Store(true)
	if err = a.service.Sync(ctx, connection.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if mailCount(t, b, "durable-original") != 1 {
		t.Fatal("receiver did not commit")
	}
	a.restart()
	b.restart()
	if err = a.service.Sync(ctx, connection.ID); err != nil || mailCount(t, b, "durable-original") != 1 {
		t.Fatal(err)
	}
	for _, f := range []*fixture{a, b} {
		values, _ := f.service.List(ctx)
		if len(values) != 1 {
			t.Fatal(values)
		}
	}
}

func TestInvitationsAreSingleUseRouteBoundAndEncrypted(t *testing.T) {
	ctx := context.Background()
	a, b, c := newFixture(t, "control-a"), newFixture(t, "control-b"), newFixture(t, "control-c")
	inv, err := b.service.Invite(ctx, "assistant", b.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := b.service.Invite(ctx, "assistant", b.server.URL)
	if same.Address != inv.Address {
		t.Fatal("unclaimed invitation should be reused")
	}
	parsed, _ := parseAddress(inv.Address)
	if _, err = a.service.Connect(ctx, "assistant", inv.Address); err != nil {
		t.Fatal(err)
	}
	if _, err = c.service.Connect(ctx, "assistant", inv.Address); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	// Neither invitation tokens nor shared connection credentials reach SQLite pages.
	for _, f := range []*fixture{a, b} {
		if _, err = f.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(f.root, "control.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), parsed.Token) {
			t.Fatal("plaintext invitation leaked")
		}
		values, _ := f.service.List(ctx)
		for _, value := range values {
			credential, err := f.service.secret(ctx, value.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), credential.Token) {
				t.Fatal("plaintext credential leaked")
			}
		}
	}
	// A token cannot select another recipient, even within the same Control.
	newInv, _ := b.service.Invite(ctx, "assistant", b.server.URL)
	bad, _ := parseAddress(newInv.Address)
	bad.Identity.Address.AgentID = "other"
	raw, _ := json.Marshal(bad)
	newInv.Address = b.server.URL + Prefix + "/connect#" + base64URL(raw)
	if _, err = c.service.Connect(ctx, "assistant", newInv.Address); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func base64URL(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func TestMessageAndReceiptProvenanceCannotCrossBindings(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	credential, _ := a.service.secret(ctx, connection.ID)
	message := send(t, a, b, "valid-message", "peer data")
	for _, change := range []func(*protocol.Envelope){func(m *protocol.Envelope) { m.To.AgentID = "other" }, func(m *protocol.Envelope) { m.From.Kind = "user" }, func(m *protocol.Envelope) { m.Kind = "plugin_event" }, func(m *protocol.Envelope) { m.From.AgentID = "other" }} {
		forged := message
		change(&forged)
		var response exchangeResponse
		err := a.service.post(ctx, b.server.URL, "/exchange", credential.Token, exchangeRequest{ID: connection.ID, Messages: []protocol.Envelope{forged}}, &response)
		if !errors.Is(err, ErrConflict) || mailCount(t, b, message.ID) != 0 {
			t.Fatal(err)
		}
	}
	forged := message
	forged.Text = "changed receipt"
	var response exchangeResponse
	if err := b.service.post(ctx, b.server.URL, "/exchange", credential.Token, exchangeRequest{ID: connection.ID, Receipts: []protocol.Receipt{{Message: forged}}}, &response); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if err := a.service.Sync(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRevokeFencesDeliveryAndPropagatesAfterRestart(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	// Simulate unavailable transport only for revocation, retaining the real endpoint.
	a.service.client.Transport = roundTripError{}
	if err := a.service.Revoke(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	value, _ := a.service.Get(ctx, connection.ID)
	if value.State != "revoked" || !value.RevocationPending {
		t.Fatal(value)
	}
	if err := a.service.Sync(ctx, connection.ID); !errors.Is(err, ErrRevoked) {
		t.Fatal(err)
	}
	a.restart()
	b.restart()
	value, _ = a.service.Get(ctx, connection.ID)
	if err := a.service.propagateRevoke(ctx, value); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*fixture{a, b} {
		value, _ := f.service.Get(ctx, connection.ID)
		if value.State != "revoked" || value.RevocationPending {
			t.Fatal(value)
		}
		if _, err := f.service.secret(ctx, connection.ID); err == nil {
			t.Fatal("revoked credential retained")
		}
	}
	// A new invitation can establish a new relationship, without erasing tombstones.
	newConnection := connect(t, a, b)
	if newConnection.ID == connection.ID {
		t.Fatal("revoked identity reused")
	}
}

type roundTripError struct{}

func (roundTripError) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline")
}

func TestListenerSideRevocationStopsDialer(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	connection := connect(t, a, b)
	if err := b.service.Revoke(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.service.Sync(ctx, connection.ID); !errors.Is(err, ErrRevoked) {
		t.Fatal(err)
	}
	value, _ := a.service.Get(ctx, connection.ID)
	if value.State != "revoked" {
		t.Fatal(value)
	}
}

func TestInvitationExpirationAndPublicBoundary(t *testing.T) {
	ctx := context.Background()
	a, b := newFixture(t, "control-a"), newFixture(t, "control-b")
	inv, _ := b.service.Invite(ctx, "assistant", b.server.URL)
	value, _ := parseAddress(inv.Address)
	if _, err := b.db.Exec(`UPDATE agent_link_invitations SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), value.Invitation); err != nil {
		t.Fatal(err)
	}
	if _, err := a.service.Connect(ctx, "assistant", inv.Address); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://remote.example", "https://example.com/path", "https://user:pass@example.com", "https://example.com?token=oops"} {
		if _, err := b.service.Invite(ctx, "assistant", endpoint); !errors.Is(err, ErrInvalid) {
			t.Fatal(endpoint, err)
		}
	}
	for _, path := range []string{"/api/v1/assistant-mail", "/api/v1/machines", Prefix + "/pair"} {
		response, err := http.Get(b.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == 200 {
			t.Fatal("unexpected public capability", path)
		}
	}
}
