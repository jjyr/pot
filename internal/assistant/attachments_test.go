package assistant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestAttachmentOnlySteeringDeduplicatesAndReachesNextModel(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	var calls, cleanup atomic.Int64
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.7\n%%EOF"))
	c := conversationFixture(t, filepath.Join(t.TempDir(), "conversation.sqlite"), "boot", func(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else if !strings.Contains(string(r.Body), pdf) {
			t.Error("steering PDF never reached provider", string(r.Body))
		}
		return replySSE("done"), nil
	}, &cleanup)
	c.options.ValidateAttachments = func(ctx context.Context, spec core.ResolvedSpec, refs []core.Attachment) error {
		if !spec.AttachmentCapabilities().PDF || len(refs) != 1 || refs[0].ID != "pdf" {
			return errors.New("invalid attachment")
		}
		return nil
	}
	prepare := c.options.Prepare
	c.options.Prepare = func(ctx context.Context, thread core.Thread, request core.TurnRequest) (RunOptions, error) {
		options, err := prepare(ctx, thread, request)
		options.ResolveAttachments = func(ctx context.Context, spec core.ResolvedSpec, messages []modelprotocol.Message) ([]modelprotocol.Message, error) {
			out := append([]modelprotocol.Message(nil), messages...)
			for i, message := range out {
				for _, a := range message.Attachments {
					message.Documents = append(message.Documents, modelprotocol.ModelDocument{Name: a.Name, Data: pdf})
				}
				message.Attachments = nil
				out[i] = message
			}
			return out, nil
		}
		return options, err
	}
	config, _ := c.Configuration(ctx)
	spec := *config.Config
	spec.Params.Agent.ModelCatalog = &core.ModelCatalog{SchemaVersion: 1, Default: json.RawMessage(`{"input_modalities":["text","pdf"]}`), Models: map[string]map[string]json.RawMessage{}}
	spec.Rehash()
	if err := c.Configure(ctx, spec, config.Generation); err != nil {
		t.Fatal(err)
	}
	receipt, err := c.Send(ctx, protocol.SendMessage{RequestID: "start", ConversationID: "main", Text: "start"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	guidance := SteerRequest{RequestID: "pdf-guidance", UserMessageID: receipt.MessageID, Attachments: []core.Attachment{{ID: "pdf", Name: "guide.pdf", MIMEType: "application/pdf", Size: 13}}}
	first, err := c.Steer(ctx, guidance)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := c.Steer(ctx, guidance)
	if err != nil || !duplicate.Duplicate || duplicate.MessageID != first.MessageID {
		t.Fatal(duplicate, err)
	}
	guidance.Attachments[0].Name = "changed.pdf"
	if _, err = c.Steer(ctx, guidance); err == nil {
		t.Fatal("changed steering attachment reused receipt")
	}
	close(release)
	snapshot := waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) > 2 })
	var count int
	for _, message := range snapshot.Messages {
		if message.ID == first.MessageID {
			count++
			if len(message.Attachments) != 1 || message.Attachments[0].Name != "guide.pdf" || message.Status != protocol.Complete {
				t.Fatal(message)
			}
		}
	}
	if count != 1 || calls.Load() != 2 {
		t.Fatal("guidance duplicated or was not consumed", count, calls.Load())
	}
	raw, err := c.store.LoadContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), pdf) {
		t.Fatal("steering bytes persisted in checkpoint")
	}
}

func TestRetryFailedCannotReplayUnknownModelOutcome(t *testing.T) {
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "conversation.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		calls.Add(1)
		return nil, errors.New("connection lost after sending")
	}, &cleanup)
	receipt, err := c.Send(context.Background(), protocol.SendMessage{RequestID: "original", ConversationID: "main", Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) == 2 })
	if _, err = c.RetryFailed(context.Background(), "retry", receipt.MessageID); err == nil {
		t.Fatal("uncertain model outcome replayed")
	}
	if calls.Load() != 1 {
		t.Fatal("unexpected duplicate model request", calls.Load())
	}
}
