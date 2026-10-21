package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

// Real configured responses choose every message tool call. Production roots
// are read only; the two Controls, credentials, peers and accounting are private.
func TestConfiguredModelAgentLinksLive(t *testing.T) {
	if os.Getenv("POT_LIVE") != "1" || os.Getenv("POT_CONFIGURED_LIVE_ROOT") == "" {
		t.Skip("requires POT_LIVE=1 and POT_CONFIGURED_LIVE_ROOT on the configured machine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	profile, provider, credential := configuredLiveModel(t, ctx, os.Getenv("POT_CONFIGURED_LIVE_ROOT"))
	reasoning := "default"
	if profile.Params.ReasoningEffort != nil {
		reasoning = *profile.Params.ReasoningEffort
	}
	t.Logf("configured model=%s protocol=%s reasoning=%s", profile.Model, provider.Protocol, reasoning)
	var requests atomic.Int32
	defer func() { t.Logf("real model requests=%d (cap 24)", requests.Load()) }()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil || !json.Valid(body) {
			http.Error(w, "invalid live request", http.StatusBadRequest)
			return
		}
		var limits struct {
			MaxOutput     uint64 `json:"max_output_tokens"`
			MaxTokens     uint64 `json:"max_tokens"`
			MaxCompletion uint64 `json:"max_completion_tokens"`
		}
		_ = json.Unmarshal(body, &limits)
		if max(limits.MaxOutput, limits.MaxTokens, limits.MaxCompletion) == 0 || limits.MaxOutput > 2048 || limits.MaxTokens > 2048 || limits.MaxCompletion > 2048 || requests.Add(1) > 24 {
			http.Error(w, "configured Agent Link live request cap", http.StatusTooManyRequests)
			return
		}
		response, err := gateway.Forward(r.Context(), provider, credential, body, r.Header.Get("anthropic-beta"), 90*time.Second)
		if err != nil {
			http.Error(w, "live upstream unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()

	type liveControl struct {
		service *Service
		manager *execution.Manager
		options Options
	}
	open := func(f *liveControl, boot string) {
		t.Helper()
		var err error
		f.manager, err = execution.New(f.options.RuntimeID, boot, execution.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		f.options.Manager, f.options.BootID = f.manager, boot
		f.service, err = Open(f.options)
		if err != nil {
			t.Fatal(err)
		}
	}
	closeControl := func(f *liveControl) {
		t.Helper()
		if f.service == nil {
			return
		}
		if err := f.service.Close(); err != nil {
			t.Error(err)
		}
		f.service = nil
		f.manager.Stop()
		drain, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := f.manager.Drain(drain); err != nil {
			t.Error(err)
		}
	}
	create := func(id string) *liveControl {
		t.Helper()
		root := t.TempDir()
		keys, err := secrets.NewMemoryKey(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		f := &liveControl{options: Options{Context: ctx, Root: root, RuntimeID: id, Keys: keys, Workspace: filepath.Join(root, "workspace"), AgentHome: filepath.Join(root, "agent")}}
		open(f, "boot-first")
		t.Cleanup(func() { closeControl(f) })
		for _, name := range []string{"Live Agent", "Unrelated Agent"} {
			if _, err := f.service.Registry.Create(ctx, "create-"+strings.ReplaceAll(name, " ", "-"), name); err != nil {
				t.Fatal(err)
			}
		}
		settings := core.ModelSettings{}
		if profile.ModelSettings != nil {
			settings = *profile.ModelSettings
		}
		output := uint64(2048)
		settings.MaxOutputTokens = &output
		saved, err := f.service.Profiles.Save(ctx, SaveProfile{RequestID: "live-profile", ProfileID: "live", Name: "Agent Link qualification", Model: profile.Model, Endpoint: proxy.URL, Protocol: provider.Protocol, APIKey: "isolated-configured-model-proxy", ModelSettings: &settings, MaxOutputTokens: output, ReasoningEffort: profile.Params.ReasoningEffort})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := f.service.Profiles.Resolve(saved.ID)
		if err != nil {
			t.Fatal(err)
		}
		allowed := []string{"list_assistants", "send_message"}
		spec.Params.Agent.AllowedTools = &allowed
		spec.Params.Agent.BuiltinTools = []string{}
		spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
		spec.Params.Agent.AutoCompact = false
		spec.Params.Agent.RequestTimeoutMS = 90000
		if err := spec.Rehash(); err != nil {
			t.Fatal(err)
		}
		conversation, err := f.service.Conversation(ctx, "main")
		if err != nil {
			t.Fatal(err)
		}
		if err = conversation.Configure(ctx, spec, 0); err != nil {
			t.Fatal(err)
		}
		if _, err = f.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "configure", Mode: "address", Endpoint: controlPeerEndpoint(f.service)}); err != nil {
			t.Fatal(err)
		}
		return f
	}
	a, b := create("link-live-a"), create("link-live-b")
	invite, err := b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite"})
	if err != nil {
		t.Fatal(err)
	}
	address := invite.(map[string]any)["address"].(string)
	preview, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "preview", Address: address})
	if err != nil {
		t.Fatal(err)
	}
	peer := preview.(map[string]any)["peer"].(map[string]any)
	if peer["node_id"] != "link-live-b" || peer["id"] != "link-live-b" || peer["kind"] != "pot" {
		t.Fatal("preview matched a different Agent")
	}
	for _, f := range []*liveControl{a, b} {
		links, err := f.service.agentLinks.service.List(ctx)
		if err != nil || len(links) != 0 {
			t.Fatal("preview created a connection", err)
		}
	}
	value, err := a.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "connect", Address: address})
	if err != nil {
		t.Fatal(err)
	}
	connection := value.(agentlink.Connection)
	if connection.State != "ready" || connection.Local.Address.NodeID != "link-live-a" || connection.Peer.Address.NodeID != "link-live-b" {
		t.Fatal("pair did not bind exact nodes")
	}
	remote, err := b.service.agentLinks.service.Get(ctx, connection.ID)
	if err != nil || remote.Peer.Address != connection.Local.Address || remote.Local.Address != connection.Peer.Address {
		t.Fatal("pair is not symmetric", err)
	}
	t.Log("preview was read-only; Add established one symmetric, pinned connection")

	wait := func(check func() bool) {
		t.Helper()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("configured Agent Link live timed out", ctx.Err())
			case <-ticker.C:
			}
		}
	}
	round := func(from, to *liveControl, marker, problem, answer string) {
		t.Helper()
		conversation, err := from.service.Conversation(ctx, "main")
		if err != nil {
			t.Fatal(err)
		}
		target := protocol.AgentRef{NodeID: to.options.RuntimeID, Kind: "assistant", AgentID: "main"}
		raw, _ := json.Marshal(target)
		input := fmt.Sprintf("请通过 list_assistants 核对完整地址 %s，然后通过 send_message 向这个已连接的 Agent 发送协作问题：%s：%s？请对方将该标记与计算结果一起回复给你。这是实际消息协作测试，不要自己代答，不创建任务、不操作文件、不运行命令。收到有用结果后不再回复，避免礼貌确认循环。", raw, marker, problem)
		if _, err = conversation.Send(ctx, protocol.SendMessage{RequestID: marker, ConversationID: "main", Text: input}); err != nil {
			t.Fatal(err)
		}
		var original, reply protocol.Envelope
		wait(func() bool {
			history, err := from.service.Mailbox.History(ctx, "main", 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range history {
				e := record.Message
				if strings.Contains(e.Text, marker) && e.From.NodeID == from.options.RuntimeID {
					original = e
				}
				if strings.Contains(e.Text, marker) && strings.Contains(e.Text, answer) && e.From == target {
					reply = e
				}
				if record.State == "failed" || record.State == "interrupted" {
					t.Fatalf("mail processing failed: %+v", record)
				}
			}
			return original.ID != "" && reply.ID != ""
		})
		if reply.ReplyTo == nil || *reply.ReplyTo != original.ID || reply.ConversationID != original.ConversationID || reply.Hops != original.Hops+1 || reply.To != original.From {
			t.Fatal("real model reply lost provenance")
		}
		wait(func() bool {
			for _, item := range []struct {
				s  *Service
				id string
			}{{to.service, original.ID}, {from.service, reply.ID}} {
				var state string
				if err := item.s.Tasks.DB().QueryRowContext(ctx, "SELECT state FROM agent_messages WHERE id=?", item.id).Scan(&state); err != nil {
					t.Fatal(err)
				}
				if state != "processed" {
					return false
				}
			}
			snapshot, err := conversation.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return snapshot.Turn == nil
		})
		// The actual tool invocation must use the generic node address.
		snapshot, err := conversation.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range snapshot.Messages {
			if m.Tool == nil || m.Tool.Name != "send_message" {
				continue
			}
			var send protocol.SendMail
			if json.Unmarshal(m.Tool.Args, &send) == nil && strings.Contains(send.Text, marker) {
				if m.Tool.Phase != "completed" || send.To != target {
					t.Fatalf("real model message call phase=%s args=%s result=%s expected=%s", m.Tool.Phase, m.Tool.Args, m.Tool.Result, raw)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("no real model send_message call")
		}
		t.Logf("%s: %s -> %s, reply=%q, hops=%d, both receivers processed", marker, original.From.NodeID, original.To.NodeID, reply.Text, reply.Hops)
	}
	round(a, b, "ROUND_A", "137+284", "421")
	round(b, a, "ROUND_B", "628-193", "435")
	identity, _ := b.service.Machines.TransportIdentity()
	closeControl(b)
	open(b, "boot-restarted")
	current, _ := b.service.Machines.TransportIdentity()
	if current != identity {
		t.Fatal("restart replaced the peer identity")
	}
	wait(func() bool {
		c, err := a.service.agentLinks.service.Get(ctx, connection.ID)
		return err == nil && c.State == "ready"
	})
	round(a, b, "ROUND_RESTART", "209+406", "615")
	for _, f := range []*liveControl{a, b} {
		var count int
		if err := f.service.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.kind')='message'").Scan(&count); err != nil || count != 6 {
			t.Fatal("unexpected duplicates or reply loop", f.options.RuntimeID, count, err)
		}
	}
	// A user-only client has no provider or local assistant. Only the receiver
	// calls the configured real model through its production message consumer.
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	empty := &liveControl{options: Options{Context: ctx, Root: t.TempDir(), RuntimeID: "live-user-only", Keys: keys}}
	open(empty, "user-only-boot")
	defer closeControl(empty)
	invite, err = b.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "invite"})
	if err != nil {
		t.Fatal(err)
	}
	connected, err := empty.service.ManageAgentLink(ctx, AgentLinkCommand{Action: "connect", Address: invite.(map[string]any)["address"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	directLink := connected.(agentlink.Connection)
	text := "POT190_DIRECT：请计算 23+19，通过 send_message 向本条消息的发送者回复标记 POT190_DIRECT 和结果。不要另发给任何助手，不创建任务。"
	if _, err = empty.service.SendLinkChat(ctx, directLink.ID, "main", "direct-live", text); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		frame, err := empty.service.LinkChat(ctx, directLink.ID, "main")
		if err != nil {
			t.Fatal(err)
		}
		messages := frame.Messages
		return len(messages) == 2 && strings.Contains(messages[1].Text, "42") && strings.Contains(messages[1].Text, "POT190_DIRECT")
	})
	defs, err := empty.service.Registry.Definitions(ctx)
	if err != nil || len(defs) != 0 {
		t.Fatal("hidden local assistant created", defs, err)
	}
	if _, err = empty.service.SendLinkChat(ctx, directLink.ID, "main", "direct-live", text); err != nil {
		t.Fatal(err)
	}
	t.Log("POT190_DIRECT: user-only Pot received the real model reply without a local provider or assistant")
	t.Logf("real model requests=%d (cap 24); restart retained identity and exactly six immutable messages", requests.Load())
}
