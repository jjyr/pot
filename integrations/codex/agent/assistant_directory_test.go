package agent

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Fault only the post-delete directory response, after the real receiver has
// committed its retirement. No fixture execution worker or model is started.
func TestCodexBrowserDeletionRetiresCachedDirectoryWhenRefreshFails(t *testing.T) {
	testCodexBrowserDirectoryRetirement(t, "refresh-failure")
}

func TestCodexBrowserDeletionFencesDirectoryCapturedByOlderSync(t *testing.T) {
	testCodexBrowserDirectoryRetirement(t, "late-directory")
}

func TestCodexBrowserDeletionFencesOlderSyncFailure(t *testing.T) {
	testCodexBrowserDirectoryRetirement(t, "late-failure")
}

func TestLocalBrowserDeletionKeepsDirectoriesConsistent(t *testing.T) {
	h, app, _ := profileApplication(t)
	ctx := context.Background()
	owner, err := app.Control.Registry.Create(ctx, "local-observer", "Observer")
	if err != nil {
		t.Fatal(err)
	}
	victim, err := app.Control.Registry.Create(ctx, "local-retirement", "Retire locally")
	if err != nil {
		t.Fatal(err)
	}
	handler, cookie := profileBrowserHandler(t, app)
	remote := agentlink.AssistantTarget(h.backend.nodeID, "codex")
	expected := []protocol.AgentRef{agentlink.AssistantTarget(app.RuntimeID, victim.ID), remote}
	check := func() {
		t.Helper()
		assertBrowserAssistantDirectory(t, app, handler, cookie, owner.ID, expected, nil)
	}
	check()
	w := browserDeleteAssistant(t, handler, cookie, victim.ID, victim.Revision+1)
	if w.Code < 400 || !strings.Contains(w.Body.String(), "assistant_changed") {
		t.Fatal("local stale deletion returned success", w.Code)
	}
	check()
	w = browserDeleteAssistant(t, handler, cookie, victim.ID, victim.Revision)
	if w.Code != 200 {
		t.Fatal("local management deletion failed", w.Code)
	}
	deleted, err := app.Control.Registry.Deleted(ctx, victim.ID)
	if err != nil || !deleted {
		t.Fatal("local browser success did not retire the definition", deleted, err)
	}
	expected = []protocol.AgentRef{remote}
	check()
	// Repeating the original browser request cannot restore its ID or select
	// a replacement assistant, even if capability admission rejects the retry.
	w = browserDeleteAssistant(t, handler, cookie, victim.ID, victim.Revision)
	if w.Code == 200 && !strings.Contains(w.Body.String(), `"deleted":true`) {
		t.Fatal("local retry reported an invalid success")
	}
	check()
	restartDirectoryControl(t, app, owner.ID)
	check()
}

