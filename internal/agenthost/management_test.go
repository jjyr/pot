package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func managementValue(value string) *string { return &value }

func managementCatalog(t *testing.T, b *backend) agentlink.AssistantManagementResult {
	t.Helper()
	v, e := b.ManageAssistant(t.Context(), agentlink.AssistantManagementRequest{From: protocol.AgentRef{NodeID: "caller", Kind: "user", AgentID: "operator"}, Action: "catalog"})
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestManagementCreatesAssistantsWithSymlinkedHostRoot(t *testing.T) {
	for _, adapter := range []Runtime{Codex(), Antigravity()} {
		t.Run(adapter.Kind, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(t.TempDir(), "host")
			if err = os.Symlink(root, alias); err != nil {
				t.Fatal(err)
			}
			db, err := storage.OpenDatabase(filepath.Join(alias, "tasks.sqlite"), storage.TasksDatabase)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			adapter.Binary = fakeBinary
			if adapter.Kind == "antigravity" {
				adapter.Binary = fakeAgyBinary
			}
			b, err := newBackend(db, "symlink-root-test", alias, adapter)
			if err != nil {
				t.Fatal(err)
			}
			catalog := managementCatalog(t, b)
			if len(catalog.Workspaces) != 1 {
				t.Fatal(catalog.Workspaces)
			}
			request := agentlink.AssistantManagementRequest{
				From:   protocol.AgentRef{NodeID: "caller", Kind: "user", AgentID: "operator"},
				Action: "create", RequestID: "create", ID: "review", Name: managementValue("Review"),
				Model: managementValue("model-one"), Workspace: catalog.Workspaces[0].ID,
			}
			created, err := b.ManageAssistant(t.Context(), request)
			if err != nil || created.Assistant == nil {
				t.Fatal("host-root alias made its advertised workspace unusable", created, err)
			}
			workspace := filepath.Join(root, "workspace")
			assistant, err := b.resolve(t.Context(), request.ID)
			if err != nil || assistant.Workspace != workspace || b.root != root {
				t.Fatal("host did not register the resolved workspace", assistant, b.root, err)
			}
			// Replacing the registered directory with a symlink must still fail.
			if err = os.Rename(workspace, workspace+"-moved"); err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(workspace+"-moved", workspace); err != nil {
				t.Fatal(err)
			}
			request.ID, request.RequestID = "replacement", "replacement"
			if _, err = b.ManageAssistant(t.Context(), request); !errors.Is(err, agentlink.ErrWorkspaceUnavailable) {
				t.Fatal("replaced workspace accepted", err)
			}
		})
	}
}

