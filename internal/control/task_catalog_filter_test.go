package control

import (
	"context"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

func TestTaskCatalogResolvesLinkedDirectoryOwnerWithoutCreatingSessions(t *testing.T) {
	s := taskControl(t, "local", "")
	ctx := context.Background()
	for _, link := range []string{"link-a", "link-b"} {
		session := linkChatID(link, "same")
		if _, err := s.integrationDB.ExecContext(ctx, "INSERT INTO agent_link_chat_sessions(link_id,assistant_id,id) VALUES(?,?,?)", link, "same", session); err != nil {
			t.Fatal(err)
		}
		record := tasks.Record{AssistantID: session, Request: protocol.CreateTask{RequestID: link, Text: "Synthetic finished work"}, Info: protocol.TaskInfo{ID: link + "-catalogue-hash", AssistantID: session, Title: link, Phase: protocol.TaskComplete, Address: &protocol.AgentRef{NodeID: link, Kind: "task", AgentID: "real-remote-task"}, AgentLink: &protocol.AgentLinkOrigin{LinkID: link, NodeID: link, AssistantID: "same"}}, Execution: &tasks.Execution{}}
		if err := s.Tasks.AcceptExecution(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range []string{"link-a", "link-b"} {
		directoryKey := "agent-link:" + link + ":same"
		page, err := s.TaskPage(ctx, "", protocol.TaskQuery{Scope: "complete", AssistantID: directoryKey})
		if err != nil || page.Total != 1 || page.Counts.Complete != 1 || len(page.Items) != 1 || page.Items[0].AssistantID != directoryKey || page.Items[0].ID != link+"-catalogue-hash" || page.Items[0].Address.AgentID != "real-remote-task" {
			t.Fatal(page, err)
		}
	}
	page, err := s.TaskPage(ctx, "", protocol.TaskQuery{Scope: "complete", AssistantID: "agent-link:missing:same"})
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
	var count int
	if err = s.integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_link_chat_sessions").Scan(&count); err != nil || count != 2 {
		t.Fatal("read created a session", count, err)
	}
}
