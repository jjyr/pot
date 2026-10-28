package control

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestPotMCPCancellationPersistsThroughDisconnectAndRestart(t *testing.T) {
	a, driver := potMCPControl(t, "cancel-mcp-a")
	b, _ := potMCPControl(t, "cancel-mcp-b")
	server := potMCPServer(t, a)
	connection := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, connection.ID)
	potMCPExposeClient(t, b, connection.ID, client)
	ctx := t.Context()
	target := agentlink.AssistantTarget(a.options.RuntimeID, "main")
	accepted, err := b.CreateTask(ctx, "main", protocol.CreateTask{RequestID: "cancel-offline", Text: "safe fake work", Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	b.syncTaskExecutions(ctx)
	if err = a.Tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	b.syncTaskExecutions(ctx)
	before, err := b.Tasks.Get(ctx, "main", accepted.ID)
	if err != nil || before == nil || before.Execution == nil || before.Info.Phase != protocol.TaskRunning || before.Execution.Receipt.RunID == "" {
		t.Fatal("missing actual accepted execution", before, err)
	}
	if err = b.DisconnectPotMCP(ctx, connection.ID); err != nil {
		t.Fatal(err)
	}
	if err = b.StopTask(ctx, "main", accepted.ID, "cancel"); !errors.Is(err, agentlink.ErrDisconnected) {
		t.Fatal("offline cancel must retain intent without claiming remote cleanup", err)
	}
	b.syncTaskExecutions(ctx)
	options := b.options
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	options.BootID = "cancel-mcp-restarted"
	options.Manager, err = execution.New(options.RuntimeID, options.BootID, execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	paused := make(chan struct{})
	var initialized sync.Once
	options.Maintenance = func() bool { initialized.Do(func() { close(paused) }); return true }
	restored, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restored.Close() })
	select {
	case <-paused:
	case <-time.After(2 * time.Second):
		t.Fatal("restored task loop did not pause")
	}
	stored, err := restored.Tasks.Get(ctx, "main", accepted.ID)
	if err != nil || stored == nil || stored.Execution == nil || !stored.Execution.StopRequested || stored.Execution.Receipt.ID != before.Execution.Receipt.ID || stored.Execution.Receipt.RunID != before.Execution.Receipt.RunID || stored.Info.Phase != protocol.TaskRunning || stored.Result != nil {
		t.Fatal("restart lost cancel intent or invented completion", stored, err)
	}
	// Restore only communication. The real Task/run receipt must be cancelled,
	// never replaced or launched again by a retry after the database reopen.
	if _, err = restored.integrationDB.Exec("UPDATE mcp_receiver_connections SET state='active' WHERE id=?", connection.ID); err != nil {
		t.Fatal(err)
	}
	client = potMCPClient(t, restored, connection.ID)
	potMCPExposeClient(t, restored, connection.ID, client)
	restored.syncTaskExecutions(ctx)
	if err = a.Tasks.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	restored.syncTaskExecutions(ctx)
	stored, err = restored.Tasks.Get(ctx, "main", accepted.ID)
	if err != nil || stored.Info.Phase != protocol.TaskCancelled || stored.Execution.Receipt.RunID != before.Execution.Receipt.RunID || driver.launches != 1 {
		t.Fatal("reconnected cancel lost binding or replayed work", stored, driver.launches, err)
	}
}
