package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/secrets"
)

func TestNativeTerminalAuthenticatedSocketAndOriginBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.Registry.Create(ctx, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	a := &Application{Control: s, RuntimeID: "local", BootID: "boot"}
	server := httptest.NewServer(Handler(a, "private"))
	defer server.Close()
	created, err := a.Call(ctx, Request{Method: "POST", Path: "/api/v1/terminals", Body: json.RawMessage(`{"cols":80,"rows":24}`)})
	if err != nil {
		t.Fatal(err)
	}
	id := created.(map[string]any)["id"].(string)
	for _, header := range []http.Header{{}, {"Authorization": {"Bearer private"}, "Origin": {"https://example.test"}}, {"Authorization": {"Bearer private"}, "Origin": {""}}} {
		r, _ := http.NewRequest("GET", server.URL+"/api/v1/terminals", nil)
		r.Header = header
		reply, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		reply.Body.Close()
		if reply.StatusCode != 401 && reply.StatusCode != 403 {
			t.Fatal("origin or unauthenticated request admitted", reply.StatusCode)
		}
	}
	socket, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/v1/terminals/"+id+"/socket", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer private"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	if err = socket.Write(ctx, websocket.MessageBinary, []byte("printf 'actual-pty-\\165tf8-\\344\\270\\255\\346\\226\\207\\n'\n")); err != nil {
		t.Fatal(err)
	}
	output := ""
	for !strings.Contains(output, "actual-pty-utf8-中文") {
		kind, data, err := socket.Read(ctx)
		if err != nil {
			terminal, _ := s.Terminals.Get("main", id)
			t.Fatal(err, terminal.Info(), output)
		}
		if kind != websocket.MessageBinary {
			t.Fatal(kind)
		}
		output += string(data)
	}
	socket.CloseNow()
	if _, err = a.Call(ctx, Request{Method: "POST", Path: "/api/v1/terminals/" + id + "/close"}); err != nil {
		t.Fatal(err)
	}
}
