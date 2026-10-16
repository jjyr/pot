package assistant

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func configChange(t *testing.T, id string, generation, revision uint64, spec core.ResolvedSpec) ConfigurationChange {
	t.Helper()
	intent, _ := json.Marshal(map[string]string{"model": spec.Model})
	hash, _ := core.HashJSON([]any{generation, revision, json.RawMessage(intent)})
	return ConfigurationChange{RequestID: id, InputHash: hash, ExpectedGeneration: generation, ExpectedRevision: revision, Spec: spec, Intent: intent}
}
func TestPendingConfigurationPinsRunningTurnAndReconcilesReceipt(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	var calls, cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "conversation.sqlite"), "boot", func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			if !strings.Contains(string(r.Body), `"model":"model"`) {
				t.Error("active turn changed model")
			}
		}
		return replySSE("done"), nil
	}, &cleanup)
	request := protocol.SendMessage{RequestID: "turn-one", ConversationID: "main", Text: "first"}
	if _, err := c.Send(ctx, request); err != nil {
		t.Fatal(err)
	}
	<-started
	next := resolved(t, "next-model")
	next.Params.Agent.BuiltinTools = []string{}
	next.Params.Agent.AutoCompact = false
	next.Rehash()
	change := configChange(t, "configuration", 1, 0, next)
	saved, err := c.QueueConfiguration(ctx, change)
	if err != nil || !saved.Pending || saved.Generation != 1 || saved.Config.Model != "model" || saved.Revision != 1 {
		t.Fatalf("pending configuration: %+v %v", saved, err)
	}
	duplicate, err := c.QueueConfiguration(ctx, change)
	if err != nil || duplicate.Revision != 1 {
		t.Fatalf("receipt replay: %+v %v", duplicate, err)
	}
	stale := change
	stale.RequestID = "stale"
	if _, err = c.QueueConfiguration(ctx, stale); err == nil {
		t.Fatal("stale config revision accepted")
	}
	conflict := change
	conflict.InputHash = strings.Repeat("a", 64)
	if _, err = c.QueueConfiguration(ctx, conflict); err == nil {
		t.Fatal("request identity reused with different input")
	}
	close(release)
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if thread, _ := c.Thread(ctx); thread.Generation != 1 || thread.Config.Model != "model" {
		t.Fatal("completion rewrote immutable active configuration")
	}
	if _, err = c.Send(ctx, request); err != nil {
		t.Fatal(err)
	}
	if thread, _ := c.Thread(ctx); thread.Generation != 1 {
		t.Fatal("duplicate input applied pending configuration")
	}
	if _, err = c.Send(ctx, protocol.SendMessage{RequestID: "turn-two", ConversationID: "main", Text: "second"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	thread, _ := c.Thread(ctx)
	if thread.Generation != 2 || thread.Config.Model != "next-model" {
		t.Fatalf("fresh boundary did not apply pending configuration: %+v", thread)
	}
	state, _ := c.Configuration(ctx)
	if state.Pending || state.Revision != 1 {
		t.Fatal(state)
	}
}
func TestPausedConfigurationSurvivesRestartAndResumeUsesOriginal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	var cleanup atomic.Int64
	c := conversationFixture(t, path, "old-boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		return nil, protocol.APIError{Code: "pot_token_limit", Message: "waiting"}
	}, &cleanup)
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "paused", ConversationID: "main", Text: "original"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool {
		return s.Turn != nil && s.Agent.Detail != nil && strings.Contains(*s.Agent.Detail, "Waiting")
	})
	next := resolved(t, "after-pause")
	next.Params.Agent.BuiltinTools = []string{}
	next.Params.Agent.AutoCompact = false
	next.Rehash()
	change := configChange(t, "pending", 1, 0, next)
	if _, err := c.QueueConfiguration(ctx, change); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c = conversationFixture(t, path, "new-boot", func(_ context.Context, r core.ModelRequest) (io.ReadCloser, error) {
		if !strings.Contains(string(r.Body), `"model":"model"`) {
			t.Error("resume adopted pending model")
		}
		return replySSE("resumed"), nil
	}, &cleanup)
	if state, _ := c.Configuration(ctx); !state.Pending || state.Config.Model != "model" {
		t.Fatal("pending configuration lost on restart", state)
	}
	if err := c.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	if thread, _ := c.Thread(ctx); thread.Config.Model != "model" {
		t.Fatal("paused spec was overwritten")
	}
	duplicate, err := c.QueueConfiguration(ctx, change)
	if err != nil || duplicate.Revision != 1 {
		t.Fatal("configuration receipt lost on restart", duplicate, err)
	}
}
func TestMetadataRefreshRunsOnlyAtFreshTurnBoundary(t *testing.T) {
	ctx := context.Background()
	var cleanup, calls, window atomic.Int64
	window.Store(64000)
	c := conversationFixture(t, filepath.Join(t.TempDir(), "conversation.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) { return replySSE("done"), nil }, &cleanup)
	c.options.RefreshConfiguration = func(_ context.Context, current core.Thread, _ json.RawMessage) (*core.ResolvedSpec, error) {
		calls.Add(1)
		copy := current.Config
		copy.ContextWindow = uint64(window.Load())
		copy.Params.Agent.ContextWindow = copy.ContextWindow
		copy.Rehash()
		return &copy, nil
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "first", ConversationID: "main", Text: "first"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	first, _ := c.Thread(ctx)
	if first.Config.ContextWindow != 64000 {
		t.Fatal(first)
	}
	window.Store(128000)
	if state, _ := c.Thread(ctx); state.Config.ContextWindow != 64000 || calls.Load() != 1 {
		t.Fatal("metadata updated an idle/running spec without fresh input")
	}
	if _, err := c.Send(ctx, protocol.SendMessage{RequestID: "second", ConversationID: "main", Text: "second"}); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil })
	second, _ := c.Thread(ctx)
	if second.Config.ContextWindow != 128000 || first.Config.ContextWindow != 64000 || second.Generation != first.Generation+1 || calls.Load() != 2 {
		t.Fatal("fresh boundary refresh failed", first, second, calls.Load())
	}
}
