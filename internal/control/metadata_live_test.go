package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// These are synthetic conversations only. Wire captures never contain headers,
// live history or credentials and are removed with the private test directory.
func TestConfiguredModelMetadataLive(t *testing.T) {
	if os.Getenv("POT_LIVE") != "1" || os.Getenv("POT_REFERENCE_LIVE_ROOT") == "" {
		t.Skip("requires POT_LIVE=1 and POT_REFERENCE_LIVE_ROOT")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	source := os.Getenv("POT_REFERENCE_LIVE_ROOT")
	profile, provider, credential := configuredLiveModel(t, ctx, source)
	if provider.Protocol != "openai_responses" {
		t.Fatal("this comparison requires the configured Responses protocol")
	}
	effort := "provider default"
	if profile.Params.ReasoningEffort != nil {
		effort = *profile.Params.ReasoningEffort
	}
	redact := func(b []byte) []byte { return bytes.ReplaceAll(b, credential, []byte("[REDACTED]")) }
	t.Logf("model=%s protocol=%s effort=%v; at most two requests, no retries", profile.Model, provider.Protocol, effort)
	for _, variant := range []string{"native-replay", "text-history"} {
		t.Run(variant, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			calls := 0
			var wire []byte
			var wireBody []byte
			var captureErr error
			var requestID string
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls++
				n := calls
				mu.Unlock()
				if n > 1 {
					http.Error(w, "live request cap", 429)
					return
				}
				body, e := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
				if e != nil || len(body) > 2<<20 || !json.Valid(body) {
					http.Error(w, "read failed", 500)
					return
				}
				if variant == "native-replay" && !bytes.Contains(body, []byte(`"phase":"commentary"`)) {
					mu.Lock()
					captureErr = fmt.Errorf("native replay missing before paid call")
					mu.Unlock()
					http.Error(w, "invalid fixture", 500)
					return
				}
				// Redact the configured secret if it unexpectedly appears in a body. The
				// synthetic fixtures contain no other secret-bearing content.
				mu.Lock()
				wireBody = redact(body)
				mu.Unlock()
				response, e := gateway.Forward(r.Context(), provider, credential, body, "", 75*time.Second)
				if e != nil {
					http.Error(w, "upstream failed", 502)
					return
				}
				defer response.Body.Close()
				w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
				w.WriteHeader(response.StatusCode)
				var captured bytes.Buffer
				buf := make([]byte, 8192)
				for {
					n, e := response.Body.Read(buf)
					if n > 0 {
						if captured.Len()+n > 2<<20 {
							mu.Lock()
							captureErr = fmt.Errorf("capture bound exceeded")
							mu.Unlock()
							return
						}
						captured.Write(buf[:n])
						if _, we := w.Write(buf[:n]); we != nil {
							break
						}
						w.(http.Flusher).Flush()
					}
					if e != nil {
						break
					}
				}
				mu.Lock()
				wire = redact(captured.Bytes())
				mu.Unlock()
			}))
			defer proxy.Close()
			manager, err := execution.New("metadata-live", "boot", execution.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			keys, _ := secrets.NewMemoryKey(make([]byte, 32))
			service, err := Open(Options{Context: ctx, Root: root, RuntimeID: "metadata-live", BootID: "boot", Workspace: filepath.Join(root, "workspace"), AgentHome: filepath.Join(root, "agent"), Manager: manager, Keys: keys})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				service.Close()
				manager.Stop()
				drain, done := context.WithTimeout(context.Background(), 10*time.Second)
				defer done()
				if e := manager.Drain(drain); e != nil {
					t.Error(e)
				}
			}()
			if _, err = service.Registry.Create(ctx, "create", "Metadata qualification"); err != nil {
				t.Fatal(err)
			}
			saved, err := service.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "metadata-live", Name: "Metadata qualification", Model: profile.Model, Endpoint: proxy.URL + "/v1/responses", Protocol: provider.Protocol, APIKey: string(credential), MaxOutputTokens: 1536, ReasoningEffort: profile.Params.ReasoningEffort})
			if err != nil {
				t.Fatal(err)
			}
			spec, err := service.Profiles.Resolve(saved.ID)
			if err != nil {
				t.Fatal(err)
			}
			allowed := []string{}
			spec.Params.Agent.AllowedTools = &allowed
			spec.Params.Agent.BuiltinTools = []string{}
			spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
			spec.Params.Agent.AutoCompact = false
			spec.Params.Agent.RequestTimeoutMS = 80000
			if err = spec.Rehash(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "qualification.sqlite")
			store, err := storage.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			history := []modelprotocol.Message{modelprotocol.TextMessage("user", "请把修复放到独立分支和 worktree，交给任务处理。"), modelprotocol.TextMessage("assistant", "我会派发任务处理。")}
			history[0].ID = "SYNTHETIC_USER"
			history[1].ID = "SYNTHETIC_ASSISTANT"
			for i, m := range history {
				if _, err = store.Save(ctx, protocol.Message{ID: m.ID, ConversationID: "main", Role: protocol.Role(m.Role), Text: m.Text, Status: protocol.Complete, CreatedAtMS: int64(i + 1)}); err != nil {
					t.Fatal(err)
				}
			}
			history[1].Calls = []modelprotocol.ToolCall{{ID: "call_fixture", Name: "dispatch_fixture", Arguments: json.RawMessage(`{}`)}}
			route := spec.Provider + ":" + spec.Model + ":" + spec.GatewayProtocol
			// Match Agent's route exactly so native provider replay is exercised.
			if variant == "native-replay" {
				history[1].Replay = json.RawMessage(`[{"id":"msg_fixture","type":"message","role":"assistant","phase":"commentary","status":"completed","content":[{"type":"output_text","text":"我会派发任务处理。"}]},{"type":"function_call","call_id":"call_fixture","name":"dispatch_fixture","arguments":"{}"}]`)
				replayRoute := route
				history[1].ReplayRoute = &replayRoute
			}
			result := modelprotocol.TextMessage("tool", `{"accepted":true,"task_id":"fixture_task","state":"queued","branch":"fix/example","worktree":"/tmp/example"}`)
			result.CallID = "call_fixture"
			history = append(history, result)
			checkpoint := agent.Checkpoint{Type: "context", Version: 2, Messages: history, Pending: []modelprotocol.ToolCall{}, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}}
			payload, _ := json.Marshal(checkpoint)
			if err = store.SaveContext(ctx, payload); err != nil {
				t.Fatal(err)
			}
			store.Close()
			var conversation *assistant.Conversation
			conversation, err = assistant.OpenConversation(path, assistant.ConversationOptions{Context: ctx, Owner: "main", RuntimeID: "metadata-live", BootID: "boot", Manager: manager, InitialSpec: &spec, MaxRequests: 1, MaxTurnRequests: 1, Prepare: func(ctx context.Context, thread core.Thread, request core.TurnRequest) (assistant.RunOptions, error) {
				options, e := service.prepare(ctx, conversation, thread, request)
				options.Tools = nil
				options.Services = metadataLivePort{ExecutionServices: options.Services, record: func(id string) { mu.Lock(); requestID = id; mu.Unlock() }}
				return options, e
			}})
			if err != nil {
				t.Fatal(err)
			}
			service.conversations["main"] = conversation
			updates, remove, err := conversation.Subscribe(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer remove()
			receipt, err := conversation.Send(ctx, protocol.SendMessage{RequestID: "confirm", ConversationID: "main", Text: "任务工具已返回接受结果。请简短确认排队状态和将使用独立分支及 worktree。", References: []core.MessageReference{{MessageID: "SYNTHETIC_ASSISTANT", Quote: "我会派发任务处理。"}}})
			if err != nil {
				t.Fatal(err)
			}
			var snapshot protocol.Snapshot
			for {
				select {
				case <-ctx.Done():
					t.Fatal("live test timed out")
				case snapshot = <-updates:
					if snapshot.Turn == nil && len(snapshot.Messages) > 0 && snapshot.Messages[len(snapshot.Messages)-1].ReplyTo == receipt.MessageID {
						goto finished
					}
				}
			}
		finished:
			// Read back durable transcript and checkpoint, not only a UI projection.
			persisted, err := storage.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer persisted.Close()
			savedContext, err := persisted.LoadContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var savedText string
			if err = persisted.DB().QueryRowContext(ctx, "SELECT json_extract(body,'$.text') FROM messages WHERE json_extract(body,'$.role')='assistant' ORDER BY rowid DESC LIMIT 1").Scan(&savedText); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if captureErr != nil {
				t.Fatal(captureErr)
			}
			for name, data := range map[string][]byte{"request.json": wireBody, "response.sse": wire, "checkpoint.json": savedContext} {
				if err = os.WriteFile(filepath.Join(root, requestID+"-"+name), redact(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var sse modelprotocol.SSE
			frames, err := sse.Push(wire)
			if err != nil {
				t.Fatal(err)
			}
			decodeInput := append([]modelprotocol.Message{}, history...)
			for _, message := range snapshot.Messages {
				if message.Role == protocol.User {
					m := modelprotocol.TextMessage("user", message.Text)
					m.ID = message.ID
					m.References = message.References
					decodeInput = append(decodeInput, m)
				}
			}
			decoder := modelprotocol.NewRequestDecoder(modelprotocol.Request{Protocol: modelprotocol.OpenAIResponses, Messages: decodeInput})
			var deltas strings.Builder
			var decodedEvents []modelprotocol.StreamEvent
			for _, frame := range frames {
				event, e := decoder.Push(frame)
				if e != nil {
					t.Fatal(e)
				}
				deltas.WriteString(event.TextDelta)
				decodedEvents = append(decodedEvents, event)
				var item map[string]any
				_ = json.Unmarshal(frame, &item)
				if item["type"] == "response.output_item.done" {
					v := item["item"].(map[string]any)
					t.Logf("HTTP item type=%v phase=%v content=%v", v["type"], v["phase"], v["content"])
				}
			}
			decoded, err := decoder.Finish()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("request_id=%s requests=%d raw_bytes=%d metadata_in_request=%t native_phase_in_request=%t raw_has_tag=%t deltas=%q decoded=%q persisted=%q", requestID, calls, len(wire), bytes.Contains(wireBody, []byte("pot_message_metadata")), bytes.Contains(wireBody, []byte(`"phase"`)), bytes.Contains(wire, []byte("pot_message_metadata")), deltas.String(), decoded.Text, string(redact([]byte(savedText))))
			if snapshot.Agent.Detail != nil {
				t.Fatal("live model failed", *snapshot.Agent.Detail)
			}
			observation, _ := json.Marshal(map[string]any{"request_id": requestID, "deltas": deltas.String(), "decoded": decoded, "events": decodedEvents, "persisted_text": savedText})
			if err = os.WriteFile(filepath.Join(root, requestID+"-decoded.json"), redact(observation), 0600); err != nil {
				t.Fatal(err)
			}
			if savedText != decoded.Text {
				t.Fatal("decode/persist mismatch")
			}
		})
	}
}

type metadataLivePort struct {
	core.ExecutionServices
	record func(string)
}

func (p metadataLivePort) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	p.record(r.ID)
	return p.ExecutionServices.Model(ctx, r)
}
