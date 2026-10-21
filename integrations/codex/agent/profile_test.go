package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/web"
)

func profileApplication(t *testing.T) (*agentHost, *transport.Application, string) {
	t.Helper()
	h, _ := testHost(t)
	t.Cleanup(h.close)
	ctx := context.Background()
	listener, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", h.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	invite, err := h.links.Invite(ctx, "tcp://"+listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := execution.New("profile-pot", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	s, err := control.Open(control.Options{Root: t.TempDir(), RuntimeID: "profile-pot", BootID: "boot", Keys: keys, Manager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	connected, err := s.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "connect", Address: invite.Address})
	if err != nil {
		t.Fatal(err)
	}
	return h, &transport.Application{Control: s, RuntimeID: "profile-pot", BootID: "boot"}, "agent-link:" + connected.(agentlink.Connection).ID + ":codex"
}

func TestCodexNewAssistantUsesUnusedKitchenIdentity(t *testing.T) {
	b := testBackend(t)
	ctx := context.Background()
	first, _ := b.resolve(ctx, "codex")
	workspace := t.TempDir()
	if err := b.ownerConfigure(ctx, assistant{ID: "second", Workspace: workspace, Mode: "workspace-write"}); err != nil {
		t.Fatal(err)
	}
	second, _ := b.resolve(ctx, "second")
	if !slices.Contains(assistantavatar.Names(), second.Name) || second.Name == first.Name || second.Avatar == "" {
		t.Fatal("new assistant did not use an unused kitchen identity", second.Name)
	}
	if err := b.ownerConfigure(ctx, assistant{ID: "second", Workspace: workspace, Mode: "workspace-write", Default: true}); err != nil {
		t.Fatal(err)
	}
	current, _ := b.resolve(ctx, "second")
	if current.Name != second.Name || current.Avatar != second.Avatar || current.Revision != second.Revision || !current.Default {
		t.Fatal("default selection changed saved profile")
	}
}

func TestCodexMCPAvatarImportIsBoundToAssistantWorkspaceAndSurvivesRestart(t *testing.T) {
	h, root := testHost(t)
	ctx := context.Background()
	a, _ := h.backend.resolve(ctx, "codex")
	image, err := os.ReadFile("../../../pkg/assistantavatar/assets/kitchen-atlas.png")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.Workspace, "avatar.png"), image, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(root, "tasks.sqlite"), filepath.Join(a.Workspace, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../tasks.sqlite", "escape", "/etc/passwd"} {
		if _, err = h.tool(ctx, hostToolInput{Operation: "profile", FromAssistantID: "codex", AvatarPath: path}); err == nil {
			t.Fatal("out of workspace image allowed", path)
		}
	}
	if _, err = h.tool(ctx, hostToolInput{Operation: "profile", FromAssistantID: "codex", AssistantID: "another", Name: func() *string { v := "wrong"; return &v }()}); err == nil {
		t.Fatal("MCP could edit another assistant")
	}
	if _, err = h.tool(ctx, hostToolInput{Operation: "profile", FromAssistantID: "codex", AvatarPath: "avatar.png"}); err != nil {
		t.Fatal(err)
	}
	custom, _ := h.backend.resolve(ctx, "codex")
	if custom.Avatar == a.Avatar || custom.Name != a.Name {
		t.Fatal("avatar import failed or renamed assistant")
	}
	if _, err = h.tool(ctx, hostToolInput{Operation: "profile", FromAssistantID: "codex", ResetAvatar: true}); err != nil {
		t.Fatal(err)
	}
	h.close()
	h, err = openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	current, _ := h.backend.resolve(ctx, "codex")
	if current.Avatar != a.Avatar || current.Name != a.Name || current.Character != a.Character {
		t.Fatal("profile lost after restart")
	}
}

func TestCodexProfilePeerRejectsAssistantCallersAndOwnerOperations(t *testing.T) {
	ctx := context.Background()
	a, _ := testHost(t)
	defer a.close()
	b, _ := testHost(t)
	defer b.close()
	listener, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", a.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	invite, err := a.links.Invite(ctx, "tcp://"+listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	connection, err := b.links.Connect(ctx, invite.Address)
	if err != nil {
		t.Fatal(err)
	}
	input := agentlink.ProfileRequest{From: protocol.AgentRef{NodeID: b.identity.Address.NodeID, Kind: "assistant", AgentID: "codex"}, AssistantID: "codex", Action: "get"}
	if _, err = b.links.Profile(ctx, connection.ID, input); err == nil {
		t.Fatal("assistant caller could manage peer profile")
	}
	input.From.Kind = "user"
	for _, action := range []string{"delete", "configure", "rules"} {
		input.Action = action
		if _, err = b.links.Profile(ctx, connection.ID, input); err == nil {
			t.Fatal("owner operation allowed through profile", action)
		}
	}
}

