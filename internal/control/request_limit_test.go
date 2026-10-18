package control

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestMainConversationHasNoDefaultRequestCountCutoff(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
			batchReply(w, "completed after request claims")
		case <-r.Context().Done():
		}
	}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := s.Conversation(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "long-turn", ConversationID: "main", Text: "finish normally"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	thread, err := c.Thread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the production request-count gate while the first response is held.
	// Agent continuation beyond 128 actual model rounds is covered separately.
	for i := range 130 {
		if err := c.ClaimModel(ctx, "long-turn", thread.Generation, fmt.Sprintf("claim-%d", i)); err != nil {
			t.Fatalf("request claim %d hit an implicit cutoff: %v", i+2, err)
		}
	}
	finish()
	snapshot := awaitConversation(t, c, func(snapshot protocol.Snapshot) bool {
		return snapshot.Turn == nil && len(snapshot.Messages) > 0 && snapshot.Messages[len(snapshot.Messages)-1].Text == "completed after request claims"
	})
	if snapshot.Agent.Phase != protocol.Ready || snapshot.Agent.Detail != nil {
		t.Fatal(snapshot.Agent)
	}
}
