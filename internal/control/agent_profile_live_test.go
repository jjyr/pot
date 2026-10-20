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
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

// Real configured provider and production Agent/executor/profile tool. All writes
// use a disposable Control; no production conversation or identity is changed.
func TestConfiguredModelAgentProfileLive(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "direct"
		if background {
			name = "background"
		}
		t.Run(name, func(t *testing.T) { configuredModelAgentProfileLive(t, background) })
	}
}

func configuredModelAgentProfileLive(t *testing.T, background bool) {
	if os.Getenv("POT_LIVE") != "1" || os.Getenv("POT_CONFIGURED_LIVE_ROOT") == "" {
		t.Skip("requires configured-machine live opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	profile, provider, credential := configuredLiveModel(t, ctx, os.Getenv("POT_CONFIGURED_LIVE_ROOT"))
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 24 {
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
	input := "请直接在你当前助手工作目录创作一张原创的牛和马并排的可爱头像，背景浅色，用 Python 标准库绘制为 PNG（可用 zlib/struct 编码 PNG），不要下载现成图片，不要使用 SVG，也不要创建后台任务。完成后把你自己的名称改为「牛马」，并把刚生成的 PNG 设置为你的头像。请实际调用你的 profile 工具保存名称和头像，完成后简短回复。"
	if background {
		input = "请创建一个后台任务，创作一张原创的猴子敲键盘头像：穿绿色上衣、浅薄荷绿背景，适合圆形裁切。任务用 Python 标准库绘图并编码为 1024×1024 PNG（可用 zlib/struct），保存为 avatar.png，不下载图片、不用 SVG。任务完成后，你要继续把任务生成的图片换成你自己的头像，并把你的名字改成「键盘猴」。必须实际保存，不能只提供下载链接。"
	}
	_, err = c.Send(ctx, protocol.SendMessage{RequestID: "generate-avatar", ConversationID: a.ID, Text: input})
	if err != nil {
		t.Fatal(err)
	}
	var final protocol.Snapshot
	for {
		select {
		case <-ctx.Done():
			t.Fatal("live profile qualification timed out")
		case snapshot := <-updates:
			if snapshot.Agent.Detail != nil {
				t.Fatal("live profile failed", *snapshot.Agent.Detail)
			}
			if snapshot.Turn == nil && len(snapshot.Messages) > 1 {
				if background {
					var tasks, active, pending int
					for query, dest := range map[string]*int{
						"SELECT COUNT(*) FROM tasks": &tasks,
						"SELECT COUNT(*) FROM tasks WHERE phase IN ('queued','running','waiting','waiting_approval')":                                  &active,
						"SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result' AND state IN ('queued','processing')": &pending,
					} {
						if err := s.Tasks.DB().QueryRowContext(ctx, query).Scan(dest); err != nil {
							t.Fatal(err)
						}
					}
					if tasks == 0 || active != 0 || pending != 0 {
						continue
					}
				}
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
	wantName := "牛马"
	if background {
		wantName = "键盘猴"
	}
	if d.Name != wantName || d.Avatar == a.Avatar || assistant.ValidateAvatar(d.Avatar) != nil {
		t.Fatalf("live model did not save profile: name=%q has_avatar=%t requests=%d", d.Name, d.Avatar != "", requests.Load())
	}
	if background {
		imported := false
		for _, message := range final.Messages {
			if message.Tool == nil || message.Tool.Name != "update_agent_profile" || message.Tool.Phase != "completed" || !strings.HasPrefix(message.Tool.TurnID, "pot-result-") {
				continue
			}
			var args agentProfileUpdate
			if err := core.DecodeJSON(message.Tool.Args, &args); err != nil {
				t.Fatal(err)
			}
			if args.Avatar == nil || args.Avatar.TaskID == "" {
				continue
			}
			raw, err := s.readAvatarSource(ctx, a.ID, *args.Avatar)
			if err != nil {
				t.Fatal(err)
			}
			normalized, err := assistant.NormalizeAvatar(raw)
			if err != nil || normalized != d.Avatar {
				t.Fatal("saved avatar does not match task artifact", err)
			}
			imported = true
		}
		if !imported {
			t.Fatal("no successful task-avatar import during result handling")
		}
	}
	if output := os.Getenv("POT_LIVE_OUTPUT"); output != "" {
		if err = os.MkdirAll(filepath.Join(output, t.Name()), 0700); err != nil {
			t.Fatal(err)
		}
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(d.Avatar, "data:image/png;base64,"))
		if err = os.WriteFile(filepath.Join(output, t.Name(), "avatar.png"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		evidence, _ := json.MarshalIndent(map[string]any{"model": profile.Model, "protocol": provider.Protocol, "requests": requests.Load(), "name": d.Name, "revision": d.Revision, "messages": final.Messages, "background": background}, "", "  ")
		if err = os.WriteFile(filepath.Join(output, t.Name(), "evidence.json"), evidence, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("model=%s protocol=%s requests=%d profile=%s revision=%d avatar=128x128 PNG", profile.Model, provider.Protocol, requests.Load(), d.Name, d.Revision)
}
