package spaces

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/protocol"
)

type testControl struct {
	mu             sync.Mutex
	id, role       string
	mail           *assistant.Mailbox
	offline        atomic.Bool
	mutations      atomic.Int32
	runtimeCalls   atomic.Int32
	localCalls     atomic.Int32
	block, entered chan struct{}
}

func newControl(t *testing.T, id string) *testControl {
	t.Helper()
	mail, err := assistant.OpenMailbox(filepath.Join(t.TempDir(), "mail.sqlite"), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mail.Close() })
	return &testControl{id: id, role: "control", mail: mail}
}
func (c *testControl) Call(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
	c.localCalls.Add(1)
	if method == "POST" && (path == "/api/v1/messages" || path == "/api/v1/delayed") {
		c.mutations.Add(1)
	}
	if path == "/api/v1/runtime" {
		c.runtimeCalls.Add(1)
	}
	if c.offline.Load() {
		return nil, errors.New("offline")
	}
	c.mu.Lock()
	id, role := c.id, c.role
	c.mu.Unlock()
	var result any
	switch path {
	case "/api/v1/runtime":
		result = map[string]any{"runtime_id": id, "boot_id": "boot-" + id}
	case "/api/v1/control":
		result = map[string]any{"id": id, "runtime_id": id, "name": "Remote", "role": role}
	case "/api/v1/assistants":
		result = []any{map[string]any{"id": "main", "name": "Main"}}
	case "/api/v1/delayed":
		if c.entered != nil {
			close(c.entered)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.block:
			result = map[string]bool{"done": true}
		}
	case protocol.AssistantMailAPI:
		var request protocol.RelayRequest
		if err := protocol.DecodeStrict(body, &request); err != nil {
			return nil, err
		}
		switch request.Operation {
		case "directory":
			result = protocol.Directory{ControlID: id, Assistants: []protocol.MailAssistant{{Address: protocol.AgentRef{ControlID: id, Kind: "assistant", AgentID: "main"}, Name: "Main"}}}
		case "register":
			if err := c.mail.Register(ctx, *request.Directory); err != nil {
				return nil, err
			}
			result = map[string]bool{"ok": true}
		case "pending":
			messages, err := c.mail.Pending(ctx, request.TargetControlID)
			if err != nil {
				return nil, err
			}
			result = messages
		case "deliver":
			receipt, err := c.mail.Deliver(ctx, *request.Message)
			if err != nil {
				return nil, err
			}
			result = receipt
		case "acknowledge":
			if err := c.mail.Acknowledge(ctx, *request.Receipt); err != nil {
				return nil, err
			}
			result = map[string]bool{"ok": true}
		}
	default:
		return nil, errors.New("test route unavailable")
	}
	return json.Marshal(result)
}
func (c *testControl) Stream(ctx context.Context, _ string, _ func(Event) error) error {
	<-ctx.Done()
	return ctx.Err()
}
func (c *testControl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/control/v1/owner")
	if r.Header.Get("Authorization") != "Bearer private-test-credential" {
		http.Error(w, "unauthorized", 401)
		return
	}
	c.mu.Lock()
	id := c.id
	c.mu.Unlock()
	if expected := r.Header.Get("X-Pot-Control-ID"); expected != "" && expected != id {
		w.WriteHeader(409)
		json.NewEncoder(w).Encode(protocol.APIError{Code: "identity_changed", Message: "Control identity changed"})
		return
	}
	if r.URL.Path == "/api/v1/changes" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return
	}
	body, _ := io.ReadAll(r.Body)
	value, err := c.Call(r.Context(), r.Method, r.URL.RequestURI(), body)
	if err != nil {
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(protocol.APIError{Code: "request_failed", Message: err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(value)
}
func openManager(t *testing.T, local *testControl) *Manager {
	t.Helper()
	m, err := Open(context.Background(), Options{Path: ConfigurationPath(t.TempDir()), Local: local, LocalID: "local", RefreshInterval: time.Hour, RelayInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}
func TestAppSpacesExplicitTLSAndBidirectionalRelay(t *testing.T) {
	local, remote := newControl(t, "local"), newControl(t, "remote")
	server := httptest.NewTLSServer(remote)
	t.Cleanup(server.Close)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	m := openManager(t, local)
	if err := m.Connect(context.Background(), ConnectRequest{Endpoint: server.URL, Token: "private-test-credential", CAPEM: ca, ControlID: "remote"}); err != nil {
		t.Fatal(err)
	}
	catalog := m.Snapshot()
	if len(catalog.Items) != 2 || catalog.Items[1].ID != "remote" || !catalog.Items[1].Connected {
		t.Fatalf("catalog: %+v", catalog)
	}
	before := local.runtimeCalls.Load()
	if err := m.Select("remote"); err != nil {
		t.Fatal(err)
	}
	body, err := m.Call(context.Background(), "local", "GET", "/api/v1/runtime?space_id=remote", nil)
	if err != nil || !strings.Contains(string(body), `"local"`) {
		t.Fatalf("explicit local failed: %s %v", body, err)
	}
	if local.runtimeCalls.Load() != before+1 {
		t.Fatal("selection changed request destination")
	}
	for _, pair := range [][2]*testControl{{local, remote}, {remote, local}} {
		_, err := pair[0].mail.Send(context.Background(), pair[0].mail.ControlID()+"-message", protocol.AgentRef{ControlID: pair[0].mail.ControlID(), Kind: "assistant", AgentID: "main"}, protocol.SendMail{To: protocol.AgentRef{ControlID: pair[1].mail.ControlID(), Kind: "assistant", AgentID: "main"}, Text: "peer work"}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Exchange(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, node := range []*testControl{local, remote} {
		next, err := node.mail.Next(context.Background(), "main")
		if err != nil || next == nil {
			t.Fatalf("peer delivery: %v %v", next, err)
		}
	}
	persisted, err := os.ReadFile(m.options.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(persisted), "remote") {
		t.Fatal("Control identity not persisted")
	}
	projection, _ := json.Marshal(m.Snapshot())
	if strings.Contains(string(projection), "private-test-credential") {
		t.Fatal("credential leaked to UI projection")
	}
}
func TestIdentityChangeAndOfflineCachesNeverFallback(t *testing.T) {
	local, remote := newControl(t, "local"), newControl(t, "remote")
	server := httptest.NewServer(remote)
	t.Cleanup(server.Close)
	m := openManager(t, local)
	if err := m.Connect(context.Background(), ConnectRequest{Endpoint: server.URL, Token: "private-test-credential"}); err != nil {
		t.Fatal(err)
	}
	remote.offline.Store(true)
	m.Refresh(context.Background())
	snapshot := m.Snapshot()
	if !snapshot.Items[0].Connected || snapshot.Items[1].Connected || len(snapshot.Items[1].Assistants) == 0 {
		t.Fatalf("independent cached state: %+v", snapshot)
	}
	before := local.mutations.Load()
	if _, err := m.Call(context.Background(), "remote", "POST", "/api/v1/messages", json.RawMessage(`{}`)); err == nil {
		t.Fatal("offline call should fail")
	}
	if local.mutations.Load() != before {
		t.Fatal("failed remote operation fell back to local")
	}
	remote.offline.Store(false)
	remote.mu.Lock()
	remote.id = "replacement"
	remote.mu.Unlock()
	m.Refresh(context.Background())
	snapshot = m.Snapshot()
	if snapshot.Items[1].ID != "remote" || snapshot.Items[1].Connected {
		t.Fatal("identity change adopted a new owner")
	}
	if _, err := m.Call(context.Background(), "remote", "POST", "/api/v1/messages", json.RawMessage(`{}`)); err == nil {
		t.Fatal("identity header did not reject replaced Control")
	}
}
func TestDisconnectFencesInflightAndPersists(t *testing.T) {
	local, remote := newControl(t, "local"), newControl(t, "remote")
	remote.block = make(chan struct{})
	remote.entered = make(chan struct{})
	server := httptest.NewServer(remote)
	t.Cleanup(server.Close)
	m := openManager(t, local)
	if err := m.Connect(context.Background(), ConnectRequest{Endpoint: server.URL, Token: "private-test-credential"}); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := m.Call(context.Background(), "remote", "POST", "/api/v1/delayed", json.RawMessage(`{}`))
		result <- err
	}()
	<-remote.entered
	if err := m.Disconnect("remote"); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrScopeChanged) {
		t.Fatalf("late operation not fenced: %v", err)
	}
	if _, err := m.Call(context.Background(), "remote", "GET", "/api/v1/runtime", nil); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("disconnected call: %v", err)
	}
	saved, err := readConfig(m.options.Path)
	if err != nil || saved.Enabled {
		t.Fatalf("disconnect not durable: %+v %v", saved, err)
	}
	if err := m.Remove("remote"); err != nil {
		t.Fatal(err)
	}
	if len(m.Snapshot().Items) != 1 {
		t.Fatal("removed owner retained")
	}
}
func TestRemoteHandshakeRejectsEmbeddedAndRedirect(t *testing.T) {
	local, remote := newControl(t, "local"), newControl(t, "remote")
	remote.role = "embedded"
	server := httptest.NewServer(remote)
	t.Cleanup(server.Close)
	m := openManager(t, local)
	if err := m.Connect(context.Background(), ConnectRequest{Endpoint: server.URL, Token: "private-test-credential"}); err == nil {
		t.Fatal("embedded endpoint accepted")
	}
	reached := atomic.Bool{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	client, err := NewHTTPClient(redirect.URL, "private-test-credential", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Call(context.Background(), "GET", "/api/v1/control", nil); err == nil {
		t.Fatal("redirect accepted")
	}
	if reached.Load() {
		t.Fatal("credential followed redirect")
	}
	for _, endpoint := range []string{"https://user:secret@example.com", "https://example.com/path", "https://example.com?query=1"} {
		if _, err := NewHTTPClient(endpoint, "token", "", ""); err == nil {
			t.Fatalf("invalid origin accepted: %s", endpoint)
		}
	}
}
func TestConnectedSpaceDoesNotPollCatalogues(t *testing.T) {
	local := newControl(t, "local")
	m, err := Open(context.Background(), Options{Path: ConfigurationPath(t.TempDir()), Local: local, LocalID: "local", RefreshInterval: 10 * time.Millisecond, RelayInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	before := local.localCalls.Load()
	time.Sleep(60 * time.Millisecond)
	if local.localCalls.Load() != before {
		t.Fatal("connected Space was polled without an event or explicit refresh")
	}
	first, versions, changed := changedEvent(Event{Data: json.RawMessage(`{"assistants":1,"usage":1}`)}, nil)
	if !changed || !relayDirty(first) {
		t.Fatal("initial assistant event lost")
	}
	usage, versions, changed := changedEvent(Event{Data: json.RawMessage(`{"assistants":1,"usage":2}`)}, versions)
	if !changed || relayDirty(usage) || catalogDirty(usage) {
		t.Fatal("unchanged assistant version caused a catalogue/mail refresh")
	}
	_, _, changed = changedEvent(Event{Data: json.RawMessage(`{"assistants":1,"usage":2}`)}, versions)
	if changed {
		t.Fatal("unchanged stream version caused work")
	}
}

type reconnectControl struct {
	*testControl
	connections atomic.Int32
}

func (c *reconnectControl) Stream(ctx context.Context, _ string, deliver func(Event) error) error {
	attempt := c.connections.Add(1)
	frame := Event{Type: "changed", Data: json.RawMessage(`{"usage":1}`)}
	if err := deliver(frame); err != nil {
		return err
	}
	if err := deliver(frame); err != nil {
		return err
	}
	if attempt == 1 {
		return errors.New("test disconnect")
	}
	<-ctx.Done()
	return ctx.Err()
}
func TestStreamResetPrecedesEachConnectionBaseline(t *testing.T) {
	local := &reconnectControl{testControl: newControl(t, "local")}
	events := make(chan string, 10)
	manager, err := Open(context.Background(), Options{Path: ConfigurationPath(t.TempDir()), Local: local, LocalID: "local",
		OnStreamReset: func(space string) {
			if space != "local" {
				t.Errorf("reset for wrong Space: %s", space)
			}
			events <- "reset"
		},
		OnChange: func(space string, _ json.RawMessage) { events <- "change" }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for _, expected := range []string{"reset", "change", "reset", "change"} {
		select {
		case got := <-events:
			if got != expected {
				t.Fatalf("got %s, wanted %s", got, expected)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing reconnect baseline")
		}
	}
	select {
	case got := <-events:
		t.Fatalf("duplicate frame produced %s", got)
	default:
	}
}
