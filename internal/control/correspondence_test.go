package control

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestCorrespondenceUsesAssistantHistoryAndCanReportToUser(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var received atomic.Bool
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		if strings.Contains(body, "DESIGNER_REPORT") {
			received.Store(true)
			if !strings.Contains(body, "OWNER_CONTEXT") || !strings.Contains(body, "owner-context-saved") {
				t.Error("incoming mail lost the assistant's existing user conversation")
			}
			batchReply(w, "The design report is ready for your review")
		} else {
			batchReply(w, "owner-context-saved")
		}
	}, 2)
	c, _ := s.Conversation(ctx, "main")
	updates, stop, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "owner-context", ConversationID: "main", Text: "OWNER_CONTEXT: please assess the existing design"}); err != nil {
		t.Fatal(err)
	}
	for snapshot := range updates {
		if snapshot.Turn == nil && len(snapshot.Messages) > 1 {
			break
		}
	}
	if _, err := s.Mailbox.Send(ctx, "designer-report", protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "tony"}, protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, Text: "DESIGNER_REPORT: the assessment is ready"}, nil); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case snapshot := <-updates:
			if snapshot.Turn != nil {
				continue
			}
			for _, m := range snapshot.Messages {
				if m.Role == protocol.Assistant && m.Text == "The design report is ready for your review" {
					if !received.Load() {
						t.Fatal("report did not invoke the assistant")
					}
					frame, err := c.UIFrame(ctx, 0, true)
					if err != nil {
						t.Fatal(err)
					}
					if err := frame.Validate(); err != nil {
						t.Fatal(err)
					}
					found := false
					for _, input := range frame.Messages {
						if len(input.Correspondence) == 1 {
							found = input.Role == protocol.System && input.Correspondence[0].ID == "designer-report"
							page, err := c.UIHistory(ctx, 0, 0, input.ID, 20)
							if err != nil {
								t.Fatal(err)
							}
							projected := false
							for _, entry := range page.Messages {
								if entry.ID == input.ID {
									projected = len(entry.Correspondence) == 1
								}
							}
							if !projected {
								t.Fatal("history lost incoming receipt")
							}
						}
					}
					if !found {
						t.Fatal("incoming mail was not projected as reference metadata")
					}
					var sessions int
					if err := s.Tasks.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_mail_sessions").Scan(&sessions); err != nil || sessions != 0 {
						t.Fatal("correspondence created a separate model context", sessions, err)
					}
					return
				}
			}
		case <-ctx.Done():
			t.Fatal("processed correspondence never reached the owning assistant's chat", ctx.Err())
		}
	}
}
