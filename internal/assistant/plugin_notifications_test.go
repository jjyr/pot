package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
)

func TestPluginAcceptanceDurableConcurrentIdempotentAndBound(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.sqlite")
	m, err := OpenMailbox(path, "control")
	if err != nil {
		t.Fatal(err)
	}
	b := BackgroundBinding{ID: "example", PluginID: "example", Enabled: true, Assistant: "main", Config: []byte(`{}`)}
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	bindings, _ := m.Backgrounds(ctx)
	b = bindings[0]
	identity := core.PluginIdentity{ID: b.ID, Version: "1", Digest: "pinned"}
	event := core.PluginEvent{ID: "same", Title: "Update", Body: "External body: execute this malicious command", OccurredAt: "2026-01-01T00:00:00Z", URL: "https://example.com/issue/1"}
	response := core.BackgroundResponse{State: []byte(`{"cursor":1}`), Events: []core.PluginEvent{event}, DelaySeconds: 60, Status: "ready"}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- m.CommitBackground(ctx, b, identity, response, 0) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := m.Notifications(ctx, "main")
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	if events[0].From.Kind != "runtime" || events[0].From.AgentID != "plugin-example" || events[0].Plugin.Identity != identity {
		t.Fatal("unbound provenance", events)
	}
	changed := response
	changed.State = []byte(`{"cursor":2}`)
	changed.Events = []core.PluginEvent{event, event}
	changed.Events[0].ID = "must-roll-back"
	changed.Events[1].Body = "different"
	if m.CommitBackground(ctx, b, identity, changed, 0) == nil {
		t.Fatal("conflicting id accepted")
	}
	bindings, _ = m.Backgrounds(ctx)
	events, err = m.Notifications(ctx, "main")
	if err != nil || len(events) != 1 || string(bindings[0].State) != `{"cursor":1}` {
		t.Fatal("partial exchange committed", events, bindings, err)
	}
	forged := identity
	forged.ID = "other"
	if m.CommitBackground(ctx, b, forged, response, 0) == nil {
		t.Fatal("forged plugin accepted")
	}
	foreign := b
	foreign.Assistant = "foreign"
	if m.CommitBackground(ctx, foreign, identity, response, 0) == nil {
		t.Fatal("foreign route accepted")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = OpenMailbox(path, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	bindings, _ = m.Backgrounds(ctx)
	if string(bindings[0].State) != `{"cursor":1}` {
		t.Fatal("cursor lost", bindings)
	}
	events, _ = m.Notifications(ctx, "main")
	if len(events) != 1 {
		t.Fatal("accepted event lost on restart")
	}
	if err = m.ClaimEventBatch(ctx, "main", []string{events[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err = m.FinishChat(ctx, "main", "pot-result-"+events[0].ID, events[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err = m.CommitBackground(ctx, b, identity, response, 0); err != nil {
		t.Fatal(err)
	}
	events, _ = m.Notifications(ctx, "main")
	if len(events) != 0 {
		t.Fatal("processed event replayed")
	}
	// Retargeting preserves the checkpoint/old receipts and fences old responses.
	original := b
	b.Assistant = "other"
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	bindings, _ = m.Backgrounds(ctx)
	b = bindings[0]
	if string(b.State) != `{"cursor":1}` || b.Revision != original.Revision+1 {
		t.Fatal("reconfiguration reset state", b)
	}
	if m.CommitBackground(ctx, original, identity, response, 0) == nil {
		t.Fatal("reconfigured route accepted stale response")
	}
	if err = m.CommitBackground(ctx, b, identity, response, 0); err != nil {
		t.Fatal("retargeting broke original receipt", err)
	}
	events, err = m.Notifications(ctx, "other")
	if err != nil || len(events) != 0 {
		t.Fatal("retargeting replayed accepted event", events, err)
	}
	// A stale in-flight exchange cannot publish after the owner disables routing.
	b.Enabled = false
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	if m.CommitBackground(ctx, b, identity, response, 0) == nil {
		t.Fatal("disabled route accepted")
	}
}
func TestPluginInterruptedAttemptFencedAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.sqlite")
	m, _ := OpenMailbox(path, "control")
	b := BackgroundBinding{ID: "example", PluginID: "example", Enabled: true, Assistant: "main", Config: []byte(`{}`)}
	m.ConfigureBackground(ctx, b)
	stored, _ := m.Backgrounds(ctx)
	b = stored[0]
	r := core.BackgroundResponse{State: []byte(`null`), Events: []core.PluginEvent{{ID: "one", Title: "Update", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"}}, DelaySeconds: 60, Status: "ready"}
	if err := m.CommitBackground(ctx, b, core.PluginIdentity{ID: b.ID}, r, 0); err != nil {
		t.Fatal(err)
	}
	events, _ := m.Notifications(ctx, "main")
	if err := m.ClaimEventBatch(ctx, "main", []string{events[0].ID}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err := OpenMailbox(path, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	events, _ = m.Notifications(ctx, "main")
	if len(events) != 0 {
		t.Fatal("uncertain attempt replayed")
	}
	stored, _ = m.Backgrounds(ctx)
	if stored[0].Attention != 1 {
		t.Fatal("interruption invisible", stored)
	}
}

func TestPluginCapacityRetryAndImmediateReclaim(t *testing.T) {
	ctx := context.Background()
	m, err := OpenMailbox(filepath.Join(t.TempDir(), "mail.sqlite"), "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	b := BackgroundBinding{ID: "example", PluginID: "example", Enabled: true, Assistant: "main", Config: []byte(`{}`)}
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	bindings, err := m.Backgrounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b = bindings[0]
	identity := core.PluginIdentity{ID: b.ID}
	r := core.BackgroundResponse{State: []byte(`null`), DelaySeconds: 60, Status: "ready"}
	for batch := range 8 {
		r.Events = nil
		for n := range 8 {
			r.Events = append(r.Events, core.PluginEvent{ID: fmt.Sprint(batch*8 + n), Title: "Update", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"})
		}
		if err = m.CommitBackground(ctx, b, identity, r, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err = m.CommitBackground(ctx, b, identity, r, 0); err != nil {
		t.Fatal("full queue broke idempotent retry", err)
	}
	r.Events = []core.PluginEvent{{ID: "overflow", Title: "Update", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"}}
	if m.CommitBackground(ctx, b, identity, r, 0) == nil {
		t.Fatal("unbounded queue accepted")
	}
	events, err := m.Notifications(ctx, "main")
	if err != nil || len(events) != 8 {
		t.Fatal(events, err)
	}
	if err = m.ClaimEventBatch(ctx, "main", []string{events[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err = m.ReleaseEvent(ctx, events[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = m.ClaimEventBatch(ctx, "main", []string{events[0].ID, events[1].ID}); err != nil {
		t.Fatal("unstarted report delayed the next claim", err)
	}
}

func TestPluginStoredFormatUpgradePreservesBindingAndReceipts(t *testing.T) {
	ctx := context.Background()
	m, path := mailbox(t, "local")
	b := BackgroundBinding{ID: "example", PluginID: "example", Enabled: true, Assistant: "main", Config: []byte(`{"reasons":["mention"]}`)}
	if err := m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	bindings, _ := m.Backgrounds(ctx)
	b = bindings[0]
	checkpoint := `{"account":"123","baseline":true,"since":"cursor","pending":{"thread":{"items":["kept"]}}}`
	r := core.BackgroundResponse{State: []byte(checkpoint), DelaySeconds: 90, Status: "permission"}
	for _, id := range []string{"queued", "processed", "interrupted"} {
		r.Events = append(r.Events, core.PluginEvent{ID: id, Title: "Update", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"})
	}
	if err := m.CommitBackground(ctx, b, core.PluginIdentity{ID: b.ID, Version: "1"}, r, 100); err != nil {
		t.Fatal(err)
	}
	// Recreate only the removed fields/table from the previous PR format.
	if _, err := m.db.Exec(`DROP INDEX agent_messages_outbox_route; ALTER TABLE agent_messages DROP COLUMN accepted_at_ms; ALTER TABLE assistant_mail_identity RENAME COLUMN node_id TO control_id; ALTER TABLE assistant_contacts RENAME COLUMN node TO control; DROP TABLE task_input_digest_formats; DROP TABLE codex_mail_consumers; DROP TABLE codex_task_inputs; DROP TABLE codex_task_sessions; DROP TABLE codex_task_executor; DROP TABLE codex_link_jobs; DROP TABLE codex_link_assistants; DROP TABLE pot_migrations; DROP TABLE run_workspaces; DROP TABLE task_runs; DROP TABLE task_events; DROP TABLE retired_execution_receipts; DROP TABLE journal_file_import; DROP TABLE task_submission_import; DROP INDEX task_outcomes_run; ALTER TABLE task_outcomes DROP COLUMN run_id; DROP TABLE task_node; ALTER TABLE retired_task_authority RENAME TO task_authority; DROP TABLE plugin_background_writes; DROP INDEX agent_link_user_chat_routes; DROP INDEX agent_chat_replies; DROP INDEX agent_plugin_instance_state; ALTER TABLE plugin_background DROP COLUMN diagnostic; ALTER TABLE plugin_background DROP COLUMN plugin_id; ALTER TABLE plugin_background DROP COLUMN connection_id; CREATE TABLE plugin_delivery_rates(owner TEXT PRIMARY KEY,next INTEGER NOT NULL); INSERT INTO plugin_delivery_rates VALUES('main',9999999999999);
 UPDATE agent_messages SET envelope=json_set(envelope,'$.plugin.event.group','thread','$.text','old duplicate');
 UPDATE agent_messages SET state=json_extract(envelope,'$.plugin.event.id');`); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err := OpenMailbox(path, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	bindings, err = m.Backgrounds(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatal(bindings, err)
	}
	got := bindings[0]
	if !got.Enabled || got.Assistant != b.Assistant || got.Revision != b.Revision || string(got.Config) != string(b.Config) || string(got.State) != checkpoint || got.Next != 90100 || got.Status != "permission" || got.Queued != 1 || got.Attention != 1 {
		t.Fatal("binding or checkpoint changed", got)
	}
	events, err := m.Notifications(ctx, "main")
	if err != nil || len(events) != 1 || events[0].Text != "" || events[0].Plugin.Event.ID != "queued" {
		t.Fatal(events, err)
	}
	var removed int
	if err = m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='plugin_delivery_rates'`).Scan(&removed); err != nil || removed != 0 {
		t.Fatal(removed, err)
	}
	for _, event := range r.Events {
		id, _ := core.HashJSON([]string{"local", b.ID, event.ID})
		var state string
		if err = m.db.QueryRow("SELECT state FROM agent_messages WHERE id=?", id).Scan(&state); err != nil || state != event.ID {
			t.Fatal("receipt changed", state, err)
		}
	}
	if err = m.CommitBackground(ctx, b, core.PluginIdentity{ID: b.ID, Version: "2"}, r, 100); err != nil {
		t.Fatal("upgrade broke identical retry", err)
	}
	events, err = m.Notifications(ctx, "main")
	if err != nil || len(events) != 1 {
		t.Fatal("upgrade replayed receipts", events, err)
	}
}

func TestPluginUnstartedReportReleaseCanRetryImmediately(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	event := protocol.Envelope{Kind: "plugin_event", ID: "plugin-notice", From: protocol.AgentRef{NodeID: "local", Kind: "runtime", AgentID: "plugin-example"}, To: address("local", "main"), ConversationID: newConversation(), Plugin: &protocol.PluginNotification{InstanceID: "example", Identity: core.PluginIdentity{ID: "example"}, Event: core.PluginEvent{ID: "one", Title: "Update", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"}}}
	if err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error { return inbox.EnqueueTx(ctx, tx, event, "in", "queued") }); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "chat.sqlite"), "boot", func(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return replySSE("done"), nil
	}, &cleanup)
	defer c.Close()
	if err := m.ClaimEvent(ctx, "main", event.ID); err != nil {
		t.Fatal(err)
	}
	// User input wins after readiness/claim but before Report accepts the summary.
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "user", ConversationID: "main", Text: "user input"}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if _, err := c.Report(ctx, "pot-result-"+event.ID, "notice"); err == nil {
		t.Fatal("summary overtook user")
	}
	if err := m.ReleaseEvent(ctx, event.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if err := m.ClaimEvent(ctx, "main", event.ID); err != nil {
		t.Fatal("unstarted Report consumed summary quota", err)
	}
	if _, err := c.Report(ctx, "pot-result-"+event.ID, "notice"); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if calls.Load() != 2 {
		t.Fatal("unexpected model calls", calls.Load())
	}
}

func TestPluginNotificationBatchCountsCanonicalPayloadBytes(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	b := BackgroundBinding{ID: "example", PluginID: "example", Enabled: true, Assistant: "main", Config: []byte(`{}`)}
	if err := m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	bindings, _ := m.Backgrounds(ctx)
	b = bindings[0]
	r := core.BackgroundResponse{State: []byte(`null`), DelaySeconds: 60, Status: "ready"}
	for i := range 8 {
		r.Events = append(r.Events, core.PluginEvent{ID: fmt.Sprint(i), Title: "Escaped", Body: strings.Repeat("\x01", 9000), OccurredAt: "2026-01-01T00:00:00Z"})
	}
	if err := m.CommitBackground(ctx, b, core.PluginIdentity{ID: b.ID}, r, 0); err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, event := range r.Events {
		id, _ := core.HashJSON([]string{"local", b.ID, event.ID})
		all = append(all, id)
	}
	if err := m.ClaimEventBatch(ctx, "main", all); err == nil {
		t.Fatal("canonical payload bypassed byte limit through empty Text")
	}
	events, err := m.Notifications(ctx, "main")
	if err != nil || len(events) == 0 || len(events) >= 8 {
		t.Fatal(events, err)
	}
	var ids []string
	for _, event := range events {
		ids = append(ids, event.ID)
		if event.Text != "" {
			t.Fatal("duplicate body in envelope")
		}
	}
	if err = m.ClaimEventBatch(ctx, "main", ids); err != nil {
		t.Fatal("bounded batch could not be claimed", err)
	}
	for _, id := range ids {
		var state string
		if err = m.db.QueryRow("SELECT state FROM agent_messages WHERE id=?", id).Scan(&state); err != nil || state != "processing" {
			t.Fatal(state, err)
		}
	}
}

// Default initialization must lose to every persisted owner choice and must not
// touch checkpoint/recipient history, even when several callers initialize.
func TestPluginDefaultInitializationConcurrent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.sqlite")
	m, err := OpenMailbox(path, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	other, err := OpenMailbox(path, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		store := m
		if i%2 == 1 {
			store = other
		}
		go func() {
			defer wg.Done()
			if err := store.EnsureBackgroundInstance(ctx, BackgroundBinding{ID: "example", PluginID: "example", Enabled: true, Assistant: "first", Config: json.RawMessage(`{}`)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	bindings, err := m.Backgrounds(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].Revision != 1 || !bindings[0].Enabled || bindings[0].Assistant != "first" {
		t.Fatal(bindings, err)
	}
	b := bindings[0]
	if err = m.CommitBackground(ctx, b, core.PluginIdentity{ID: "example", Version: "1", Digest: "installed"}, core.BackgroundResponse{State: json.RawMessage(`{"account":"old","baseline":true}`), Status: "baseline", DelaySeconds: 90}, 100); err != nil {
		t.Fatal(err)
	}
	b.Enabled = false
	b.Assistant = "chosen"
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err = m.EnsureBackgroundInstance(ctx, BackgroundBinding{ID: "example", PluginID: "example", Enabled: true, Assistant: "different", Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	bindings, err = m.Backgrounds(ctx)
	if err != nil || bindings[0].Enabled || bindings[0].Assistant != "chosen" || bindings[0].Revision != 2 || !strings.Contains(string(bindings[0].State), `"baseline":true`) {
		t.Fatal(bindings, err)
	}
	if err = m.ConfigureBackground(ctx, b); err == nil {
		t.Fatal("stale draft accepted")
	}
	// An explicit off choice before any assistant exists is initialized intent.
	if err = m.ConfigureBackground(ctx, BackgroundBinding{ID: "waiting", PluginID: "waiting", Enabled: false, Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err = m.EnsureBackgroundInstance(ctx, BackgroundBinding{ID: "waiting", PluginID: "waiting", Enabled: true, Assistant: "late-assistant", Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	bindings, err = m.Backgrounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range bindings {
		if next.ID == "waiting" && (next.Enabled || next.Assistant != "" || next.Revision != 1) {
			t.Fatal(next)
		}
	}
}

func TestPluginBackgroundFailureDiagnosticSurvivesRestartAndClearsOnSuccess(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.sqlite")
	m, err := OpenMailbox(path, "control")
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ConfigureBackground(ctx, BackgroundBinding{ID: "github", PluginID: "github", Enabled: true, Assistant: "main", Config: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	bindings, _ := m.Backgrounds(ctx)
	b := bindings[0]
	if err = m.BackgroundError(ctx, b, "unavailable", "background exchange timed out", 1000); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = OpenMailbox(path, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	bindings, _ = m.Backgrounds(ctx)
	if bindings[0].Diagnostic != "background exchange timed out" || bindings[0].Next != 301000 {
		t.Fatal(bindings)
	}
	r := core.BackgroundResponse{State: []byte(`{"cursor":"preserved"}`), DelaySeconds: 60, Status: "ready"}
	if err = m.CommitBackground(ctx, b, core.PluginIdentity{ID: "github", Version: "1", Digest: "fixture"}, r, 2000); err != nil {
		t.Fatal(err)
	}
	bindings, _ = m.Backgrounds(ctx)
	if bindings[0].Diagnostic != "" || bindings[0].Status != "ready" {
		t.Fatal(bindings)
	}
}

func TestPluginInstancesIsolateIdentityQueuesAndRetargetFences(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	defer m.Close()
	for _, id := range []string{"provider-first", "provider-second"} {
		if err := m.EnsureBackgroundInstance(ctx, BackgroundBinding{ID: id, PluginID: "gateway", ConnectionID: id, Enabled: true, Assistant: "main", Config: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	bindings, err := m.Backgrounds(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatal(bindings, err)
	}
	response := core.BackgroundResponse{State: json.RawMessage(`{"episode":1}`), DelaySeconds: 60, Status: "ready", Events: []core.PluginEvent{{ID: "same-event", Title: "Allowance", Body: `{"used":95}`, OccurredAt: "2026-10-20T00:00:00Z"}}}
	identity := core.PluginIdentity{ID: "gateway", Version: "1", Digest: "installed"}
	for _, b := range bindings {
		if err = m.CommitBackground(ctx, b, identity, response, 100); err != nil {
			t.Fatal(err)
		}
	}
	bindings, err = m.Backgrounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bindings {
		if b.Queued != 1 || b.PluginID != "gateway" {
			t.Fatal(b)
		}
	}
	messages, err := m.Notifications(ctx, "main")
	if err != nil || len(messages) != 2 {
		t.Fatal(messages, err)
	}
	for _, msg := range messages {
		if msg.Plugin.Identity.ID != "gateway" || msg.From.AgentID != "plugin-"+msg.Plugin.InstanceID || msg.Plugin.ConnectionID != msg.Plugin.InstanceID {
			t.Fatal(msg)
		}
		if err = msg.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	stale := bindings[0]
	changed := stale
	changed.PluginID = "other"
	if err = m.ConfigureBackground(ctx, changed); err == nil {
		t.Fatal("package identity rebound")
	}
	changed = stale
	changed.ConnectionID = "different"
	if err = m.ConfigureBackground(ctx, changed); err == nil {
		t.Fatal("connection identity rebound")
	}
	changed = stale
	changed.Assistant = "other"
	if err = m.ConfigureBackground(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if err = m.CommitBackground(ctx, stale, identity, response, 200); err == nil {
		t.Fatal("stale route accepted")
	}
	bindings, _ = m.Backgrounds(ctx)
	changed = bindings[0]
	if err = m.CommitBackground(ctx, changed, identity, response, 200); err != nil {
		t.Fatal("identical retry after retarget failed", err)
	}
	other, err := m.Notifications(ctx, "other")
	if err != nil || len(other) != 0 {
		t.Fatal("old receipts replayed to new target", other, err)
	}
	forged := identity
	forged.ID = changed.ID
	if err = m.CommitBackground(ctx, changed, forged, response, 200); err == nil {
		t.Fatal("instance impersonated package")
	}
	changed.Enabled = false
	if err = m.ConfigureBackground(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if err = m.EnsureBackgroundInstance(ctx, BackgroundBinding{ID: changed.ID, PluginID: "gateway", ConnectionID: changed.ConnectionID, Enabled: true, Assistant: "main", Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	bindings, _ = m.Backgrounds(ctx)
	if bindings[0].Enabled {
		t.Fatal("explicit off overwritten")
	}
}

func TestBackgroundWaitingInstanceCanCommitCheckpointWithoutEvents(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	defer m.Close()
	if err := m.EnsureBackgroundInstance(ctx, BackgroundBinding{ID: "provider-waiting", PluginID: "gateway", ConnectionID: "waiting", Enabled: true, Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	bindings, err := m.Backgrounds(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatal(bindings, err)
	}
	b := bindings[0]
	identity := core.PluginIdentity{ID: "gateway"}
	response := core.BackgroundResponse{State: json.RawMessage(`{"version":1,"active":false,"episode":0}`), DelaySeconds: 60, Status: "ready"}
	if err = m.CommitBackground(ctx, b, identity, response, 100); err != nil {
		t.Fatal(err)
	}
	bindings, err = m.Backgrounds(ctx)
	if err != nil || string(bindings[0].State) != string(response.State) || bindings[0].Next != 60100 {
		t.Fatal(bindings, err)
	}
	response.Events = []core.PluginEvent{{ID: "premature", Title: "Quota", Body: `{"used":90}`, OccurredAt: "2026-10-20T00:00:00Z"}}
	if err = m.CommitBackground(ctx, b, identity, response, 200); err == nil {
		t.Fatal("event accepted without target")
	}
}

func TestBackgroundWritesFenceInflightAndRetainOpaqueProgress(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	defer m.Close()
	if err := m.EnsureBackgroundInstance(ctx, BackgroundBinding{ID: "example", PluginID: "example", Enabled: true, Assistant: "main", Config: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	bindings, _ := m.Backgrounds(ctx)
	b := bindings[0]
	identity := core.PluginIdentity{ID: "example", Version: "1", Digest: "installed"}
	checkpoint := json.RawMessage(`{"account":"123","baseline":true,"since":"2026-01-01T00:00:00Z","threads":{"123":{"marker":"retained"}},"pending":{"old":{"body":"discard"}},"scan":{"rows":[1]}}`)
	if err := m.CommitBackground(ctx, b, identity, core.BackgroundResponse{State: checkpoint, DelaySeconds: 60, Status: "ready"}, 100); err != nil {
		t.Fatal(err)
	}
	old := b
	b.Enabled = false
	if err := m.ConfigureBackgroundRequest(ctx, b, "off"); err != nil {
		t.Fatal(err)
	}
	if err := m.ConfigureBackgroundRequest(ctx, b, "off"); err != nil {
		t.Fatal("retry", err)
	}
	if err := m.ConfigureBackgroundRequest(ctx, b, "another-off"); err == nil {
		t.Fatal("stale accepted")
	}
	// A completed old poll must not enqueue or restore its pending state.
	r := core.BackgroundResponse{State: checkpoint, DelaySeconds: 60, Status: "ready", Events: []core.PluginEvent{{ID: "old-event", Title: "old", Body: "untrusted", OccurredAt: "2026-01-01T00:00:00Z"}}}
	if err := m.CommitBackground(ctx, old, identity, r, 200); err == nil {
		t.Fatal("old inflight accepted")
	}
	bindings, _ = m.Backgrounds(ctx)
	next := bindings[0]
	var state map[string]any
	json.Unmarshal(next.State, &state)
	if next.Revision != 2 || next.Enabled || state["since"] != "2026-01-01T00:00:00Z" || state["threads"].(map[string]any)["123"] == nil || len(state["pending"].(map[string]any)) != 1 || state["scan"] == nil {
		t.Fatal(next, string(next.State))
	}
	next.Enabled = true
	if err := m.ConfigureBackgroundRequest(ctx, next, "on"); err != nil {
		t.Fatal(err)
	}
	if err := m.CommitBackground(ctx, old, identity, r, 300); err == nil {
		t.Fatal("reenabling revived old response")
	}
}

// A shared scan must commit every receiver together with its checkpoint. Even
// capacity exhaustion on the last receiver rolls back the earlier enqueue.
func TestPluginFanoutAtomicBackpressureRevisionAndRetry(t *testing.T) {
	ctx := context.Background()
	m, err := OpenMailbox(filepath.Join(t.TempDir(), "mail.sqlite"), "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	b := BackgroundBinding{ID: "fanout", PluginID: "fanout", Enabled: true, Config: json.RawMessage(`{}`)}
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.Backgrounds(ctx)
	b = rows[0]
	b.EventRouting = true
	identity := core.PluginIdentity{ID: "fanout", Version: "1", Digest: "fixture"}
	event := func(id, target string) core.PluginEvent {
		return core.PluginEvent{ID: id, Assistant: target, Title: "Update", Body: "fixture", OccurredAt: "2026-01-01T00:00:00Z"}
	}
	for i := 0; i < 63; i++ {
		r := core.BackgroundResponse{State: json.RawMessage(`{"cursor":1}`), Events: []core.PluginEvent{event(fmt.Sprint("seed-", i), "B")}, DelaySeconds: 60, Status: "ready"}
		if err = m.CommitBackground(ctx, b, identity, r, 0); err != nil {
			t.Fatal(err)
		}
	}
	response := core.BackgroundResponse{State: json.RawMessage(`{"cursor":2}`), Events: []core.PluginEvent{event("new-B", "B"), event("new-C", "C")}, DelaySeconds: 60, Status: "ready"}
	if err = m.CommitBackground(ctx, b, identity, response, 0); err == nil {
		t.Fatal("capacity overflow accepted")
	}
	rows, _ = m.Backgrounds(ctx)
	B, _ := m.Notifications(ctx, "B")
	C, _ := m.Notifications(ctx, "C")
	var queued int
	if err = m.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_messages WHERE state='queued'").Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 63 || len(C) != 0 || string(rows[0].State) != `{"cursor":1}` {
		t.Fatal("partial fanout committed", len(B), len(C), string(rows[0].State))
	}
	if _, err = m.db.ExecContext(ctx, `UPDATE agent_messages SET state='processed'`); err != nil {
		t.Fatal(err)
	}
	if err = m.CommitBackground(ctx, b, identity, response, 0); err != nil {
		t.Fatal(err)
	}
	if err = m.CommitBackground(ctx, b, identity, response, 0); err != nil {
		t.Fatal(err)
	}
	B, _ = m.Notifications(ctx, "B")
	C, _ = m.Notifications(ctx, "C")
	if len(B) != 1 || len(C) != 1 {
		t.Fatal("retry duplicated or missed a receiver", len(B), len(C))
	}
	// A subscription deletion/reassignment increments the root revision. Results
	// already fetched under the old revision cannot advance the shared cursor.
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	response.State = json.RawMessage(`{"cursor":3}`)
	if err = m.CommitBackground(ctx, b, identity, response, 0); err == nil {
		t.Fatal("old in-flight revision accepted")
	}
	rows, _ = m.Backgrounds(ctx)
	if string(rows[0].State) != `{"cursor":2}` {
		t.Fatal(string(rows[0].State))
	}
}
