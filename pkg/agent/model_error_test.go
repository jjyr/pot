package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"strings"
	"testing"
)

func TestFailedModelClearsSampleFence(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		cleared bool
	}{{"rejected", &core.ModelRequestError{Cause: errors.New("provider rejected"), Disposition: "rejected"}, true}, {"unsent", &core.ModelRequestError{Cause: errors.New("configuration invalid"), Disposition: "unsent"}, true}, {"network lost", errors.New("transport result unknown"), true}, {"truncated stream", nil, true}} {
		t.Run(test.name, func(t *testing.T) {
			a, f, s, thread, execution := setup(t)
			s.model = func(core.ModelRequest, int) (string, error) { return "", test.err }
			if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); err == nil || test.err != nil && !errors.Is(err, test.err) {
				t.Fatal(err)
			}
			var checkpoint Checkpoint
			if err := json.Unmarshal(f.body, &checkpoint); err != nil {
				t.Fatal(err)
			}
			if (checkpoint.ModelRequestID == nil) != test.cleared {
				t.Fatal("incorrect model request uncertainty", checkpoint.ModelRequestID)
			}
		})
	}
}

func TestInterruptedSampleRetryRetainsToolReceiptAndDiscardsPartialCalls(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	dispatched := 0
	a.Dispatch = func(context.Context, core.ExecutionContext, m.ToolCall, func(core.Update) error) (json.RawMessage, error) {
		dispatched++
		return json.RawMessage(`{"stdout":"effect completed"}`), nil
	}
	var requestIDs []string
	s.model = func(request core.ModelRequest, index int) (string, error) {
		requestIDs = append(requestIDs, request.ID)
		switch index {
		case 0:
			return response("", m.ToolCall{ID: "completed", Name: "probe", Arguments: json.RawMessage(`{}`)}), nil
		case 1:
			// The provider proposed another effect, but disconnected before a
			// complete response. No part of this proposal may be executed.
			return "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"partial\",\"function\":{\"name\":\"probe\",\"arguments\":\"{}\"}}]}}]}\n\n", nil
		default:
			if !strings.Contains(string(request.Body), "effect completed") || strings.Contains(string(request.Body), "partial") {
				t.Error("retry lost the confirmed receipt or included partial output", string(request.Body))
			}
			return response("done"), nil
		}
	}
	request := core.TurnRequest{RequestID: "turn", Text: "do work"}
	if err := a.Execute(context.Background(), execution, thread, request, false, func(core.AgentEvent) error { return nil }); err == nil {
		t.Fatal("truncated sample succeeded")
	}
	if err := a.Execute(context.Background(), execution, thread, request, true, func(core.AgentEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var state Checkpoint
	if err := json.Unmarshal(f.body, &state); err != nil {
		t.Fatal(err)
	}
	if dispatched != 1 || len(state.Receipts) != 1 || state.CompletedRequest != "turn" || len(requestIDs) != 3 || requestIDs[1] == requestIDs[2] {
		t.Fatal("retry replayed an effect or reused the interrupted model identity", dispatched, state, requestIDs)
	}
}

func TestFailedSampleCannotClearFenceWhenPersistenceFails(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	s.model = func(core.ModelRequest, int) (string, error) {
		f.saveErr = errors.New("disk full")
		return "", errors.New("connection lost")
	}
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil }); !errors.Is(err, f.saveErr) {
		t.Fatal(err)
	}
	var state Checkpoint
	if err := json.Unmarshal(f.body, &state); err != nil || state.ModelRequestID == nil {
		t.Fatal("unsaved cleanup lost its recovery fence", err, state)
	}
}