func TestManagementContractAcrossRuntimes(t *testing.T) {
	for _, kind := range []string{"codex", "antigravity"} {
		t.Run(kind, func(t *testing.T) {
			b := testBackend(t)
			if kind == "antigravity" {
				b = agyBackend(t)
			}
			catalog := managementCatalog(t, b)
			if catalog.SchemaVersion != 1 || catalog.Runtime.Kind != kind || len(catalog.Models) != 2 || len(catalog.Workspaces) != 1 {
				t.Fatal(catalog)
			}
			scope := catalog.Workspaces[0].ID
			if scope == kind || filepath.IsAbs(scope) {
				t.Fatal("scope aliases assistant or path", scope)
			}
			directory, e := b.Directory(t.Context())
			if e != nil || !directory.Capabilities(agentlink.NodeTarget(b.nodeID)).Supports(agentlink.AssistantManagement) {
				t.Fatal(directory, e)
			}
			r := agentlink.AssistantManagementRequest{From: protocol.AgentRef{NodeID: "caller", Kind: "user", AgentID: "operator"}, Action: "create", RequestID: "create", ID: "same-name", Name: managementValue("Review"), Model: managementValue(catalog.Models[0].ID), Workspace: scope}
			// A barrier begins concurrent submissions; no worker or model is started.
			start := make(chan struct{})
			var wg sync.WaitGroup
			results := make(chan agentlink.AssistantManagementResult, 8)
			errs := make(chan error, 8)
			second, e := newBackend(b.db, b.nodeID, b.root, b.runtime())
			if e != nil {
				t.Fatal(e)
			}
			second.binary = b.binary
			for i := range 8 {
				wg.Go(func() {
					<-start
					receiver := b
					if i%2 == 1 {
						receiver = second
					}
					v, e := receiver.ManageAssistant(t.Context(), r)
					results <- v
					errs <- e
				})
			}
			close(start)
			wg.Wait()
			close(results)
			close(errs)
			for e := range errs {
				if e != nil {
					t.Fatal(e)
				}
			}
			var result agentlink.AssistantManagementResult
			for v := range results {
				if v.Assistant == nil || v.Assistant.Address != agentlink.AssistantTarget(b.nodeID, r.ID) {
					t.Fatal(v)
				}
				result = v
			}
			var count int
			b.db.QueryRow("SELECT COUNT(*) FROM agent_adapter_assistants WHERE id=?", r.ID).Scan(&count)
			if count != 1 {
				t.Fatal(count)
			}
			var model, effort, thread string
			b.db.QueryRow("SELECT model,effort FROM agent_adapter_executor WHERE singleton=1").Scan(&model, &effort)
			b.db.QueryRow("SELECT thread_id FROM agent_adapter_assistants WHERE id=?", r.ID).Scan(&thread)
			b.db.QueryRow("SELECT COUNT(*) FROM agent_adapter_jobs").Scan(&count)
			if model != "" || effort != "" || thread != "" || count != 0 {
				t.Fatal("creation executed work or changed executor", model, effort, thread, count)
			}
			changed := r
			changed.Name = managementValue("Different")
			if _, e = b.ManageAssistant(t.Context(), changed); !errors.Is(e, agentlink.ErrConflict) {
				t.Fatal(e)
			}
			changed = r
			changed.Effort = managementValue("")
			if _, e = b.ManageAssistant(t.Context(), changed); !errors.Is(e, agentlink.ErrConflict) {
				t.Fatal("omitted and explicit empty effort share an idempotency payload", e)
			}
			// Lost-response recovery survives unavailable catalogs/scopes and a restart.
			originalBinary := b.binary
			b.binary = "/missing-native-metadata"
			b.db.Exec("DELETE FROM agent_adapter_workspaces WHERE id=?", scope)
			retry, e := b.ManageAssistant(t.Context(), r)
			if e != nil || encoded(retry) != encoded(result) {
				t.Fatal(retry, e)
			}
			b.binary = originalBinary
			b.db.Exec("INSERT INTO agent_adapter_workspaces VALUES(?,?,?)", scope, filepath.Join(b.root, "workspace"), "read-only")
			for _, workspace := range []string{kind, "/etc", "unknown-scope"} {
				bad := r
				bad.ID = "bad"
				bad.RequestID = "bad-" + fmt.Sprint(len(workspace))
				bad.Workspace = workspace
				if _, e = b.ManageAssistant(t.Context(), bad); !errors.Is(e, agentlink.ErrWorkspaceUnavailable) {
					t.Fatal(workspace, e)
				}
			}
			bad := r
			bad.ID = "unknown-model"
			bad.RequestID = "unknown-model"
			bad.Model = managementValue("missing")
			if _, e = b.ManageAssistant(t.Context(), bad); !errors.Is(e, agentlink.ErrModelUnavailable) {
				t.Fatal(e)
			}
			bad = r
			bad.ID = "bad-effort"
			bad.RequestID = "bad-effort"
			invalidEffort := "invented"
			bad.Effort = &invalidEffort
			if _, e = b.ManageAssistant(t.Context(), bad); !errors.Is(e, agentlink.ErrInvalid) && !errors.Is(e, agentlink.ErrUnsupported) {
				t.Fatal(e)
			}
			for _, caller := range []string{"assistant", "task"} {
				bad = r
				bad.From.Kind = caller
				if _, e = b.ManageAssistant(t.Context(), bad); !errors.Is(e, agentlink.ErrUnauthorized) {
					t.Fatal(e)
				}
			}
			// Inbox admission retains work while configuration saves for the next execution.
			envelope := protocol.Envelope{ID: "busy", Kind: "message", From: r.From, To: result.Assistant.Address, ConversationID: "123e4567-e89b-42d3-a456-426614174000", Text: "queued"}
			mailbox := hostMailbox{db: b.db, backend: b, local: envelope.To}
			if _, e = mailbox.Deliver(t.Context(), envelope); e != nil {
				t.Fatal(e)
			}
			update := agentlink.AssistantManagementRequest{From: r.From, Action: "update", RequestID: "rename", ID: r.ID, Name: managementValue("Renamed"), Model: r.Model, Revision: result.Assistant.Revision}
			renamed, e := b.ManageAssistant(t.Context(), update)
			if e != nil {
				t.Fatal(e)
			}
			update.RequestID = "stale"
			if _, e = b.ManageAssistant(t.Context(), update); !errors.Is(e, agentlink.ErrAssistantChanged) {
				t.Fatal(e)
			}
			update.RequestID = "busy-model"
			update.Revision = renamed.Assistant.Revision
			update.Model = managementValue("model-two")
			saved, e := b.ManageAssistant(t.Context(), update)
			if e != nil || saved.Assistant.Model != "model-two" {
				t.Fatal(saved, e)
			}
			if e = b.DeleteAssistant(t.Context(), agentlink.DeleteAssistantRequest{From: r.From, AssistantID: r.ID, Revision: saved.Assistant.Revision}); !errors.Is(e, agentlink.ErrAssistantBusy) {
				t.Fatal(e)
			}
			b.db.Exec("UPDATE agent_messages SET state='processed' WHERE id='busy'")
			saved, e = b.ManageAssistant(t.Context(), update)
			if e != nil || saved.Assistant.Revision <= renamed.Assistant.Revision {
				t.Fatal(saved, e)
			}
			// A committed update retry wins over its now-stale revision.
			retry, e = b.ManageAssistant(t.Context(), update)
			if e != nil || encoded(saved) != encoded(retry) {
				t.Fatal(retry, e)
			}
			concurrent := make(chan struct{})
			nextErrors := make(chan error, 4)
			for i := range 4 {
				wg.Go(func() {
					<-concurrent
					next := r
					next.ID = fmt.Sprint("independent-", i)
					next.RequestID = next.ID
					_, e := b.ManageAssistant(t.Context(), next)
					nextErrors <- e
				})
			}
			close(concurrent)
			wg.Wait()
			close(nextErrors)
			for e := range nextErrors {
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = b.DeleteAssistant(t.Context(), agentlink.DeleteAssistantRequest{From: r.From, AssistantID: r.ID, Revision: saved.Assistant.Revision}); e != nil {
				t.Fatal(e)
			}
			var retired bool
			if e = b.db.QueryRow("SELECT deleted FROM agent_adapter_assistants WHERE id=?", r.ID).Scan(&retired); e != nil || !retired {
				t.Fatal("retirement was not persisted", retired, e)
			}
			reopened, e := newBackend(b.db, b.nodeID, b.root, b.runtime())
			if e != nil {
				t.Fatal(e)
			}
			reopened.binary = b.binary
			after := managementCatalog(t, reopened)
			if len(after.Assistants) != (len(catalog.Assistants) + 4) {
				t.Fatal("directory lost identities on reopen", len(after.Assistants))
			}
			retry, e = reopened.ManageAssistant(t.Context(), r)
			if e != nil || encoded(retry) != encoded(result) {
				t.Fatal("reopen lost original receipt", retry, e)
			}
		})
	}
}

func TestBrowserManagementKeepsCommittedIdentityWhenDirectoryFails(t *testing.T) {
	var fail atomic.Bool
	h, app, owner := profileApplication(t, func(server *peer.Server) {
		authorize := server.Authorize
		server.Authorize = func(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
			handler, e := authorize(ctx, remote, service, auth)
			return func(ctx context.Context, r peer.Request) (json.RawMessage, error) {
				if r.Method == "directory" && fail.Load() {
					return nil, agentlink.ErrUnavailable
				}
				out, e := handler(ctx, r)
				if r.Method == "assistant.management" && strings.Contains(string(r.Params), `"action":"create"`) && e == nil {
					fail.Store(true)
				}
				return out, e
			}, e
		}
	})
	h.backend.binary = fakeBinary
	handler, cookie := profileBrowserHandler(t, app)
	link := strings.Split(owner, ":")[1]
	body := map[string]any{"connection_id": link, "action": "create", "request_id": "browser-create", "id": "review", "name": "Review", "model": "model-one", "effort": "high", "workspace": scopeID(filepath.Join(h.backend.root, "workspace"), "read-only"), "from": map[string]string{"node_id": "forged", "kind": "assistant", "agent_id": "model"}}
	call := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/agent-links/assistants", strings.NewReader(encoded(body)))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	first := call()
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var value agentlink.AssistantManagementResult
	if json.Unmarshal(first.Body.Bytes(), &value) != nil || value.Assistant == nil || value.Assistant.Address != agentlink.AssistantTarget(h.backend.nodeID, "review") || value.Assistant.Effort != "high" {
		t.Fatal(first.Body.String())
	}
	second := call()
	if second.Code != 200 || second.Body.String() != first.Body.String() {
		t.Fatal("retry changed committed result", second.Code, second.Body.String())
	}
	var count int
	h.backend.db.QueryRow("SELECT COUNT(*) FROM agent_adapter_assistants WHERE id='review'").Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	var callerNode, callerKind, callerAgent string
	if e := h.backend.db.QueryRow("SELECT json_extract(payload,'$.from.node_id'),json_extract(payload,'$.from.kind'),json_extract(payload,'$.from.agent_id') FROM agent_adapter_management WHERE request_id='browser-create'").Scan(&callerNode, &callerKind, &callerAgent); e != nil || callerNode != app.RuntimeID || callerKind != "user" || callerAgent != "operator" {
		t.Fatal("untrusted From survived authentication binding", callerNode, callerKind, callerAgent, e)
	}
	// Verify optional-field presence through the actual HTTP and Agent Link path.
	delete(body, "workspace")
	delete(body, "effort")
	delete(body, "model")
	body["action"] = "update"
	body["request_id"] = "rename-with-omitted-effort"
	body["revision"] = value.Assistant.Revision
	body["name"] = "Updated"
	h.backend.binary = "/missing-metadata-for-presentation-edit"
	renamed := call()
	var renamedValue agentlink.AssistantManagementResult
	if renamed.Code != 200 || json.Unmarshal(renamed.Body.Bytes(), &renamedValue) != nil || renamedValue.Assistant == nil || renamedValue.Assistant.Effort != "high" {
		t.Fatal("HTTP omission cleared receiver effort", renamed.Code, renamed.Body.String())
	}
	h.backend.binary = fakeBinary
	body["request_id"] = "explicit-default-effort"
	body["revision"] = renamedValue.Assistant.Revision
	body["effort"] = ""
	delete(body, "name")
	cleared := call()
	var clearedValue agentlink.AssistantManagementResult
	if cleared.Code != 200 || json.Unmarshal(cleared.Body.Bytes(), &clearedValue) != nil || clearedValue.Assistant == nil || clearedValue.Assistant.Effort != "" {
		t.Fatal("HTTP explicit default was lost", cleared.Code, cleared.Body.String())
	}

}

func TestManagedCodexSessionAppliesItsOwnModelAndEffort(t *testing.T) {
	workspace := t.TempDir()
	worker, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	result := runNativeTurn(t.Context(), Session{Adapter: "codex", Binary: fakeBinary, Workspace: workspace, Mode: "read-only", Model: "model-one", Effort: "high", Worker: []string{worker}}, "fixture metadata", turnEvents{})
	if result.Status != "completed" {
		t.Fatal(result)
	}
	raw, e := os.ReadFile(filepath.Join(workspace, "native-settings.json"))
	if e != nil {
		t.Fatal(e)
	}
	var settings map[string]string
	if json.Unmarshal(raw, &settings) != nil || settings["model"] != "model-one" || settings["effort"] != "high" {
		t.Fatal(string(raw))
	}
}

func TestBrowserManagementRecoversLostMutationResponseWithOriginalRequest(t *testing.T) {
	var dropped atomic.Bool
	h, app, owner := profileApplication(t, func(server *peer.Server) {
		authorize := server.Authorize
		server.Authorize = func(ctx context.Context, remote peer.Identity, service string, auth json.RawMessage) (peer.Handler, error) {
			handler, e := authorize(ctx, remote, service, auth)
			return func(ctx context.Context, r peer.Request) (json.RawMessage, error) {
				result, e := handler(ctx, r)
				if e == nil && r.Method == "assistant.management" && strings.Contains(string(r.Params), `"action":"create"`) && !dropped.Swap(true) {
					return nil, agentlink.ErrUnavailable
				}
				return result, e
			}, e
		}
	})
	h.backend.binary = fakeBinary
	handler, cookie := profileBrowserHandler(t, app)
	link := strings.Split(owner, ":")[1]
	body := map[string]any{"connection_id": link, "action": "create", "request_id": "lost-response", "id": "independent", "name": "Review", "model": "model-one", "workspace": scopeID(filepath.Join(h.backend.root, "workspace"), "read-only")}
	call := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/agent-links/assistants", strings.NewReader(encoded(body)))
		request.AddCookie(cookie)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := call()
	if first.Code == 200 || !dropped.Load() {
		t.Fatal("lost response was not injected", first.Code)
	}
	// The model source can disappear after the durable mutation. The retry still
	// resolves its receipt before discovery and does not create another identity.
	h.backend.binary = "/missing-metadata-after-commit"
	retry := call()
	var result agentlink.AssistantManagementResult
	if retry.Code != 200 || json.Unmarshal(retry.Body.Bytes(), &result) != nil || result.Assistant == nil || result.Assistant.Address != agentlink.AssistantTarget(h.backend.nodeID, "independent") {
		t.Fatal(retry.Code, retry.Body.String())
	}
	body["name"] = "Changed payload"
	conflict := call()
	if conflict.Code == 200 || !strings.Contains(conflict.Body.String(), agentlink.ErrConflict.Error()) {
		t.Fatal(conflict.Code, conflict.Body.String())
	}
	var count int
	if e := h.backend.db.QueryRow("SELECT COUNT(*) FROM agent_adapter_assistants WHERE id='independent'").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

func TestManagementOmittedEffortPreservesOwnerConfiguration(t *testing.T) {
	for _, kind := range []string{"codex", "antigravity"} {
		t.Run(kind, func(t *testing.T) {
			b := testBackend(t)
			if kind == "antigravity" {
				b = agyBackend(t)
			}
			if e := b.ownerConfigure(t.Context(), assistant{ID: "owner-configured", Name: "Owner preset", Model: "model-one", Effort: "high", Workspace: filepath.Join(b.root, "workspace"), Mode: "read-only"}); e != nil {
				t.Fatal(e)
			}
			current, e := b.resolve(t.Context(), "owner-configured")
			if e != nil {
				t.Fatal(e)
			}
			originalBinary := b.binary
			b.binary = "/missing-metadata-for-safe-rename"
			request := agentlink.AssistantManagementRequest{From: protocol.AgentRef{NodeID: "caller", Kind: "user", AgentID: "operator"}, Action: "update", RequestID: "name-only", ID: current.ID, Name: managementValue("Renamed"), Model: managementValue(current.Model), Revision: current.Revision}
			renamed, e := b.ManageAssistant(t.Context(), request)
			if e != nil || renamed.Assistant.Effort != "high" {
				t.Fatal("omitted field cleared owner effort", renamed, e)
			}
			b.binary = originalBinary
			empty := ""
			request.Effort = &empty
			request.RequestID = "explicit-default"
			request.Revision = renamed.Assistant.Revision
			saved, e := b.ManageAssistant(t.Context(), request)
			if kind == "codex" {
				if e != nil || saved.Assistant.Effort != "" {
					t.Fatal("supported explicit default was not applied", saved, e)
				}
			} else {
				if !errors.Is(e, agentlink.ErrUnsupported) {
					t.Fatal("unadvertised effort was accepted", e)
				}
				after, e := b.resolve(t.Context(), current.ID)
				if e != nil || after.Effort != "high" {
					t.Fatal("unsupported field changed owner config", after, e)
				}
				if _, e = b.db.Exec("UPDATE agent_adapter_assistants SET effort='' WHERE id=?", current.ID); e != nil {
					t.Fatal(e)
				}
				request.RequestID = "unsupported-even-if-unchanged"
				if _, e = b.ManageAssistant(t.Context(), request); !errors.Is(e, agentlink.ErrUnsupported) {
					t.Fatal("unsupported explicit empty effort accepted as a no-op", e)
				}
			}
		})
	}
}
