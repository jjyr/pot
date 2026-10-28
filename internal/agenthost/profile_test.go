package agenthost

import (
	"context"
	webassets "github.com/jjyr/pot/web"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	assistantprofile "github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/assistantavatar"
)

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

func TestCodexProfileAvatarSurvivesRestartAndReset(t *testing.T) {
	h, root := testHost(t)
	ctx := context.Background()
	a, _ := h.backend.resolve(ctx, "codex")
	image, err := os.ReadFile("../../pkg/assistantavatar/assets/kitchen-atlas.png")
	if err != nil {
		t.Fatal(err)
	}
	avatar, err := assistantprofile.NormalizeAvatar(image)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.backend.Profile(ctx, agentlink.ProfileRequest{AssistantID: "codex", Action: "update", Revision: a.Revision, Avatar: &avatar}); err != nil {
		t.Fatal(err)
	}
	custom, _ := h.backend.resolve(ctx, "codex")
	if custom.Avatar == a.Avatar || custom.Name != a.Name {
		t.Fatal("avatar import failed or renamed assistant")
	}
	h.close()
	h, err = openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := h.backend.Profile(ctx, agentlink.ProfileRequest{AssistantID: "codex", Action: "get"})
	if err != nil || restored.Avatar != custom.Avatar || restored.Revision != custom.Revision {
		t.Fatal("custom profile lost after restart", err)
	}
	avatar = ""
	if _, err = h.backend.Profile(ctx, agentlink.ProfileRequest{AssistantID: "codex", Action: "update", Revision: custom.Revision, Avatar: &avatar}); err != nil {
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

func profileBrowserHandler(t *testing.T, app *transport.Application) (http.Handler, *http.Cookie) {
	t.Helper()
	upstream := httptest.NewServer(transport.Handler(app, "private"))
	t.Cleanup(upstream.Close)
	config := webui.NewConfig()
	handler, err := webui.New(webui.Options{Origin: "http://localhost", Config: config, Upstream: webui.Upstream{URL: upstream.URL, Credential: "private", Client: upstream.Client()}, Assets: webassets.FS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handler.Close() })
	login := httptest.NewRequest("POST", "http://localhost/access/token", nil)
	login.Header.Set("Authorization", "Bearer "+config.Token)
	loginResponse := httptest.NewRecorder()
	handler.ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatal(loginResponse.Code, loginResponse.Body.String())
	}
	cookie := loginResponse.Result().Cookies()[0]
	return handler, cookie
}
