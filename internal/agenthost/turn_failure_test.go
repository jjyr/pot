package agenthost

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/agentlink"
	"strings"
	"testing"
)

// Codex 0.163.0 TurnError/CodexErrorInfo shapes, source 83086188ae0614bd882818ff1aa23f3194962012.
func failedTurnNotification(t *testing.T, thread, turn string, info any) rpcMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": "failed", "error": map[string]any{"message": "private-native-error-secret", "additionalDetails": "https://private.invalid/token", "codexErrorInfo": info}}})
	if err != nil {
		t.Fatal(err)
	}
	return rpcMessage{Method: "turn/completed", Params: raw}
}

func TestFailedTurnRetainsOnlySafeProtocolClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		info any
		want string
	}{
		{"context", "contextWindowExceeded", "codex_turn_failed: contextWindowExceeded"},
		{"auth", "unauthorized", "codex_turn_failed: unauthorized"},
		{"rate", "rateLimitExceeded", "codex_turn_failed: rateLimitExceeded"},
		{"http", map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 503}}, "codex_turn_failed: httpConnectionFailed (http_status=503)"},
		{"stream", map[string]any{"responseStreamDisconnected": map[string]any{"httpStatusCode": nil}}, "codex_turn_failed: responseStreamDisconnected"},
		{"unknown", "private-unknown-code", "codex_turn_failed"},
		{"unknown object", map[string]any{"private-token": map[string]any{"httpStatusCode": 401}}, "codex_turn_failed"},
		{"malformed", []string{"unauthorized"}, "codex_turn_failed"},
		{"unknown nested", map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 503, "private": "secret"}}, "codex_turn_failed"},
		{"invalid status", map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 999}}, "codex_turn_failed"},
		{"string status", map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": "401 private"}}, "codex_turn_failed"},
		{"null", nil, "codex_turn_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &appTurn{done: make(chan struct{}), result: TurnResult{ThreadID: "thread", TurnID: "turn"}}
			a.notification(failedTurnNotification(t, "thread", "turn", tc.info))
			if a.result.Status != "failed" || a.result.Error != tc.want || a.result.Text != "" {
				t.Fatal(a.result)
			}
			if strings.Contains(a.result.Error, "private") || strings.Contains(a.result.Error, "secret") {
				t.Fatal("free text escaped classification")
			}
		})
	}
}

func TestTurnFailureClassificationRequiresBoundThreadAndTurn(t *testing.T) {
	for _, tc := range []struct {
		name, thread, turn, boundThread, boundTurn string
		ignored                                    bool
	}{
		{"other thread", "foreign", "turn", "thread", "turn", true},
		{"other turn", "thread", "old-turn", "thread", "turn", true},
		{"missing thread", "", "turn", "thread", "turn", true},
		{"missing turn", "thread", "", "thread", "turn", true},
		{"unbound", "thread", "turn", "thread", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &appTurn{done: make(chan struct{}), result: TurnResult{ThreadID: tc.boundThread, TurnID: tc.boundTurn}}
			a.notification(failedTurnNotification(t, tc.thread, tc.turn, "contextWindowExceeded"))
			if tc.ignored {
				if a.result.Status != "" || a.result.Error != "" {
					t.Fatal(a.result)
				}
				return
			}
			if a.result.Error != "codex_turn_failed" {
				t.Fatal("unproven association enriched failure", a.result)
			}
		})
	}
}

func TestIndependentErrorNotificationCannotFinishOrTaintTurn(t *testing.T) {
	for _, retry := range []bool{false, true} {
		a := &appTurn{done: make(chan struct{}), result: TurnResult{ThreadID: "thread", TurnID: "turn"}}
		raw, _ := json.Marshal(map[string]any{"threadId": "thread", "turnId": "turn", "willRetry": retry, "error": map[string]any{"message": "private-native-error-secret", "codexErrorInfo": "contextWindowExceeded"}})
		a.notification(rpcMessage{Method: "error", Params: raw})
		select {
		case <-a.done:
			t.Fatal("nonterminal notification completed execution")
		default:
		}
		a.notification(rpcMessage{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed","error":null}}`)})
		if a.result.Status != "completed" || a.result.Error != "" {
			t.Fatal("independent error overwrote later success", a.result)
		}
		a.notification(failedTurnNotification(t, "thread", "turn", "unauthorized"))
		if a.result.Status != "completed" || a.result.Error != "" {
			t.Fatal("late error overwrote completed result", a.result)
		}
	}
}

func TestFailedTurnMalformedDetailsStayGeneric(t *testing.T) {
	for _, raw := range []string{
		`{"message":"private","codexErrorInfo":"unauthorized","codexErrorInfo":"contextWindowExceeded"}`,
		`{"message":"private","codexErrorInfo":{"httpConnectionFailed":{"httpStatusCode":503,"httpStatusCode":401}}}`,
		`{"message":"private","codexErrorInfo":"unauthorized","unknown":"private"}`,
		`{"message":false,"codexErrorInfo":"unauthorized"}`,
		`{"codexErrorInfo":"unauthorized"}`,
		`{"message":"private","codexErrorInfo":{"httpConnectionFailed":null}}`,
		`{"message":"private","codexErrorInfo":{"httpConnectionFailed":{"httpStatusCode":503.5}}}`,
	} {
		if got := codexTurnFailure(json.RawMessage(raw)); got != "codex_turn_failed" {
			t.Fatal(got)
		}
	}
}

func TestFailedTurnPersistsOnlySafeDiagnostic(t *testing.T) {
	b := testBackend(t)
	from := agentlink.AssistantTarget("origin", "owner")
	task, err := b.submitTask(t.Context(), agentlink.TaskRequest{From: from, Target: agentlink.NodeTarget(b.nodeID), RequestID: "safe-failure", Text: "fail-with-private-diagnostic"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := b.read(t.Context(), from, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	b.runTask(t.Context(), job)
	status, err := b.Status(t.Context(), from, task.ID)
	if err != nil || status.State != "failed" || status.Error != "codex_turn_failed: httpConnectionFailed (http_status=503)" || status.Result != "" {
		t.Fatal(status, err)
	}
	var raw string
	if err = b.db.QueryRow("SELECT data FROM agent_adapter_jobs WHERE id=?", task.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-native-error-secret", "private.invalid/token", "codexErrorInfo", "additionalDetails"} {
		if strings.Contains(raw, secret) {
			t.Fatal("raw native failure retained in job")
		}
	}
}