func TestCodexProfileThroughPotBrowserKeepsThreadAndWelcome(t *testing.T) {
	h, app, owner := profileApplication(t)
	ctx := context.Background()
	original, err := h.backend.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(assistantavatar.Names(), original.Name) || original.Avatar == "" {
		t.Fatal("fresh Codex identity has no character", original.Name)
	}
	if _, err = h.tasks.Exec("UPDATE codex_link_assistants SET thread_id='same-thread' WHERE id='codex'"); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(transport.Handler(app, "private"))
	defer upstream.Close()
	config := webui.NewConfig()
	handler, err := webui.New(webui.Options{Origin: "http://localhost", Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "private", Client: upstream.Client()}, Assets: webassets.FS()})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	login := httptest.NewRequest("POST", "http://localhost/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatal(loginResponse.Code, loginResponse.Body.String())
	}
	cookie := loginResponse.Result().Cookies()[0]
	call := func(method, path string, input any, status int) []byte {
		t.Helper()
		var raw []byte
		if input != nil {
			raw, _ = json.Marshal(input)
		}
		r := httptest.NewRequest(method, "http://localhost"+path+"?assistant_id="+url.QueryEscape(owner)+"&space_id=local&locale=zh-CN", bytes.NewReader(raw))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	welcome := call("GET", "/api/v1/snapshot", nil, 200)
	if !strings.Contains(string(welcome), "运行在 Codex") || !strings.Contains(string(welcome), original.Name) {
		t.Fatal(string(welcome))
	}
	read := func() agentlink.Profile {
		var p agentlink.Profile
		if err := json.Unmarshal(call("GET", "/api/v1/assistants/profile", nil, 200), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := read()
	custom, _ := assistantavatar.Render("pot", "custom")
	call("POST", "/api/v1/assistants/"+url.PathEscape(owner), map[string]any{"name": "改名助手", "avatar": custom, "revision": p.Revision}, 200)
	after := read()
	if after.Name != "改名助手" || after.Avatar != custom || after.Revision != p.Revision+1 {
		t.Fatal("profile not saved", after.Name)
	}
	// An old settings page cannot overwrite a newer MCP or UI edit.
	stale := httptest.NewRequest("POST", "http://localhost/api/v1/assistants/"+url.PathEscape(owner)+"?assistant_id="+url.QueryEscape(owner), strings.NewReader(`{"name":"stale","revision":1}`))
	stale.AddCookie(cookie)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, stale)
	if w.Code < 400 {
		t.Fatal("stale profile accepted")
	}
	var defaults agentlink.Profile
	json.Unmarshal(call("GET", "/api/v1/assistants/default-avatar", nil, 200), &defaults)
	if defaults.Avatar != original.Avatar {
		t.Fatal("rename changed assigned character")
	}
	call("POST", "/api/v1/assistants/"+url.PathEscape(owner), map[string]any{"avatar": "", "revision": after.Revision}, 200)
	if read().Avatar != original.Avatar {
		t.Fatal("reset did not restore original avatar")
	}
	current, _ := h.backend.resolve(ctx, "codex")
	if current.ThreadID != "same-thread" || current.Workspace != original.Workspace || current.Mode != original.Mode {
		t.Fatal("profile edit changed execution scope")
	}
	if !bytes.Equal(welcome, call("GET", "/api/v1/snapshot", nil, 200)) {
		t.Fatal("welcome was rewritten after rename or duplicated")
	}
	var count int
	h.tasks.QueryRow("SELECT count(*) FROM codex_link_jobs").Scan(&count)
	if count != 0 {
		t.Fatal("welcome or profile edit invoked Codex")
	}
	items, err := app.Control.AgentLinks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(items)
	if !strings.Contains(string(raw), "改名助手") || !strings.Contains(string(raw), original.Avatar) {
		t.Fatal("directory cache did not refresh")
	}
}

func TestCodexProfileBrowserFixture(t *testing.T) {
	file := os.Getenv("POT_CODEX_PROFILE_FIXTURE_URL_FILE")
	if file == "" {
		t.Skip("opt-in browser fixture")
	}
	_, app, _ := profileApplication(t)
	upstream := httptest.NewServer(transport.Handler(app, "private"))
	defer upstream.Close()
	repo, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	config := webui.NewConfig()
	browser, err := webui.New(webui.Options{Origin: origin, Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "private", Client: upstream.Client()}, Assets: os.DirFS(repo)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 1)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fixture/stop" {
			select {
			case done <- struct{}{}:
			default:
			}
			return
		}
		// This opt-in loopback fixture grants only its disposable browser session.
		// All product routes still pass through browser authentication and Allowed.
		if r.URL.Path == "/fixture/login" && r.Method == http.MethodPost {
			login := httptest.NewRequest(http.MethodPost, origin+"/access/token", nil)
			login.Header.Set("Authorization", "Bearer "+config.Token)
			browser.ServeHTTP(w, login)
			return
		}
		browser.ServeHTTP(w, r)
	})
	server.Start()
	defer server.Close()
	defer browser.Close()
	if err = os.WriteFile(file, []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(20 * time.Minute):
		t.Fatal("fixture expired")
	}
}

func TestCodexSubscriptionPersistsFirstWelcomeLocale(t *testing.T) {
	_, app, owner := profileApplication(t)
	ctx := context.Background()
	sub, err := app.Control.SubscribeAgent(ctx, owner, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first, err := sub.Frame(ctx, 0, true)
	if err != nil || len(first.Messages) != 1 || !strings.Contains(first.Messages[0].Text, "Codex") || !strings.Contains(first.Messages[0].Text, "我") {
		t.Fatal(first, err)
	}
	again, err := app.Control.CallAgent(ctx, owner, agentlink.ChatRead, agentlink.Call{Query: map[string]string{"locale": "en"}})
	if err != nil {
		t.Fatal(err)
	}
	saved := again.(protocol.UIFrame)
	if len(saved.Messages) != 1 || saved.Messages[0].Text != first.Messages[0].Text || saved.Cursor != first.Cursor || len(saved.Replies) != 0 {
		t.Fatal("welcome was duplicated, relocalized or treated as execution", saved)
	}
}
