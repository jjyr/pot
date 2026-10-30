package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/protocol"
)

func statusEvent(request, state string, cursor int64, ids ...string) localui.Event {
	value := protocol.AgentExecutionStatus{BootID: "boot", ConversationID: "main", ConversationCursor: cursor, AssistantID: "main", RequestID: request, State: state, Terminal: state == "completed", ReplyIDs: ids, Assistant: protocol.AgentExecutionProgress{ID: "main", State: state, ActiveTools: []string{}}}
	raw, _ := json.Marshal(value)
	return localui.Event{Type: "execution_status", Data: raw}
}
func chatEvent(kind, boot string, base, cursor int64, ids ...string) localui.Event {
	messages := []protocol.UIMessage{}
	for _, id := range ids {
		messages = append(messages, protocol.UIMessage{Message: protocol.Message{ID: id, ConversationID: "main", Role: protocol.Assistant, Status: protocol.Complete, Text: id}, Version: cursor})
	}
	raw, _ := json.Marshal(protocol.UIFrame{Kind: kind, BaseCursor: base, UISnapshot: protocol.UISnapshot{Snapshot: protocol.Snapshot{APIVersion: protocol.APIVersion, Agent: protocol.AgentStatus{Phase: protocol.Ready}, BootID: boot, ConversationID: "main", Cursor: cursor}, Messages: messages}})
	return localui.Event{Type: "conversation", Data: raw}
}

func TestAskWaitsForConversationCursorAndSubmitsOnce(t *testing.T) {
	submits := 0
	api := func(ctx context.Context, owner, method, path string, body any) (json.RawMessage, error) {
		switch path {
		case "/api/v1/assistants":
			return json.RawMessage("[{\"id\":\"main\"}]"), nil
		case "/api/v1/snapshot":
			return chatEvent("reset", "boot", 0, 0).Data, nil
		case "/api/v1/operator/ask":
			ask, ok := body.(control.AskRequest)
			if !ok || ask.Effort != "high" || ask.TaskEffort != "low" {
				t.Fatalf("%+v", body)
			}
			submits++
			return json.RawMessage("{\"message_id\":\"input\"}"), nil
		}
		t.Fatalf("unexpected query (polling?): %s", path)
		return nil, nil
	}
	streams := func(ctx context.Context, owner, request string, receive func(localui.Event) error) error {
		for _, event := range []localui.Event{statusEvent(request, "running", 2, "early"), chatEvent("reset", "boot", 0, 2, "early"), statusEvent(request, "completed", 10, "early", "final"), chatEvent("delta", "boot", 2, 9)} {
			if err := receive(event); err != nil {
				t.Fatal("exited before reply barrier", err)
			}
		}
		return receive(chatEvent("delta", "boot", 9, 10, "final"))
	}
	var out bytes.Buffer
	if err := executeExperiment(t.Context(), experimentOptions{Command: "ask", Prompt: "go", Effort: "high", TaskEffort: "low"}, api, streams, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if submits != 1 || out.String() != "early\nfinal\n" {
		t.Fatal(submits, out.String())
	}
}

func TestExecutionReconnectDeduplicatesAndHydratesOldReply(t *testing.T) {
	attempts, history := 0, 0
	api := func(context.Context, string, string, string, any) (json.RawMessage, error) {
		history++
		raw, _ := json.Marshal(protocol.UIPage{BootID: "boot", ConversationID: "main", Cursor: 10, Messages: []protocol.UIMessage{{Message: protocol.Message{ID: "early", Role: protocol.Assistant, Status: protocol.Complete, Text: "early"}}}})
		return raw, nil
	}
	streams := func(ctx context.Context, owner, request string, receive func(localui.Event) error) error {
		attempts++
		events := []localui.Event{statusEvent(request, "running", 2, "early"), chatEvent("reset", "boot", 0, 2, "early")}
		if attempts == 2 {
			events = []localui.Event{statusEvent(request, "completed", 10, "early", "final"), chatEvent("reset", "boot", 0, 10, "final")}
		}
		for _, event := range events {
			if err := receive(event); err != nil {
				return err
			}
		}
		return io.EOF
	}
	var out bytes.Buffer
	if err := waitExperiment(t.Context(), experimentOptions{Assistant: "main"}, "req", api, streams, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || history != 1 || out.String() != "early\nfinal\n" {
		t.Fatal(attempts, history, out.String())
	}
}

func TestExecutionBlockedJSONAndCancellation(t *testing.T) {
	streams := func(ctx context.Context, owner, request string, receive func(localui.Event) error) error {
		if err := receive(statusEvent(request, "waiting_approval", 3)); err != nil {
			return err
		}
		return receive(chatEvent("reset", "boot", 0, 3))
	}
	var out bytes.Buffer
	if err := waitExperiment(t.Context(), experimentOptions{Assistant: "main", JSON: true}, "req", nil, streams, &out, io.Discard); err == nil || !json.Valid(out.Bytes()) {
		t.Fatal(err, out.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitExperiment(ctx, experimentOptions{}, "req", nil, streams, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "remains active") {
		t.Fatal(err)
	}
}

func TestExecutionWrongBootCannotSatisfyBarrier(t *testing.T) {
	streams := func(ctx context.Context, owner, request string, receive func(localui.Event) error) error {
		if err := receive(statusEvent(request, "completed", 1, "final")); err != nil {
			return err
		}
		if err := receive(chatEvent("reset", "old-boot", 0, 999, "final")); err != nil {
			t.Fatal("wrong boot satisfied barrier", err)
		}
		return receive(chatEvent("reset", "boot", 0, 1, "final"))
	}
	if err := waitExperiment(t.Context(), experimentOptions{Assistant: "main"}, "req", nil, streams, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionSubscriptionErrorDoesNotMeanCompleted(t *testing.T) {
	streams := func(context.Context, string, string, func(localui.Event) error) error {
		return protocol.APIError{Code: "unauthorized", Message: "revoked"}
	}
	err := waitExperiment(t.Context(), experimentOptions{}, "req", nil, streams, io.Discard, io.Discard)
	var failure protocol.APIError
	if !errors.As(err, &failure) || failure.Code != "unauthorized" {
		t.Fatal(err)
	}
}
