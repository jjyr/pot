package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

// Real configured provider and production Agent/executor/profile tool. All writes
// use a disposable Control; no production conversation or identity is changed.
func TestConfiguredModelAgentProfileLive(t *testing.T) {
	if os.Getenv("POT_LIVE") != "1" || os.Getenv("POT_CONFIGURED_LIVE_ROOT") == "" {
		t.Skip("requires configured-machine live opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	profile, provider, credential := configuredLiveModel(t, ctx, os.Getenv("POT_CONFIGURED_LIVE_ROOT"))
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 12 {
			http.Error(w, "live request bound reached", 429)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), "POST", provider.Endpoint, io.LimitReader(r.Body, 8*1024*1024))
		if err != nil {
			http.Error(w, "request failed", 500)
			return
		}
		req.Header.Set("Authorization", "Bearer "+string(credential))
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "upstream connection failed", 502)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	root := t.TempDir()
	manager, err := execution.New("profile-live", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := Open(Options{Context: ctx, Root: root, RuntimeID: "profile-live", BootID: "boot", Workspace: filepath.Join(root, "workspace"), AgentHome: filepath.Join(root, "agent"), Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.Close()
		manager.Stop()
		drain, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := manager.Drain(drain); err != nil {
			t.Error(err)
		}
	}()
	if err = s.Permissions.Configure("profile-live", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Profiles.Save(ctx, SaveProfile{RequestID: "live", Name: "Configured profile qualification", Model: profile.Model, Endpoint: proxy.URL, Protocol: provider.Protocol, APIKey: "isolated-proxy", MaxOutputTokens: 4096, ReasoningEffort: profile.Params.ReasoningEffort})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "create", Name: "Pot", Locale: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Conversation(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	updates, remove, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	_, err = c.Send(ctx, protocol.SendMessage{RequestID: "generate-avatar", ConversationID: a.ID, Text: "请直接在你当前助手工作目录创作一张原创的牛和马并排的可爱头像，背景浅色，用 Python 标准库绘制为 PNG（可用 zlib/struct 编码 PNG），不要下载现成图片，不要使用 SVG，也不要创建后台任务。完成后把你自己的名称改为「牛马」，并把刚生成的 PNG 设置为你的头像。请实际调用你的 profile 工具保存名称和头像，完成后简短回复。"})
	if err != nil {
		t.Fatal(err)
	}
	var final protocol.Snapshot
	for {
		select {
		case <-ctx.Done():
			t.Fatal("live profile qualification timed out")
		case snapshot := <-updates:
			if snapshot.Turn == nil && len(snapshot.Messages) > 1 {
				final = snapshot
				if snapshot.Agent.Detail != nil {
					t.Fatal("live profile failed", *snapshot.Agent.Detail)
				}
				goto finished
			}
		}
	}
finished:
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := definitions[0]
	if d.Name != "牛马" || d.Avatar == "" || assistant.ValidateAvatar(d.Avatar) != nil {
		t.Fatalf("live model did not save profile: name=%q has_avatar=%t requests=%d", d.Name, d.Avatar != "", requests.Load())
	}
	if output := os.Getenv("POT_LIVE_OUTPUT"); output != "" {
		if err = os.MkdirAll(output, 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(d.Avatar, "data:image/png;base64,"))
		if err = os.WriteFile(filepath.Join(output, "avatar.png"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		evidence, _ := json.MarshalIndent(map[string]any{"model": profile.Model, "protocol": provider.Protocol, "requests": requests.Load(), "name": d.Name, "revision": d.Revision, "reply": final.Messages[len(final.Messages)-1].Text}, "", "  ")
		if err = os.WriteFile(filepath.Join(output, "evidence.json"), evidence, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("model=%s protocol=%s requests=%d profile=%s revision=%d avatar=128x128 PNG", profile.Model, provider.Protocol, requests.Load(), d.Name, d.Revision)
}
