package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/pkg/execution"
)

func TestAskUsesRealSocketStatusAndConversationSubscriptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("/tmp", "pot-ask-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"socket reply\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"prompt_tokens_details\":{\"cached_tokens\":30}}}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := control.Open(control.Options{Root: root, RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	owner, err := service.Registry.Create(ctx, "owner", "CLI")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.Profiles.Save(ctx, control.SaveProfile{RequestID: "profile", ProfileID: "profile", Name: "Fixture", Model: "gpt-4.1", Endpoint: provider.URL, Protocol: "openai_chat", APIKey: "fixture", MaxOutputTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := service.Profiles.Resolve(profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := service.Conversation(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = conversation.Configure(ctx, spec, 0); err != nil {
		t.Fatal(err)
	}
	socket, err := transport.StartSocket(ctx, root, &transport.Application{Control: service, RuntimeID: "local", BootID: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close(context.Background())
	var out bytes.Buffer
	if err = experimentCommand(ctx, []string{"ask", "--root", root, "--assistant", owner.ID, "--json", "hello"}, nil, &out, io.Discard); err != nil {
		t.Fatal(err, out.String())
	}
	var result executionResult
	if err = json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err, out.String())
	}
	if result.State != "completed" || !result.Terminal || result.ConversationCursor == 0 || len(result.Messages) != 1 || result.Messages[0].Text != "socket reply" {
		t.Fatalf("%+v", result)
	}
	out.Reset()
	if err = experimentCommand(ctx, []string{"usage", "--root", root, "--assistant", owner.ID, "--ask", result.RequestID, "--group-by", "model", "--details", "--json"}, nil, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var report control.ExperimentUsage
	if err = json.Unmarshal(out.Bytes(), &report); err != nil || report.Accounting.Total.Requests != 1 || report.Accounting.Total.USD != "0.000315000000000" || !report.Accounting.Total.Complete || len(report.Accounting.Records) != 1 {
		t.Fatalf("%s %v", out.String(), err)
	}
	if report.Accounting.Records[0].Price.Source != "models.dev" || report.Accounting.Records[0].Price.Match != "origin_fallback" {
		t.Fatal(report.Accounting.Records)
	}

}
