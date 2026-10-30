package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/internal/attachments"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestAttachmentControlBytesRetryOwnershipAndRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pdf := []byte("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\n%%EOF")
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(base64.StdEncoding.EncodeToString(pdf))) || !bytes.Contains(body, []byte(`"type":"file"`)) {
			t.Errorf("provider did not receive native original PDF: %s", body)
		}
		if bytes.Contains(body, []byte("attachments")) {
			t.Error("stable references leaked into provider JSON")
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Read PDF\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	root := t.TempDir()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	options := Options{Root: root, RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys}
	s, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = legacyMainAssistant(ctx, s, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	profile, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "provider", ProfileID: "test", Name: "Test", Model: "gpt-4.1", Endpoint: upstream.URL, Protocol: "openai_chat", APIKey: "fake", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec.Params.Agent.ModelCatalog = &core.ModelCatalog{SchemaVersion: 1, Models: map[string]map[string]json.RawMessage{}, Default: json.RawMessage(`{"input_modalities":["text","pdf"]}`)}
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Params.Agent.AutoCompact = false
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	var uploaded attachments.Status
	for _, u := range []attachments.Upload{{Action: "begin", ID: "pdf", Name: "original.pdf", MIMEType: "application/pdf", Size: uint64(len(pdf))}, {Action: "chunk", ID: "pdf", Data: base64.StdEncoding.EncodeToString(pdf)}, {Action: "finish", ID: "pdf"}} {
		uploaded, err = s.UploadAttachment(ctx, "main", u)
		if err != nil {
			t.Fatal(err)
		}
	}
	a := uploaded.Attachment
	if used, err := s.attachmentReferenced("main", a.ID); err != nil || used {
		t.Fatal("unused upload", used, err)
	}
	request := protocol.SendMessage{RequestID: "original-input", ConversationID: "main", Attachments: []core.Attachment{a}}
	receipt, err := c.Send(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	state := awaitConversation(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 2 })
	if state.Messages[0].Text != "" || len(state.Messages[0].Attachments) != 1 || state.Messages[1].Status != protocol.Failed {
		t.Fatal(state.Messages)
	}
	if used, err := s.attachmentReferenced("main", a.ID); err != nil || !used {
		t.Fatal("accepted reference missing", used, err)
	}
	duplicate, err := c.Send(ctx, request)
	if err != nil || !duplicate.Duplicate || duplicate.MessageID != receipt.MessageID || calls.Load() != 1 {
		t.Fatal("send duplicate replayed model", duplicate, err, calls.Load())
	}
	request.Attachments[0].Name = "spoof.pdf"
	if _, err = c.Send(ctx, request); err == nil {
		t.Fatal("request identity accepted changed attachment")
	}
	request.Attachments[0] = a
	retry, err := c.RetryFailed(ctx, "retry-click", receipt.MessageID)
	if err != nil || retry.MessageID != receipt.MessageID {
		t.Fatal(retry, err)
	}
	state = awaitConversation(t, c, func(s protocol.Snapshot) bool {
		return s.Turn == nil && len(s.Messages) == 2 && s.Messages[1].Status == protocol.Complete
	})
	if calls.Load() != 2 || state.Messages[1].Text != "Read PDF" || state.Messages[1].Error != "" || state.Messages[0].Status != protocol.Complete {
		t.Fatal("retry did not reuse original projections", state.Messages, calls.Load())
	}
	if r, err := c.RetryFailed(ctx, "retry-click", receipt.MessageID); err != nil || !r.Duplicate || calls.Load() != 2 {
		t.Fatal("retry receipt replay", r, err, calls.Load())
	}
	if _, err = s.Attachments.Upload("main", attachments.Upload{Action: "remove", ID: a.ID}, core.AttachmentCapabilities{}); err == nil {
		t.Fatal("history original deleted")
	}
	if _, err = s.Attachments.Chunk("other", a.ID, 0); err == nil {
		t.Fatal("cross-owner download")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	manager2, _ := execution.New("local", "reboot", execution.DefaultLimits())
	options.Manager = manager2
	options.BootID = "reboot"
	s2, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	c2, err := s2.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := c2.Snapshot(ctx)
	if err != nil || len(restored.Messages) != 2 || restored.Messages[0].Attachments[0] != a {
		t.Fatal("history refs lost on restart", restored, err)
	}
	raw, err := s2.Attachments.Read("main", a)
	if err != nil || !bytes.Equal(raw, pdf) {
		t.Fatal("original lost on restart", err)
	}
	db, err := storage.OpenSQLite(filepath.Join(root, "assistants", "main", "conversation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkpoint, err := readConversationCheckpoint(ctx, db, filepath.Join(root, "assistants", "main", "conversation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(checkpoint), base64.StdEncoding.EncodeToString(pdf)) {
		t.Fatal("base64 persisted in checkpoint")
	}
	var saved agent.Checkpoint
	if err = json.Unmarshal([]byte(checkpoint), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) == 0 {
		t.Fatal("missing model history")
	}
	for _, message := range saved.Messages {
		if len(message.Documents) > 0 {
			t.Fatal("document bytes persisted in checkpoint")
		}
	}

}
func TestResolveRejectsUnsupportedAndDoesNotMutateCheckpoint(t *testing.T) {
	t.Parallel()
	s, err := attachments.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	control := &Service{Attachments: s}
	spec := core.ResolvedSpec{}
	spec.GatewayProtocol = "gemini"
	spec.Params.Agent.ModelCatalog = &core.ModelCatalog{SchemaVersion: 1, Models: map[string]map[string]json.RawMessage{}, Default: json.RawMessage(`{"input_modalities":["text","pdf"]}`)}
	pdf := []byte("%PDF-1.7\n%%EOF")
	caps := spec.AttachmentCapabilities()
	var status attachments.Status
	for _, u := range []attachments.Upload{{Action: "begin", ID: "pdf", Name: "x.pdf", MIMEType: "application/pdf", Size: uint64(len(pdf))}, {Action: "chunk", ID: "pdf", Data: base64.StdEncoding.EncodeToString(pdf)}, {Action: "finish", ID: "pdf"}} {
		status, err = s.Upload("main", u, caps)
		if err != nil {
			t.Fatal(err)
		}
	}
	message := modelprotocol.TextMessage("user", "")
	message.Attachments = []core.Attachment{status.Attachment}
	message.Kind.Key = "current"
	resolved, err := control.resolveAttachments("main", "current")(context.Background(), spec, []modelprotocol.Message{message})
	if err != nil || len(resolved[0].Documents) != 1 || len(message.Documents) != 0 {
		t.Fatal("resolve mutation", resolved, err)
	}
	spec.Params.Agent.ModelCatalog = nil
	if _, err = control.resolveAttachments("main", "current")(context.Background(), spec, []modelprotocol.Message{message}); err == nil {
		t.Fatal("unsupported attachment silently dropped")
	}
	if errors.Is(err, agent.ErrUncertainEffects) {
		t.Fatal("resolution is an unsent failure")
	}
	message.Kind.Key = "previous"
	historical, err := control.resolveAttachments("main", "current")(context.Background(), spec, []modelprotocol.Message{message})
	if err != nil || len(historical[0].Documents) != 0 || !strings.Contains(historical[0].Text, "contents are omitted") {
		t.Fatal("historical omission was not explicit", historical, err)
	}
}
