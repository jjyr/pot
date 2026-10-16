package transport

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/control"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/secrets"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSocketProductCreateEventsAuthAndRestart(t *testing.T) {
	ctx := context.Background()
	temporary, err := os.MkdirTemp("/tmp", "pot-api-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(temporary)
	root := filepath.Join(temporary, "go")
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	options := product.Options{Root: root, Control: control.Options{Keys: keys}}
	runtime, err := product.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	app := &Application{Control: runtime.Control, RuntimeID: runtime.Root.Identity.RuntimeID, BootID: runtime.BootID, Name: "Pot"}
	s, err := StartSocket(ctx, root, app)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	endpoint, err := ReadEndpoint(root)
	if err != nil {
		t.Fatal(err)
	}
	client := endpoint.Client()
	call := func(method, path, body, token string) (int, []byte) {
		t.Helper()
		r, _ := http.NewRequest(method, "http://runtime"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		response, err := client.Do(r)
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
	if code, _ := call("GET", "/api/v1/runtime", "", "wrong"); code != 401 {
		t.Fatal(code)
	}
	if code, data := call("GET", "/api/v1/assistants", "", endpoint.Token); code != 200 || strings.TrimSpace(string(data)) != "[]" {
		t.Fatal(code, string(data))
	}
	body := `{"request_id":"create","name":"First","text":"Initial rules"}`
	code, data := call("POST", "/api/v1/assistants", body, endpoint.Token)
	if code != 200 {
		t.Fatal(code, string(data))
	}
	var definition map[string]any
	if err = json.Unmarshal(data, &definition); err != nil {
		t.Fatal(err)
	}
	c, err := runtime.Control.Conversation(ctx, definition["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := c.Instructions(ctx)
	if err != nil || rules.Text != "Initial rules" {
		t.Fatal(rules, err)
	}
	if _, err = c.SaveInstructions(ctx, "Edited rules", rules.Revision); err != nil {
		t.Fatal(err)
	}
	if code, _ = call("POST", "/api/v1/assistants", body, endpoint.Token); code != 200 {
		t.Fatal(code)
	}
	rules, _ = c.Instructions(ctx)
	if rules.Text != "Edited rules" {
		t.Fatal("creation retry reset rules")
	}
	if code, _ = call("POST", "/api/v1/assistants", strings.Replace(body, "Initial rules", "Different", 1), endpoint.Token); code < 400 {
		t.Fatal("unbound creation retry")
	}
	streamctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(streamctx, "GET", "http://runtime/api/v1/events?assistant_id=main", nil)
	request.Header.Set("Authorization", "Bearer "+endpoint.Token)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := response.Body.Read(buf)
	response.Body.Close()
	if err != nil || !strings.Contains(string(buf[:n]), "event: snapshot") {
		t.Fatal(string(buf[:n]), err)
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime, err = product.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if runtime.BootID == endpoint.BootID || runtime.Root.Identity.RuntimeID != endpoint.RuntimeID {
		t.Fatal("restart identity")
	}
	c, err = runtime.Control.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	rules, _ = c.Instructions(ctx)
	if rules.Text != "Edited rules" {
		t.Fatal(rules)
	}
}
