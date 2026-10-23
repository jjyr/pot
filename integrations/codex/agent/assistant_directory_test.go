package agent

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/agentlink"
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

func testCodexBrowserDirectoryRetirement(t *testing.T, scenario string) {
	delayed := scenario != "refresh-failure"
	var retired atomic.Bool
	var hold atomic.Bool
	captured, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	h, app, linkedOwner := profileApplication(t, func(server *peer.Server) {
		authorize := server.Authorize
		server.Authorize = func(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
			handler, err := authorize(ctx, remote, service, auth)
			return func(ctx context.Context, request peer.Request) (json.RawMessage, error) {
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
	lookup := func() []protocol.Completion {
		t.Helper()
		page, err := app.Control.Completions(ctx, local.ID, "@")
		if err != nil {
			t.Fatal(err)
		}
		return page["items"].([]protocol.Completion)
	}
	if len(lookup()) != 2 {
		t.Fatal("fixture must first expose both real receiver identities")
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
	a, err := h.backend.resolve(ctx, "codex")
	if err != nil {
		t.Fatal(err)
	}
	handler, cookie := profileBrowserHandler(t, app)
	body := strings.NewReader(`{"revision":` + strconv.FormatUint(a.Revision, 10) + `}`)
	request := httptest.NewRequest("POST", "http://localhost/api/v1/assistants/"+url.PathEscape(linkedOwner)+"/delete?assistant_id="+url.QueryEscape(linkedOwner)+"&space_id=local", body)
	request.AddCookie(cookie)
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var deleted bool
	if err = h.tasks.QueryRow("SELECT deleted FROM codex_link_assistants WHERE id='codex'").Scan(&deleted); err != nil || !deleted {
		t.Fatal("browser success did not retire the actual remote definition", deleted, err)
	}
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
	if len(items) != 1 || items[0].Token != "@assistant:"+h.backend.nodeID+"/survivor" {
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
	retired.Store(false) // Restore directory reads, then exercise reconnect.
	if _, err = app.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "reconnect", ID: linkID}); err != nil {
		t.Fatal(err)
	}
	if items = lookup(); len(items) != 1 || items[0].Offline {
		t.Fatal("reconnect changed the active survivor")
	}
	if _, err = app.Control.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "disconnect", ID: linkID}); err != nil {
		t.Fatal(err)
	}
	if items = lookup(); len(items) != 1 || !items[0].Offline {
		t.Fatal("offline state hid an undeleted assistant or restored a retired one")
	}
}
