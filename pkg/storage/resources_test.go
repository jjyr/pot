package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestResourceReplyReopenAndUnreadWithoutMarkdown(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	if err := Migrate(ctx, db, ConversationDatabase); err != nil {
		t.Fatal(err)
	}
	original := `{"id":"earlier","conversation_id":"main","role":"user","text":"keep this history","status":"complete","created_at_ms":1,"request_id":null}`
	execMigrationSQL(t, db, `INSERT INTO messages(id,body) VALUES('earlier','`+original+`')`)
	for range 2 {
		if err := Migrate(ctx, db, ConversationDatabase); err != nil {
			t.Fatal(err)
		}
	}
	store := &Store{db: db}
	file := core.ResourceFile{Path: "report.txt", MIMEType: "text/plain", Size: 3, Digest: strings.Repeat("a", 64)}
	message := protocol.Message{ID: "resource-reply", ConversationID: "main", Role: protocol.Assistant, Status: protocol.Complete, CreatedAtMS: 2, Presentation: &core.ResourcePresentation{Mode: "preview", Caption: "Report", Resource: core.Resource{ID: "resource", Name: "report.txt", MIMEType: "text/plain", Size: 3, Entry: file.Path, Files: []core.ResourceFile{file}, Download: file}}}
	for range 2 {
		if _, err := store.Save(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	count, err := store.NotificationCount(ctx)
	if err != nil || count != 1 {
		t.Fatal("resource-only unread count or retry", count, err)
	}
	last, err := store.LastCompletedReply(ctx)
	if err != nil || last == nil || last.Presentation == nil || last.ID != message.ID {
		t.Fatal("resource-only latest reply", err)
	}
	prior, err := store.Message(ctx, "earlier")
	if err != nil || prior.Text != "keep this history" {
		t.Fatal("reopen lost earlier history", err)
	}
}
