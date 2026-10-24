package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestAssistantMailAcceptsDuringRecipientCompaction(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"complete", "failed", "restart"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 20*time.Second))
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var handled atomic.Int32
			s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				body := string(raw)
				if strings.Contains(body, "pot_compact_request") {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					if outcome == "failed" {
						http.Error(w, "controlled compact failure", 400)
						return
					}
					batchReply(w, "Preserve completed work and authorized pending work.")
					return
				}
				if strings.Contains(body, "mail-one") {
					if !strings.Contains(body, "mail-two") || strings.Index(body, "mail-one") > strings.Index(body, "mail-two") {
						t.Error("mail batch order lost")
					}
					handled.Add(1)
					batchReply(w, "mail handled")
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": strings.Repeat("observed ", 1000)}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 16000, "completion_tokens": 1000, "prompt_cache_hit_tokens": 2048, "prompt_tokens_details": map[string]int{"cached_tokens": 2048}}})
				fmt.Fprint(w, "data: "+string(event)+"\n\ndata: [DONE]\n\n")
			}, 2)
			c, _ := s.Conversation(ctx, "main")
			thread, _ := c.Thread(ctx)
			spec := thread.Config
			spec.Params.Agent.AutoCompact = true
			spec.Rehash()
			if err := c.Configure(ctx, spec, thread.Generation); err != nil {
				t.Fatal(err)
			}
			for i := range 4 {
				if _, err := c.Send(ctx, protocol.SendMessage{RequestID: fmt.Sprint("warm-", i), ConversationID: "main", Text: strings.Repeat("goal ", 1000)}); err != nil {
					t.Fatal(err)
				}
				awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil })
			}
			db, err := storage.OpenDatabase(filepath.Join(s.options.Root, "assistants", "main", "conversation.sqlite"), storage.ConversationDatabase)
			if err != nil {
				t.Fatal(err)
			}
			anchor := time.Now().Add(-27 * time.Minute / 2)
			_, err = db.Exec("UPDATE assistant_idle_compaction SET started_at=?,finished_at=? WHERE singleton=1", anchor.UnixMilli(), anchor.UnixMilli())
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err = c.CheckIdleCompaction(ctx, time.Now()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				thread, _ := c.Thread(ctx)
				p, _ := c.Snapshot(ctx)
				t.Fatalf("compact did not start auto=%v turn=%+v error=%v", thread.Config.Params.Agent.AutoCompact, p.Turn, p.Agent)
			}
			peer, err := s.Registry.Create(ctx, "peer", "Peer")
			if err != nil {
				t.Fatal(err)
			}
			send := s.productDispatch(peer.ID, "sender-user-turn", 1, false)["send_message"]
			receipts := make([]protocol.Envelope, 2)
			for i, text := range []string{"mail-one", "mail-two"} {
				args, _ := json.Marshal(protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, Text: text})
				call := modelprotocol.ToolCall{ID: fmt.Sprint("mail-", i), Name: "send_message", Arguments: args}
				var results []json.RawMessage
				var wg sync.WaitGroup
				var mu sync.Mutex
				// The same stable request may arrive concurrently; only one inbox item wins.
				for range 2 {
					wg.Go(func() {
						raw, e := send(ctx, core.ExecutionContext{Owner: peer.ID}, call, nil)
						if e != nil {
							t.Error(e)
							return
						}
						mu.Lock()
						results = append(results, raw)
						mu.Unlock()
					})
				}
				wg.Wait()
				if len(results) != 2 || string(results[0]) != string(results[1]) {
					t.Fatal("duplicate acceptance differs", results)
				}
				if err = json.Unmarshal(results[0], &receipts[i]); err != nil {
					t.Fatal(err)
				}
			}
			history, err := s.Mailbox.History(ctx, "main", 0)
			if err != nil || len(history) != 2 || history[0].State != "queued" || history[1].State != "queued" || handled.Load() != 0 {
				t.Fatal("compact blocked ingress or consumed early", history, err, handled.Load())
			}
			if history[0].Message.ID != receipts[0].ID || history[1].Message.ID != receipts[1].ID {
				t.Fatal("receipt/inbox mismatch")
			}
			if outcome == "restart" {
				if err = s.Close(); err != nil {
					t.Fatal(err)
				}
				manager, e := execution.New("local", "reboot", execution.DefaultLimits())
				if e != nil {
					t.Fatal(e)
				}
				options := s.options
				options.Manager = manager
				options.BootID = "reboot"
				s, err = Open(options)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				defer manager.Stop()
				c, _ = s.Conversation(ctx, "main")
			}
			unblock()
			sub := s.Mailbox.Changes.Subscribe()
			defer sub.Close()
			for {
				history, err = s.Mailbox.History(ctx, "main", 0)
				if err != nil {
					t.Fatal(err)
				}
				if len(history) == 2 && history[0].State == "processed" && history[1].State == "processed" {
					break
				}
				if _, err = sub.Next(ctx); err != nil {
					t.Fatal("queued mail not recovered", history, err)
				}
			}
			awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil })
			if handled.Load() != 1 {
				t.Fatal("mail consumed more than once", handled.Load())
			}
		})
	}
}