func testCodexBrowserDirectoryRetirement(t *testing.T, scenario string) {
	delayed := scenario != "refresh-failure"
	var retired atomic.Bool
	var hold atomic.Bool
	var rejectDelete atomic.Bool
	captured, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	h, app, linkedOwner := profileApplication(t, func(server *peer.Server) {
		authorize := server.Authorize
		server.Authorize = func(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
			handler, err := authorize(ctx, remote, service, auth)
			return func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
				if request.Method == "assistant.delete" && rejectDelete.Load() {
					return nil, agentlink.ErrUnavailable
				}
				if request.Method == "directory" && retired.Load() && !delayed {
					return nil, agentlink.ErrUnavailable
				}
				result, err := handler(ctx, request)
				if request.Method == "directory" && hold.CompareAndSwap(true, false) {
					close(captured)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					if scenario == "late-failure" {
						return nil, agentlink.ErrUnavailable
					}
				}
				if request.Method == "assistant.delete" && err == nil {
					retired.Store(true)
				}
				return result, err
			}, err
		}
	})
	ctx := context.Background()
	local, err := app.Control.Registry.Create(ctx, "directory-owner", "Local owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.backend.ownerConfigure(ctx, assistant{ID: "survivor", Name: "Survivor", Workspace: t.TempDir(), Mode: "read-only"}); err != nil {
		t.Fatal(err)
	}
	// A real catalogue refresh includes an assistant with no chat.read capability.
	linkID := strings.Split(linkedOwner, ":")[1]
	if _, err = app.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "reconnect", ID: linkID}); err != nil {
		t.Fatal(err)
	}
	// A second real node advertises the same assistant ID. Retirement must be
	// scoped to the complete receiver address, never the ID or display name.
	other, _ := testHost(t)
	t.Cleanup(other.close)
	listener, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", other.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	invite, err := other.links.Invite(ctx, "tcp://"+listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "connect", Address: invite.Address}); err != nil {
		t.Fatal(err)
	}
	handler, cookie := profileBrowserHandler(t, app)
	selected := agentlink.AssistantTarget(h.backend.nodeID, "codex")
	survivor := agentlink.AssistantTarget(h.backend.nodeID, "survivor")
	sameID := agentlink.AssistantTarget(other.backend.nodeID, "codex")
	expected := []protocol.AgentRef{selected, survivor, sameID}
	offline := map[string]bool{}
	lookup := func() []protocol.Completion {
		t.Helper()
		return assertBrowserAssistantDirectory(t, app, handler, cookie, local.ID, expected, offline)
	}
	if len(lookup()) != 3 {
		t.Fatal("fixture must first expose all real receiver identities")
	}
	a, err := h.backend.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	// A rejected deletion must not publish success or prune any consumer.
	w := browserDeleteAssistant(t, handler, cookie, linkedOwner, a.Revision+1)
	if w.Code < 400 || !strings.Contains(w.Body.String(), "assistant_changed") {
		t.Fatal("stale deletion did not return failure", w.Code)
	}
	lookup()
	rejectDelete.Store(true)
	w = browserDeleteAssistant(t, handler, cookie, linkedOwner, a.Revision)
	if w.Code < 400 {
		t.Fatal("unconfirmed remote deletion returned success", w.Code)
	}
	rejectDelete.Store(false)
	lookup()
	if _, err := h.backend.resolve(ctx, "codex"); err != nil {
		t.Fatal("a rejected/unconfirmed deletion retired the real receiver", err)
	}
	var synced chan error
	if delayed {
		hold.Store(true)
		synced = make(chan error, 1)
		go func() {
			_, err := app.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "reconnect", ID: linkID})
			synced <- err
		}()
		select {
		case <-captured:
		case <-time.After(10 * time.Second):
			t.Fatal("old sync did not capture a directory")
		}
	}
	w = browserDeleteAssistant(t, handler, cookie, linkedOwner, a.Revision)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var deleted bool
	if err = h.tasks.QueryRow("SELECT deleted FROM codex_link_assistants WHERE id='codex'").Scan(&deleted); err != nil || !deleted {
		t.Fatal("browser success did not retire the actual remote definition", deleted, err)
	}
	expected = []protocol.AgentRef{survivor, sameID}
	lookup() // Immediately after the receipt, before releasing the old Sync.
	if delayed {
		release <- struct{}{}
		select {
		case err = <-synced:
			if (err != nil) != (scenario == "late-failure") {
				t.Fatal("unexpected sync result", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("old sync did not finish")
		}
	}
	items := lookup()
	if len(items) != 2 {
		tokens := []string{}
		for _, item := range items {
			tokens = append(tokens, item.Token)
		}
		t.Fatal("successful remote deletion left a selectable cached identity", tokens)
	}
	// Reopen the materialized catalogue separately from the running Control.
	// Its persisted snapshot must already omit the acknowledged retirement.
	root := app.Control.Registry.Directory(local.ID)
	db, err := storage.OpenDatabase(filepath.Join(filepath.Dir(filepath.Dir(root)), "control.sqlite"), storage.ControlDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var metadata []byte
	if err = db.QueryRow("SELECT metadata FROM agent_link_connections WHERE id=?", linkID).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var connection agentlink.Connection
	if err = json.Unmarshal(metadata, &connection); err != nil || connection.Directory == nil || len(connection.Directory.Assistants()) != 1 || connection.Directory.DefaultTarget != nil {
		t.Fatal("retirement was not persisted in the directory/default", err)
	}
	// Reinitializing the production adapter must preserve the tombstone and
	// must not recreate its initial codex identity or lose the survivor.
	reopened, err := newBackend(h.tasks, h.backend.nodeID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory, err := reopened.Directory(ctx)
	if err != nil || len(directory.Assistants()) != 1 || directory.Assistants()[0].Address.AgentID != "survivor" {
		t.Fatal("adapter initialization resurrected the retired identity", err)
	}
	restartDirectoryControl(t, app, local.ID)
	lookup()             // Rehydrate all consumers from storage with a fresh epoch map.
	retired.Store(false) // Restore directory reads, then exercise reconnect.
	if _, err = app.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "reconnect", ID: linkID}); err != nil {
		t.Fatal(err)
	}
	if items = lookup(); len(items) != 2 {
		t.Fatal("reconnect changed the active survivor")
	}
	if _, err = app.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "disconnect", ID: linkID}); err != nil {
		t.Fatal(err)
	}
	offline[h.backend.nodeID] = true
	if items = lookup(); len(items) != 2 {
		t.Fatal("offline state hid an undeleted assistant or restored a retired one")
	}
}

