package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestRegistryRolloutCompressionBoundsAndCursor(t *testing.T) {
	ctx := context.Background()
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "control.sqlite"), "main")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := range 40 {
		id := fmt.Sprintf("id%03d", i)
		if _, err = r.db.Exec("INSERT INTO assistant_definitions(id,request_id,deleted) VALUES(?,?,?)", id, "create-"+id, i == 1); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			s, err := storage.Open(filepath.Join(r.Directory(id), "conversation.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			err = s.SaveContext(ctx, json.RawMessage(`{"type":"context","version":2,"messages":[]}`))
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	before := time.Now().Add(time.Hour)
	next, count, err := r.CompressRolloutBatch(ctx, before, "", 1, 8<<20)
	if err != nil || count != 1 || next != "id000" {
		t.Fatal(next, count, err)
	}
	next, count, err = r.CompressRolloutBatch(ctx, before, next, 1, 8<<20)
	if err != nil || count != 1 || next != "id001" {
		t.Fatal(next, count, err)
	}
	next, count, err = r.CompressRolloutBatch(ctx, before, next, 2, 8<<20)
	if err != nil || count != 0 || next != "id033" {
		t.Fatal(next, count, err)
	}
	next, count, err = r.CompressRolloutBatch(ctx, before, next, 2, 8<<20)
	if err != nil || count != 0 || next != "" {
		t.Fatal(next, count, err)
	}
}

func TestTurnFinishedSignalFollowsDurableRetirement(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		t.Run(fmt.Sprint("waiting=", waiting), func(t *testing.T) {
			ctx := context.Background()
			var calls atomic.Int32
			options := completionOptions(t, "boot", &calls)
			if waiting {
				options.Prepare = func(context.Context, core.Thread, core.TurnRequest) (RunOptions, error) {
					return RunOptions{}, protocol.APIError{Code: "pot_token_limit"}
				}
			}
			type notice struct {
				idle bool
				turn string
				err  error
			}
			signals := make(chan notice, 2)
			var c *Conversation
			options.OnTurnFinished = func() {
				raw, _, err := c.store.Get(ctx, "conversation.turn")
				signals <- notice{idle: c.turn == nil, turn: raw, err: err}
			}
			var err error
			c, err = OpenConversation(filepath.Join(t.TempDir(), "conversation.sqlite"), options)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			accepted, err := c.Send(ctx, protocol.SendMessage{RequestID: "input", ConversationID: "main", Text: "hello"})
			if err != nil {
				t.Fatal(err)
			}
			if waiting {
				waitSnapshot(t, c, func(s protocol.Snapshot) bool {
					return s.Turn != nil && s.Agent.Detail != nil && strings.Contains(*s.Agent.Detail, "Waiting")
				})
				select {
				case n := <-signals:
					t.Fatal("waiting turn emitted terminal signal", n)
				default:
				}
				if err = c.Cancel(ctx, protocol.CancelTurn{RequestID: "cancel", UserMessageID: accepted.MessageID}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case n := <-signals:
				if n.err != nil || !n.idle || n.turn != "null" {
					t.Fatal("signal preceded durable retirement", n)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("terminal signal missing")
			}
		})
	}
}
