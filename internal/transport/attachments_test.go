package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestAttachmentHTTPAuthenticationAndFinalCapabilityCheck(t *testing.T) {
	ctx := context.Background()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	service, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "local", BootID: "boot", Keys: keys, Manager: manager})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	service.Registry.Create(ctx, "create", "Pot")
	profile, err := service.Profiles.Save(ctx, control.SaveProfile{RequestID: "provider", ProfileID: "test", Name: "Test", Model: "gpt-4.1", Endpoint: "https://fixture.invalid/v1", Protocol: "openai_chat", APIKey: "fake", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.Profiles.ResolveAssistant(control.AssistantSelection{ProfileID: profile.ID, ProfileRevision: profile.Revision, Model: profile.Model})
	if err != nil {
		t.Fatal(err)
	}
	c, err := service.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	handler := Handler(&Application{Control: service, RuntimeID: "local", BootID: "boot"}, "secret")
	call := func(method, path string, body any, token string) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	begin := map[string]any{"action": "begin", "id": "pdf", "name": "report.pdf", "mime_type": "application/pdf", "size": 14}
	if w := call("POST", "/api/v1/attachments", begin, "wrong"); w.Code != 401 {
		t.Fatal("unauthenticated upload", w.Code)
	}
	pdf := []byte("%PDF-1.7\n%%EOF")
	begin["size"] = len(pdf)
	for _, body := range []any{begin, map[string]any{"action": "chunk", "id": "pdf", "data": base64.StdEncoding.EncodeToString(pdf)}, map[string]any{"action": "finish", "id": "pdf"}} {
		w := call("POST", "/api/v1/attachments?assistant_id=main", body, "secret")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var status struct {
		Attachment core.Attachment `json:"attachment"`
	}
	w := call("POST", "/api/v1/attachments?assistant_id=main", map[string]any{"action": "finish", "id": "pdf"}, "secret")
	if err = json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if w := call("GET", "/api/v1/attachments/pdf?assistant_id=main", nil, "wrong"); w.Code != 401 {
		t.Fatal("unauthenticated read", w.Code)
	}
	if w := call("GET", "/api/v1/attachments/pdf?assistant_id=other", nil, "secret"); w.Code == 200 {
		t.Fatal("cross-owner read")
	}
	if w := call("GET", "/api/v1/attachments/pdf?assistant_id=main&offset=0", nil, "secret"); w.Code != 200 {
		t.Fatal("owner read", w.Code, w.Body.String())
	}
	config, _ := c.Configuration(ctx)
	spec = *config.Config
	spec.SourceProfileRevision = 999
	spec.Params.Agent.ModelCatalog = &core.ModelCatalog{SchemaVersion: 1, Models: map[string]map[string]json.RawMessage{}, Default: json.RawMessage(`{"input_modalities":["text"]}`)}
	spec.Rehash()
	if err = c.Configure(ctx, spec, config.Generation); err != nil {
		t.Fatal(err)
	}
	request := protocol.SendMessage{RequestID: "bypass-ui", ConversationID: "main", Attachments: []core.Attachment{status.Attachment}}
	if w := call("POST", "/api/v1/messages?assistant_id=main", request, "secret"); w.Code == 200 {
		t.Fatal("unsupported attachment submission bypass")
	}
	if w := call("POST", "/api/v1/attachments?assistant_id=main", begin, "secret"); w.Code == 200 {
		t.Fatal("unsupported upload retry bypass")
	}
	snapshot, _ := c.Snapshot(ctx)
	if len(snapshot.Messages) != 0 || snapshot.Agent.Attachments.PDF || snapshot.Agent.Attachments.Image {
		t.Fatal("rejected input changed messages or capabilities", snapshot)
	}
}
