package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/rollout"
	"github.com/jjyr/pot/pkg/storage"
)

type rolloutLiveServices struct {
	core.ExecutionServices
	requests *atomic.Int32
}

func (s rolloutLiveServices) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	var limits struct {
		MaxTokens     uint64 `json:"max_tokens"`
		MaxOutput     uint64 `json:"max_output_tokens"`
		MaxCompletion uint64 `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal(r.Body, &limits); err != nil {
		return nil, err
	}
	if max(limits.MaxTokens, limits.MaxOutput, limits.MaxCompletion) == 0 || limits.MaxTokens > 768 || limits.MaxOutput > 768 || limits.MaxCompletion > 768 {
		return nil, fmt.Errorf("live rollout output cap exceeded")
	}
	if s.requests.Add(1) > 8 {
		return nil, fmt.Errorf("live rollout 8-request cap exceeded")
	}
	return s.ExecutionServices.Model(ctx, r)
}

// Explicit paid opt-in: real configured model, production Control/Conversation,
// temporary storage only. No task tools are enabled in this conversation test.
func TestConfiguredModelSessionRolloutCompactResumeLive(t *testing.T) {
	if os.Getenv("POT_SESSION_ROLLOUT_LIVE") != "1" {
		t.Skip("requires POT_SESSION_ROLLOUT_LIVE=1 and configured POT_LIVE model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	profile, provider, credential := notificationLiveModel(t, ctx)
	manager, err := execution.New("rollout-live", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	root := t.TempDir()
	s, err := Open(Options{Context: ctx, Root: root, RuntimeID: "rollout-live", BootID: "boot", Workspace: filepath.Join(root, "workspace"), WorkspaceHome: t.TempDir(), Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		s.Close()
		manager.Stop()
		drain, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := manager.Drain(drain); err != nil {
			t.Error(err)
		}
	}()
	if _, err = legacyMainAssistant(ctx, s, "create", "Rollout live qualification"); err != nil {
		t.Fatal(err)
	}
	none := "none"
	saved, err := s.Profiles.Save(ctx, SaveProfile{RequestID: "profile", ProfileID: "rollout-live", Name: "Rollout qualification", Model: profile.Model, Endpoint: provider.Endpoint, Protocol: provider.Protocol, APIKey: string(credential), MaxOutputTokens: 768, ReasoningEffort: &none})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.Profiles.Resolve(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{}
	threshold := uint64(4096)
	spec.Params.Agent.BuiltinTools = []string{}
	spec.Params.Agent.AllowedTools = &allowed
	spec.Params.Agent.Plugins = map[string]core.PluginConfig{}
	spec.Params.Agent.AutoCompact = true
	spec.Params.Agent.CompactAt = &threshold
	spec.Params.Agent.UseProviderUsage = false
	spec.Params.Agent.RequestTimeoutMS = 60000
	if err = spec.Rehash(); err != nil {
		t.Fatal(err)
	}
	marker := "ROLL-" + storage.ID()
	welcome := "The most important durable fact is the exact recovery token " + marker + ". Preserve this token in every summary and remember it for later questions. The following background is expendable and can be summarized in one sentence.\n" + strings.Repeat("Ordinary background: this is redundant reference material with no action or unique facts.\n", 700)
	path := filepath.Join(root, "qualification.sqlite")
	journal := filepath.Join(root, "sessions", "qualification.jsonl")
	var requests atomic.Int32
	var c *assistant.Conversation
	options := assistant.ConversationOptions{Context: ctx, Owner: "main", RuntimeID: "rollout-live", BootID: "boot", Manager: manager, InitialSpec: &spec, InitialWelcome: welcome, MaxRequests: 8, MaxTurnRequests: 3, Prepare: func(ctx context.Context, thread core.Thread, r core.TurnRequest) (assistant.RunOptions, error) {
		prepared, err := s.prepare(ctx, c, thread, r)
		if err == nil {
			prepared.Services = rolloutLiveServices{prepared.Services, &requests}
		}
		return prepared, err
	}}
	c, err = assistant.OpenConversation(path, options)
	if err != nil {
		t.Fatal(err)
	}
	s.conversations["main"] = c
	send := func(id, prompt string) protocol.Snapshot {
		t.Helper()
		if strings.Contains(prompt, marker) {
			t.Fatal("marker leaked into recall prompt")
		}
		if _, err := c.Send(ctx, protocol.SendMessage{RequestID: id, ConversationID: "main", Text: prompt}); err != nil {
			t.Fatal(err)
		}
		updates, remove, err := c.Subscribe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer remove()
		for {
			select {
			case <-ctx.Done():
				t.Fatal("live rollout deadline reached")
			case snapshot, ok := <-updates:
				if !ok {
					t.Fatal("conversation stream closed")
				}
				if snapshot.Turn == nil {
					if snapshot.Agent.Detail != nil {
						t.Fatal("live rollout failed", *snapshot.Agent.Detail)
					}
					return snapshot
				}
			}
		}
	}
	answer := func(snap protocol.Snapshot) string {
		for i := len(snap.Messages) - 1; i >= 0; i-- {
			if snap.Messages[i].Role == protocol.Assistant {
				return snap.Messages[i].Text
			}
		}
		return ""
	}
	first := send("ack", "Remember the durable recovery token from our earlier context. Reply only ACK; do not repeat the token yet.")
	if !strings.Contains(strings.ToUpper(answer(first)), "ACK") {
		t.Fatal("initial acknowledgement failed")
	}
	beforeCalls := requests.Load()
	second := send("recall-after-compact", "What is the exact durable recovery token from earlier? Reply only the token.")
	compactions := 0
	for _, message := range second.Messages {
		if message.Tool != nil {
			t.Fatal("unexpected tool in no-tool fixture")
		}
		if message.Compaction != nil && message.Compaction.State == "complete" {
			compactions++
			if message.Compaction.After >= message.Compaction.Before {
				t.Fatal("compaction did not reduce estimate")
			}
		}
	}
	if compactions == 0 || requests.Load() < beforeCalls+2 {
		t.Fatal("actual compaction model call was not observed", compactions, requests.Load())
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	delete(s.conversations, "main")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.LoadContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	if err = json.Unmarshal(raw, &checkpoint); err != nil {
		t.Fatal(err)
	}
	summary := false
	summaryCount := 0
	for _, message := range checkpoint.Messages {
		if message.Kind.Type == "summary" {
			summaryCount++
			summary = summary || strings.Contains(message.Text, marker)
		}
	}
	t.Logf("compact recall diagnostic: answer=%q expected_marker=%q checkpoint_marker=%t summaries=%d summary_marker=%t", answer(second), marker, bytes.Contains(raw, []byte(marker)), summaryCount, summary)
	if !strings.Contains(answer(second), marker) {
		t.Fatal("real model lost marker after compaction; persisted summary diagnostic above distinguishes summary omission from recall failure")
	}
	if !summary {
		t.Fatal("committed rollout lacks real compact summary containing marker")
	}
	var ref rollout.Ref
	if err = store.DB().QueryRow("SELECT sequence,hash FROM context_rollout WHERE singleton=1").Scan(&ref.Sequence, &ref.Hash); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if int64(len(lines)) != ref.Sequence {
		t.Fatal("JSONL reference/line mismatch")
	}
	deltas := 0
	for _, line := range lines {
		var record map[string]json.RawMessage
		if err = json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["delta"] != nil {
			deltas++
		}
	}
	if deltas == 0 {
		t.Fatal("no persisted context deltas")
	}
	if err = store.CompressContext(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.Stat(journal + ".zst")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.LoadContext(ctx)
	if err != nil || !bytes.Equal(raw, restored) {
		t.Fatal("compressed load changed committed context", err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("expected compressed-only journal", err)
	}
	options.InitialWelcome = ""
	options.BootID = "boot-resumed"
	c, err = assistant.OpenConversation(path, options)
	if err != nil {
		t.Fatal(err)
	}
	s.conversations["main"] = c
	third := send("recall-after-reopen", "State the durable recovery token we established earlier. Reply only that token.")
	if !strings.Contains(answer(third), marker) {
		t.Fatal("real model lost marker after zstd/reopen")
	}
	if _, err = os.Stat(journal); err != nil {
		t.Fatal("resumed append did not restore JSONL", err)
	}
	if _, err = os.Stat(journal + ".zst"); !os.IsNotExist(err) {
		t.Fatal("stale compressed representation remains", err)
	}
	totalCompactions := 0
	for _, message := range third.Messages {
		if message.Tool != nil {
			t.Fatal("unexpected tool after resume in no-tool fixture")
		}
		if message.Compaction != nil && message.Compaction.State == "complete" {
			totalCompactions++
		}
	}
	if totalCompactions < compactions {
		t.Fatal("resume lost completed compaction records")
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	delete(s.conversations, "main")
	store, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var finalRef rollout.Ref
	if err = store.DB().QueryRow("SELECT sequence,hash FROM context_rollout WHERE singleton=1").Scan(&finalRef.Sequence, &finalRef.Hash); err != nil {
		t.Fatal(err)
	}
	if finalRef.Sequence <= ref.Sequence || finalRef.Hash == ref.Hash {
		t.Fatal("resumed model turn did not advance committed rollout reference")
	}
	finalRaw, err := store.LoadContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var finalCheckpoint agent.Checkpoint
	if err = json.Unmarshal(finalRaw, &finalCheckpoint); err != nil {
		t.Fatal(err)
	}
	if finalCheckpoint.LastRequest != "recall-after-reopen" || finalCheckpoint.Started != nil || finalCheckpoint.ModelRequestID != nil || len(finalCheckpoint.Pending) != 0 {
		t.Fatal("resumed checkpoint is not durably settled")
	}
	t.Logf("model=%s requests=%d successful_compactions_before_resume=%d successful_compactions_total=%d records_before_resume=%d records_after_resume=%d deltas_before_resume=%d jsonl_bytes=%d zstd_bytes=%d recall_after_compact=true recall_after_reopen=true", profile.Model, requests.Load(), compactions, totalCompactions, ref.Sequence, finalRef.Sequence, deltas, len(data), compressed.Size())
}
