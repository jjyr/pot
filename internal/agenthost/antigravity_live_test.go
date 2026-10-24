package agenthost

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// Opt-in: uses the installed native plugin, real agy login and paid model calls.
// All host/Pot state and workspaces are disposable. No personal chat is resumed.
func TestAntigravityLive(t *testing.T) {
	if os.Getenv("POT_AGY_LIVE") != "1" {
		t.Skip("set POT_AGY_LIVE=1 with an authenticated agy and installed Pot plugin")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	for _, address := range []string{Antigravity().Listen, Antigravity().PeerListen} {
		if c, e := net.DialTimeout("tcp", address, time.Second); e == nil {
			c.Close()
			t.Fatalf("live fixture requires idle adapter ports: %s", address)
		}
	}
	binary := filepath.Join(t.TempDir(), "pot-antigravity-agent")
	if out, e := exec.Command("go", "build", "-o", binary, "../../cmd/pot-antigravity-agent").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	liveBase, e := os.MkdirTemp("", "pot-agy-live-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("retained live fixture:", liveBase)
		} else {
			os.RemoveAll(liveBase)
		}
	})
	root := filepath.Join(liveBase, "host")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	var daemon *exec.Cmd
	start := func() {
		t.Helper()
		daemon = exec.Command(binary, "agent-serve", "--root", root)
		log, e := os.OpenFile(filepath.Join(root, "daemon-test.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		daemon.Stdout = log
		daemon.Stderr = log
		if e = daemon.Start(); e != nil {
			t.Fatal(e)
		}
		log.Close()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			c, e := existingAgentHostClient(root, Antigravity())
			if e == nil && c.healthy() {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("native daemon did not start")
	}
	stop := func() {
		if daemon != nil {
			p := daemon
			daemon = nil
			_ = p.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- p.Wait() }()
			select {
			case e := <-done:
				if e != nil {
					t.Errorf("daemon exit: %v", e)
				}
			case <-time.After(15 * time.Second):
				_ = p.Process.Kill()
				t.Error("daemon cleanup timed out")
			}
		}
	}
	defer stop()
	start()
	client, e := existingAgentHostClient(root, Antigravity())
	if e != nil {
		t.Fatal(e)
	}
	var invitation struct {
		Address string `json:"address"`
	}
	if e = client.call(ctx, "/api/v1/agent-links/invitation", map[string]string{"endpoint": "tcp://" + Antigravity().PeerListen}, &invitation); e != nil {
		t.Fatal(e)
	}
	manager, e := execution.New("agy-live-pot", "boot", execution.DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Stop()
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	pot, e := control.Open(control.Options{Context: ctx, Root: t.TempDir(), RuntimeID: "agy-live-pot", BootID: "boot", Manager: manager, Keys: keys})
	if e != nil {
		t.Fatal(e)
	}
	defer pot.Close()
	connected, e := pot.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "connect", Address: invitation.Address})
	if e != nil {
		t.Fatal(e)
	}
	link := connected.(agentlink.Connection)
	catalog, e := pot.ManageLinkedAssistant(ctx, link.ID, agentlink.AssistantManagementRequest{Action: "catalog"})
	if e != nil || len(catalog.Models) == 0 || len(catalog.Workspaces) == 0 {
		t.Fatal(catalog, e)
	}
	workspace := catalog.Workspaces[0].ID
	create := func(id, model string) {
		t.Helper()
		r := agentlink.AssistantManagementRequest{Action: "create", RequestID: "create-" + id, ID: id, Name: managementValue(id), Model: managementValue(model), Workspace: workspace}
		v, e := pot.ManageLinkedAssistant(ctx, link.ID, r)
		if e != nil || v.Assistant == nil {
			t.Fatal(v, e)
		}
		retry, e := pot.ManageLinkedAssistant(ctx, link.ID, r)
		if e != nil || retry.Assistant.ID != id {
			t.Fatal("creation retry", retry, e)
		}
		t.Logf("created assistant=%s model=%s", id, model)
	}
	create("gemini-live", "gemini-3.8-flash-low")
	create("claude-live", "claude-sonnet-5-5-low")
	ask := func(id, request, text, want string) protocol.SendMessageAccepted {
		t.Helper()
		accepted, e := pot.SendLinkChat(ctx, link.ID, id, request, text)
		if e != nil {
			t.Fatal(e)
		}
		deadline := time.Now().Add(100 * time.Second)
		for time.Now().Before(deadline) {
			frame, e := pot.LinkChat(ctx, link.ID, id)
			if e != nil {
				t.Fatal(e)
			}
			for _, m := range frame.Messages {
				if m.Role == protocol.Assistant && strings.TrimSpace(m.Text) == want {
					t.Logf("completed assistant=%s request=%s", id, request)
					return accepted
				}
			}
			for _, state := range frame.Replies {
				if state.InputID == accepted.MessageID && state.State == "failed" {
					t.Fatal("native turn failed", state)
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("missing answer %q", want)
		return accepted
	}
	if os.Getenv("POT_AGY_LIVE_REVERSE_ONLY") == "" {

		first := ask("gemini-live", "remember", "Remember this secret test word for this conversation: POT_AGY_6281. Reply exactly REMEMBERED_6281. Do not use tools.", "REMEMBERED_6281")
		ask("claude-live", "isolation", "Reply exactly CLAUDE_AGY_OK. Do not use tools.", "CLAUDE_AGY_OK")
		db, e := storage.OpenDatabaseReadOnly(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		var geminiThread, claudeThread string
		if e = db.QueryRow("SELECT thread_id FROM agent_adapter_assistants WHERE id='gemini-live'").Scan(&geminiThread); e != nil {
			t.Fatal(e)
		}
		if e = db.QueryRow("SELECT thread_id FROM agent_adapter_assistants WHERE id='claude-live'").Scan(&claudeThread); e != nil || geminiThread == "" || geminiThread == claudeThread {
			t.Fatal("session isolation", e)
		}
		stop()
		start()
		if _, e = pot.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "reconnect", ID: link.ID}); e != nil {
			t.Fatal(e)
		}
		repeated, e := pot.SendLinkChat(ctx, link.ID, "gemini-live", "remember", "Remember this secret test word for this conversation: POT_AGY_6281. Reply exactly REMEMBERED_6281. Do not use tools.")
		if e != nil || !repeated.Duplicate || repeated.MessageID != first.MessageID {
			t.Fatal("retry after restart", repeated, e)
		}
		ask("gemini-live", "recall", "What was the secret test word I asked you to remember? Reply only that word. Do not use tools.", "POT_AGY_6281")
		var after string
		db.QueryRow("SELECT thread_id FROM agent_adapter_assistants WHERE id='gemini-live'").Scan(&after)
		if after != geminiThread {
			t.Fatal("restart replaced session")
		}
		// Switch the same assistant to a different native provider, retaining context.
		current, e := pot.ManageLinkedAssistant(ctx, link.ID, agentlink.AssistantManagementRequest{Action: "catalog"})
		if e != nil {
			t.Fatal(e)
		}
		var revision uint64
		for _, a := range current.Assistants {
			if a.ID == "gemini-live" {
				revision = a.Revision
			}
		}
		_, e = pot.ManageLinkedAssistant(ctx, link.ID, agentlink.AssistantManagementRequest{Action: "update", RequestID: "switch-to-claude", ID: "gemini-live", Name: managementValue("gemini-live"), Model: managementValue("claude-sonnet-5-5-low"), Revision: revision})
		if e != nil {
			t.Fatal(e)
		}
		ask("gemini-live", "recall-after-model-change", "Repeat the secret test word from earlier in this conversation, and nothing else. Do not use tools.", "POT_AGY_6281")
	}
	db, e := storage.OpenDatabaseReadOnly(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()

	receiver, e := pot.Registry.Create(ctx, "agy-mail-recipient", "Pot mailbox")
	if e != nil {
		t.Fatal(e)
	}
	// An actual model calls the installed MCP server to send in the reverse direction.
	ask("claude-live", "reverse-mcp", fmt.Sprintf("Use the pot MCP tools to send exactly AGY_TO_POT_6281 to the Pot assistant with id %s on node agy-live-pot. First discover its connection with pot_connections, then call pot_send with a new request_id. After successful acceptance reply exactly MCP_SENT_6281.", receiver.ID), "MCP_SENT_6281")
	deadline := time.Now().Add(20 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		var count int
		e = pot.Tasks.DB().QueryRow(`SELECT COUNT(*) FROM agent_messages WHERE direction='in' AND json_extract(envelope,'$.text')='AGY_TO_POT_6281' AND json_extract(envelope,'$.to.agent_id')=?`, receiver.ID).Scan(&count)
		if e != nil {
			t.Fatal(e)
		}
		if count > 0 {
			found = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !found {
		for _, source := range []struct {
			name string
			db   *sql.DB
		}{{"Pot", pot.Tasks.DB()}, {"AGY", db}} {
			rows, _ := source.db.Query("SELECT direction,state,envelope FROM agent_messages")
			for rows.Next() {
				var direction, state, raw string
				rows.Scan(&direction, &state, &raw)
				t.Log(source.name, direction, state, raw)
			}
			rows.Close()
		}
		t.Fatal("MCP message never reached Pot")
	}
	// Pot assistant-originated mail runs automatically in the addressed AGY session.
	from := agentlink.AssistantTarget("agy-live-pot", receiver.ID)
	mail, e := pot.Mailbox.Send(ctx, "pot-to-agy-live", from, protocol.SendMail{To: agentlink.AssistantTarget(link.PeerHost.NodeID, "claude-live"), Text: "Reply exactly POT_TO_AGY_REPLY_6281. Do not use tools."}, nil)
	if e != nil {
		t.Fatal(e)
	}
	deadline = time.Now().Add(100 * time.Second)
	found = false
	for time.Now().Before(deadline) {
		var raw string
		e = pot.Tasks.DB().QueryRow(`SELECT envelope FROM agent_messages WHERE direction='in' AND json_extract(envelope,'$.reply_to')=?`, mail.ID).Scan(&raw)
		if e == nil {
			var reply protocol.Envelope
			json.Unmarshal([]byte(raw), &reply)
			if strings.Contains(reply.Text, "POT_TO_AGY_REPLY_6281") {
				found = true
				break
			}
		} else if e != sql.ErrNoRows {
			t.Fatal(e)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !found {
		t.Fatal("assistant reply missing")
	}
	if os.Getenv("POT_AGY_LIVE_REVERSE_ONLY") != "" {
		t.Log("PASS: real MCP reverse delivery and assistant-originated mail")
	} else {
		t.Log("PASS: native plugin, Gemini + Claude, user chat, model change, isolated persistent sessions, daemon restart, deduplication, MCP reverse send and assistant mail")
	}
}
