package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveTransportRecordsCredentialFreeNativeChunksAndHonorsBudget(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer private-fixture-key" {
			t.Error("missing credential")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["thinking"].(map[string]any)["type"] != "disabled" {
			t.Error("thinking policy missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"completion_tokens_details":{"reasoning_tokens":2}}}`+"\n\n")
	}))
	defer server.Close()
	s := SmallScenario("native-fixture", []string{"Reply exactly done."}, false)
	text := "done"
	s.ExpectedFinalText = &text
	c := &DeepSeekConnection{endpoint: server.URL, key: "private-fixture-key", model: s.Profile.Model, contextWindow: 32768, maxOutput: 128}
	limits := LiveLimits{1, 10000}
	transport, err := c.configure(&s, limits)
	if err != nil {
		t.Fatal(err)
	}
	budget, _ := NewLiveBudget(limits, time.Second*10)
	path := filepath.Join(t.TempDir(), "run")
	service := &liveServices{connection: c, directory: path, transport: transport, budget: budget, client: server.Client()}
	defer service.Close()
	report, err := Run(context.Background(), s, path, RunOptions{Services: service, Mode: "native_policy_live", Transport: transport})
	if err != nil || !report.Success || report.Usage.ReasoningTokens == nil || *report.Usage.ReasoningTokens != 2 {
		t.Fatal(report, err)
	}
	for _, name := range []string{"manifest.json", "scenario.json", "events.jsonl", "requests.jsonl", "responses.jsonl"} {
		raw, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), c.key) || strings.Contains(string(raw), "Authorization") {
			t.Fatal("credential leaked into records")
		}
		info, _ := os.Stat(filepath.Join(path, name))
		if info.Mode().Perm() != 0600 {
			t.Fatal("records not private")
		}
	}
	_, err = service.Model(context.Background(), coreRequestFromRun(t, path))
	if err == nil || calls != 1 {
		t.Fatal("request budget bypassed")
	}
}
func coreRequestFromRun(t *testing.T, path string) core.ModelRequest {
	t.Helper()
	var requests []core.ModelRequest
	if err := readJSON(filepath.Join(path, "requests.json"), &requests); err != nil {
		t.Fatal(err)
	}
	return requests[0]
}
func TestReadDeepSeekRejectsAmbiguousFlashAndCommandSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	config := `{"providers":{"deepseek":{"api":"openai-completions","baseUrl":"https://api.example.test/v1","apiKey":"!read-secret","models":[{"id":"deepseek-v4-flash","contextWindow":32768,"maxTokens":128}]}}}`
	os.WriteFile(path, []byte(config), 0600)
	if _, err := ReadDeepSeek(path); err == nil {
		t.Fatal("command secret accepted")
	}
	config = strings.ReplaceAll(config, "!read-secret", "fixture")
	config = strings.ReplaceAll(config, `"maxTokens":128}]`, `"maxTokens":128},{"id":"deepseek-flash","contextWindow":32768,"maxTokens":128}]`)
	os.WriteFile(path, []byte(config), 0600)
	if _, err := ReadDeepSeek(path); err == nil {
		t.Fatal("ambiguous Flash accepted")
	}
}
