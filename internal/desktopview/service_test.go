package desktopview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/pkg/sandbox"
)

type recordedStream struct {
	mu   sync.Mutex
	data []byte
	peer net.Conn
}

func (r *recordedStream) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte{}, r.data...)
}

type displayDouble struct {
	mu      sync.Mutex
	streams []*recordedStream
	valid   atomic.Bool
}

func (m *displayDouble) PrepareDesktop(context.Context, string) (sandbox.DesktopBinding, error) {
	return sandbox.DesktopBinding{SandboxID: "shared", ManagerEpoch: 1, Generation: 1, Capabilities: sandbox.DesktopCapabilities{Viewing: true, Input: true, Clipboard: true, Browser: true, Protocol: "rfb-3.8"}}, nil
}
func (m *displayDouble) ValidateDesktop(context.Context, sandbox.DesktopBinding) error {
	if !m.valid.Load() {
		return errors.New("generation changed")
	}
	return nil
}
func (m *displayDouble) OpenDesktop(context.Context, sandbox.DesktopBinding) (io.ReadWriteCloser, error) {
	a, b := net.Pipe()
	record := &recordedStream{peer: b}
	m.mu.Lock()
	m.streams = append(m.streams, record)
	m.mu.Unlock()
	go func() {
		defer b.Close()
		buf := make([]byte, 1024)
		for {
			n, err := b.Read(buf)
			if n > 0 {
				record.mu.Lock()
				record.data = append(record.data, buf[:n]...)
				record.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return a, nil
}
func (m *displayDouble) DesktopMetrics(context.Context, sandbox.DesktopBinding) (json.RawMessage, error) {
	return json.RawMessage(`{"os":"linux","cpu_percent":2,"token":"must-not-leak","viewer":"http://guest","image":"test"}`), nil
}
func (m *displayDouble) DesktopBrowser(context.Context, sandbox.DesktopBinding) error { return nil }
func setupGateway(t *testing.T) (*Service, *httptest.Server, *displayDouble, *atomic.Bool) {
	t.Helper()
	manager := &displayDouble{}
	manager.valid.Store(true)
	login := &atomic.Bool{}
	login.Store(true)
	service, err := New(Options{Manager: manager, Authorize: func(r *http.Request) (Identity, error) {
		owner := r.Header.Get("X-Test-Login")
		if owner == "" {
			return Identity{}, errors.New("login")
		}
		return Identity{Owner: owner, Valid: login.Load}, nil
	}, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") != "https://evil.invalid" }, Assets: fstest.MapFS{"desktop/index.html": {Data: []byte("viewer")}, "desktop-assets/core/rfb.js": {Data: []byte("module")}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service)
	t.Cleanup(func() { service.Close(); server.Close() })
	return service, server, manager, login
}
func request(t *testing.T, server *httptest.Server, owner, method, path, text string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Login", owner)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}
func ticket(t *testing.T, server *httptest.Server, owner string) string {
	t.Helper()
	status, body := request(t, server, owner, "POST", "/api/v1/desktop/sessions", `{"sandbox_id":"shared"}`)
	if status != 200 {
		t.Fatalf("open %d %s", status, body)
	}
	var value struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(body, &value); err != nil || value.Session.ID == "" {
		t.Fatal(string(body), err)
	}
	return value.Session.ID
}
func connectClient(t *testing.T, server *httptest.Server, owner, id string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/desktop/"+id+"/rfb", &websocket.DialOptions{HTTPHeader: http.Header{"X-Test-Login": {owner}}, Subprotocols: []string{"binary"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}
func send(t *testing.T, c *websocket.Conn, data []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, data); err != nil {
		t.Fatal(err)
	}
}
func waitBytes(t *testing.T, r *recordedStream, n int) []byte {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		data := r.snapshot()
		if len(data) >= n {
			return data
		}
		if time.Now().After(deadline) {
			t.Fatalf("received %d < %d", len(data), n)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestTicketsRequireLoginAndBindOwnerAndRoutes(t *testing.T) {
	_, server, _, _ := setupGateway(t)
	if status, _ := request(t, server, "", "POST", "/api/v1/desktop/sessions", `{"sandbox_id":"shared"}`); status != 401 {
		t.Fatal(status)
	}
	id := ticket(t, server, "first")
	for _, path := range []string{"/desktop/" + id + "/", "/desktop/" + id + "/api/status", "/desktop/" + id + "/rfb"} {
		if status, _ := request(t, server, "second", "GET", path, ""); status != 403 {
			t.Fatalf("cross-login %s %d", path, status)
		}
	}
	for _, path := range []string{"/desktop/" + id + "/api/driver", "/desktop/" + id + "/api/execute", "/desktop-assets/../secret", "/desktop-assets/core/secret.json"} {
		if status, _ := request(t, server, "first", "GET", path, ""); status == 200 {
			t.Fatalf("unexpected route %s", path)
		}
	}
	status, data := request(t, server, "first", "GET", "/desktop/"+id+"/api/metrics", "")
	if status != 200 || !bytes.Contains(data, []byte(`"os":"linux"`)) || bytes.Contains(data, []byte("must-not-leak")) || bytes.Contains(data, []byte("guest")) {
		t.Fatalf("metrics %d %s", status, data)
	}
	req, _ := http.NewRequest("POST", server.URL+"/desktop/"+id+"/api/control", strings.NewReader(`{"action":"take"}`))
	req.Header.Set("X-Test-Login", "first")
	req.Header.Set("Origin", "https://evil.invalid")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
}
func TestServerGatesInputAndReleasesBeforeControlTransfer(t *testing.T) {
	service, server, manager, _ := setupGateway(t)
	a, b := ticket(t, server, "first"), ticket(t, server, "second")
	ca, cb := connectClient(t, server, "first", a), connectClient(t, server, "second", b)
	manager.mu.Lock()
	ra, rb := manager.streams[0], manager.streams[1]
	manager.mu.Unlock()
	handshake := []byte("RFB 003.008\n\x01\x01")
	send(t, ca, handshake)
	send(t, cb, handshake)
	waitBytes(t, ra, 14)
	waitBytes(t, rb, 14)
	key := []byte{4, 1, 0, 0, 0, 0, 0, 65}
	send(t, ca, append(append([]byte{}, key...), []byte{3, 0, 0, 0, 0, 0, 0, 1, 0, 1}...))
	if data := waitBytes(t, ra, 24); len(data) != 24 {
		t.Fatalf("view-only key forwarded: %v", data)
	}
	status, _ := request(t, server, "first", "POST", "/desktop/"+a+"/api/control", `{"action":"take"}`)
	if status != 200 {
		t.Fatal(status)
	}
	send(t, ca, key)
	waitBytes(t, ra, 32)
	if status, _ := request(t, server, "second", "POST", "/desktop/"+b+"/api/control", `{"action":"take"}`); status != 409 {
		t.Fatal(status)
	}
	if status, _ := request(t, server, "first", "POST", "/desktop/"+a+"/api/control", `{"action":"return"}`); status != 200 {
		t.Fatal(status)
	}
	data := waitBytes(t, ra, 40)
	if !bytes.Equal(data[32:40], []byte{4, 0, 0, 0, 0, 0, 0, 65}) {
		t.Fatalf("key was not released: %v", data)
	}
	if status, _ := request(t, server, "second", "POST", "/desktop/"+b+"/api/control", `{"action":"take"}`); status != 200 {
		t.Fatal(status)
	}
	send(t, cb, key)
	waitBytes(t, rb, 22)
	service.RevokeOwner("first")
	if status, _ := request(t, server, "first", "GET", "/desktop/"+a+"/api/status", ""); status != 403 {
		t.Fatal(status)
	}
	if status, _ := request(t, server, "second", "GET", "/desktop/"+b+"/api/status", ""); status != 200 {
		t.Fatal(status)
	}
}
func TestRevocationAndVMGenerationChangeCloseActiveStreams(t *testing.T) {
	_, server, manager, login := setupGateway(t)
	id := ticket(t, server, "first")
	conn := connectClient(t, server, "first", id)
	manager.valid.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("generation change left stream open")
	}
	manager.valid.Store(true)
	id = ticket(t, server, "first")
	conn = connectClient(t, server, "first", id)
	login.Store(false)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel2()
	if _, _, err := conn.Read(ctx2); err == nil {
		t.Fatal("login revocation left stream open")
	}
}
func TestRFBGateRejectsPartialAuthorityChangesAndUnknownExtensions(t *testing.T) {
	gate := rfbGate{}
	if out, err := gate.push([]byte("RFB 003.008\n\x01\x01"), false, false); err != nil || len(out) != 14 {
		t.Fatal(err)
	}
	if _, err := gate.push([]byte{4, 1, 0}, true, true); err != nil {
		t.Fatal(err)
	}
	if gate.changed() == nil {
		t.Fatal("partial input crossed authority epoch")
	}
	if out, err := gate.push([]byte{0, 0, 0, 0, 65}, false, false); err != nil || len(out) != 0 {
		t.Fatal(out, err)
	}
	if _, err := gate.push([]byte{254}, true, true); err == nil {
		t.Fatal("unknown extension accepted")
	}
	gate = rfbGate{}
	if _, err := gate.push([]byte("RFB 003.003\n"), false, false); err == nil {
		t.Fatal("non3.8 handshake accepted")
	}
}
