package spaces

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAttachmentChunksStayOnExplicitRemoteSpace(t *testing.T) {
	local, remote := newControl(t, "local"), newControl(t, "remote")
	var attachmentCalls atomic.Int64
	payload := json.RawMessage(`{"action":"chunk","id":"upload","offset":0,"data":"AQID"}`)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(strings.TrimPrefix(r.URL.Path, "/control/v1/owner"), "/api/v1/attachments") {
			if r.Header.Get("Authorization") != "Bearer private-test-credential" || r.Header.Get("X-Pot-Control-ID") != "remote" || r.URL.Query().Get("assistant_id") != "alice" {
				t.Error("remote authentication or owner scope lost")
			}
			attachmentCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"size":3,"offset":0,"revision":"upload","data":"AQID"}`))
			return
		}
		remote.ServeHTTP(w, r)
	}))
	defer server.Close()
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	manager, err := Open(context.Background(), Options{Path: ConfigurationPath(t.TempDir()), Local: local, LocalID: "local", RefreshInterval: time.Hour, RelayInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err = manager.Connect(context.Background(), ConnectRequest{Endpoint: server.URL, Token: "private-test-credential", CAPEM: ca, ControlID: "remote"}); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		method, path string
		body         json.RawMessage
	}{
		{"POST", "/api/v1/attachments?assistant_id=alice&space_id=local", payload},
		{"GET", "/api/v1/attachments/upload?assistant_id=alice&offset=0", nil},
	} {
		body, err := manager.Call(context.Background(), "remote", request.method, request.path, request.body)
		if err != nil || !strings.Contains(string(body), "AQID") {
			t.Fatal("remote byte chunk route", string(body), err)
		}
	}
	if attachmentCalls.Load() != 2 {
		t.Fatal(attachmentCalls.Load())
	}
	if err = manager.Disconnect("remote"); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Call(context.Background(), "remote", "POST", "/api/v1/attachments?assistant_id=alice", payload); err == nil {
		t.Fatal("disconnected remote silently used local Control")
	}
	if attachmentCalls.Load() != 2 {
		t.Fatal("unexpected retry on another route")
	}
}
