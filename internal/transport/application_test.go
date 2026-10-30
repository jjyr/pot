package transport

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/localui"
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

func localAssistantID(t testing.TB, service *control.Service) string {
	t.Helper()
	definitions, err := service.Registry.Definitions(t.Context())
	if err != nil || len(definitions) == 0 {
		t.Fatalf("missing fixture assistant: %v", err)
	}
	return definitions[0].ID
}

func TestSocketProductCreateEventsAuthAndRestart(t *testing.T) {
	ctx := context.Background()
	temporary, err := os.MkdirTemp("", "pot-api-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(temporary)
	root := filepath.Join(temporary, "go")
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	options := product.Options{Root: root, DisableMetadataRefresh: true, Control: control.Options{Keys: keys}}
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
	if code, _ := call("GET", "/api/v1/assistants/default-avatar?assistant_id=missing", "", endpoint.Token); code < 400 {
		t.Fatal("missing assistant accepted")
	}
	body := `{"request_id":"create","text":"Initial rules"}`
	code, data := call("POST", "/api/v1/assistants", body, endpoint.Token)
	if code != 200 {
		t.Fatal(code, string(data))
	}
	var definition map[string]any
	if err = json.Unmarshal(data, &definition); err != nil {
		t.Fatal(err)
	}
	if definition["id"] != strings.ToLower(strings.ReplaceAll(definition["name"].(string), " ", "-")) || strings.Count(definition["id"].(string), "-") != 1 {
		t.Fatal("first assistant did not use a shared two-word ID and initial name")
	}
	for _, path := range []string{"/api/v1/snapshot", "/api/v1/instructions", "/api/v1/agent"} {
		if code, _ := call("GET", path, "", endpoint.Token); code != 200 {
			t.Fatal("default route did not resolve generated assistant ID", path, code)
		}
		if code, _ := call("GET", path+"?assistant_id=missing", "", endpoint.Token); code < 400 {
			t.Fatal("explicit missing assistant silently fell back", path)
		}
	}
	code, data = call("GET", "/api/v1/assistants/default-avatar?assistant_id="+definition["id"].(string), "", endpoint.Token)
	var preview map[string]string
	if code != 200 || json.Unmarshal(data, &preview) != nil || assistant.ValidateAvatar(preview["avatar"]) != nil {
		t.Fatal("invalid reset preview", code)
	}
	if definition["avatar"] != preview["avatar"] {
		t.Fatal("creation did not save the preview image")
	}
	assertAppearanceSeed := func() {
		t.Helper()
		code, data := call("GET", "/api/v1/assistants", "", endpoint.Token)
		var items []map[string]any
		if code != 200 || json.Unmarshal(data, &items) != nil || len(items) != 1 || items[0]["request_id"] != "create" || items[0]["id"] != definition["id"] {
			t.Fatalf("directory lost persisted appearance seed: %d %s", code, data)
		}
		if items[0]["avatar"] != preview["avatar"] {
			t.Fatal("directory lost saved image")
		}
	}
	if definition["request_id"] != "create" {
		t.Fatal("creation response lost appearance seed", definition)
	}
	assertAppearanceSeed()
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
	sentinel := errors.New("conversation event")
	err = localui.NewLocalHTTPClient(client, endpoint.Token, "").Stream(streamctx, "/api/v1/events?assistant_id="+definition["id"].(string), func(event localui.Event) error {
		if event.Type == "conversation" {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
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
	app = &Application{Control: runtime.Control, RuntimeID: runtime.Root.Identity.RuntimeID, BootID: runtime.BootID, Name: "Pot"}
	s, err = StartSocket(ctx, root, app)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	endpoint, err = ReadEndpoint(root)
	if err != nil {
		t.Fatal(err)
	}
	client = endpoint.Client()
	assertAppearanceSeed()
	renameBody, _ := json.Marshal(map[string]any{"name": "Renamed", "revision": definition["revision"]})
	if code, data := call("POST", "/api/v1/assistants/"+definition["id"].(string), string(renameBody), endpoint.Token); code != 200 {
		t.Fatal(code, string(data))
	}
	assertAppearanceSeed()
	c, err = runtime.Control.Conversation(ctx, definition["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	rules, _ = c.Instructions(ctx)
	if rules.Text != "Edited rules" {
		t.Fatal(rules)
	}
}