func restartDirectoryControl(t *testing.T, app *transport.Application, owner string) {
	t.Helper()
	root := filepath.Dir(filepath.Dir(app.Control.Registry.Directory(owner)))
	if err := app.Control.Close(); err != nil {
		t.Fatal(err)
	}
	boot := "directory-restart"
	manager, err := execution.New(app.RuntimeID, boot, execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	// profileApplication uses this isolated, deterministic in-memory test key.
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := control.Open(control.Options{Root: root, RuntimeID: app.RuntimeID, BootID: boot, Keys: keys, Manager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	app.Control, app.BootID = reopened, boot
}

func browserDeleteAssistant(t *testing.T, handler http.Handler, cookie *http.Cookie, owner string, revision uint64) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"revision":` + strconv.FormatUint(revision, 10) + `}`)
	r := httptest.NewRequest("POST", "http://localhost/api/v1/assistants/"+url.PathEscape(owner)+"/delete?assistant_id="+url.QueryEscape(owner)+"&space_id=local", body)
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

// Exercise the actual product-tool query and the browser APIs that supply
// Chat/Task completions and local/linked UI directory projections. Capabilities
// may select different views, but cannot supply additional identities.
func assertBrowserAssistantDirectory(t *testing.T, app *transport.Application, handler http.Handler, cookie *http.Cookie, owner string, expected []protocol.AgentRef, offline map[string]bool) []protocol.Completion {
	t.Helper()
	read := func(path string, target any) {
		t.Helper()
		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		r := httptest.NewRequest("GET", "http://localhost"+path+separator+"assistant_id="+url.QueryEscape(owner)+"&space_id=local", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), target) != nil {
			t.Fatal("directory browser read failed", path, w.Code)
		}
	}
	want, online := map[protocol.AgentRef]bool{}, map[protocol.AgentRef]bool{}
	for _, ref := range expected {
		want[ref] = true
		if !offline[ref.NodeID] {
			online[ref] = true
		}
	}
	self := agentlink.AssistantTarget(app.RuntimeID, owner)
	listed, err := app.Control.ListAssistants(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tool := map[protocol.AgentRef]bool{}
	for _, assistant := range listed {
		if assistant.Address != self {
			tool[assistant.Address] = true
		}
	}
	if !maps.Equal(tool, online) {
		t.Fatal("product tool address catalogue differs", tool, online)
	}
	var completions struct{ Items []protocol.Completion }
	read("/api/v1/completions?q=%40", &completions)
	mentions := map[protocol.AgentRef]bool{}
	for _, item := range completions.Items {
		parts := strings.SplitN(strings.TrimPrefix(item.Token, "@assistant:"), "/", 2)
		if len(parts) != 2 || item.Offline != offline[parts[0]] {
			t.Fatal("completion lost its address or offline state", item.Token)
		}
		mentions[agentlink.AssistantTarget(parts[0], parts[1])] = true
	}
	if !maps.Equal(mentions, want) {
		t.Fatal("completion address catalogue differs", mentions, want)
	}
	var locals []struct{ ID string }
	read("/api/v1/assistants", &locals)
	ui := map[protocol.AgentRef]bool{}
	for _, assistant := range locals {
		ref := agentlink.AssistantTarget(app.RuntimeID, assistant.ID)
		if ref != self {
			ui[ref] = true
		}
	}
	var links struct {
		Items []struct {
			Assistants []struct{ Address protocol.AgentRef }
			Directory  *agentlink.Directory
		}
	}
	read("/api/v1/agent-links", &links)
	for _, link := range links.Items {
		projected, saved := map[protocol.AgentRef]bool{}, map[protocol.AgentRef]bool{}
		for _, assistant := range link.Assistants {
			projected[assistant.Address] = true
			ui[assistant.Address] = true
		}
		if link.Directory != nil {
			for _, assistant := range link.Directory.Assistants() {
				saved[assistant.Address] = true
			}
		}
		if !maps.Equal(projected, saved) {
			t.Fatal("UI projection differs from the saved receiver directory", projected, saved)
		}
	}
	if !maps.Equal(ui, want) {
		t.Fatal("UI address catalogue differs", ui, want)
	}
	return completions.Items
}
