// Deterministic JSONL fixture, using App Server v2 method/field names. No model,
// filesystem command, or shell is executed by this process.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("codex-cli 0.163.0")
		return
	}
	if len(os.Args) != 4 || os.Args[1] != "app-server" || os.Args[2] != "--listen" || os.Args[3] != "stdio://" {
		os.Exit(12)
	}
	var mu sync.Mutex
	send := func(v any) { mu.Lock(); defer mu.Unlock(); _ = json.NewEncoder(os.Stdout).Encode(v) }
	reply := func(id any, v any) { send(map[string]any{"id": id, "result": v}) }
	muted := map[string]bool{}
	notification := func(method string, p any) {
		if !muted[method] {
			send(map[string]any{"method": method, "params": p})
		}
	}
	threadID := fmt.Sprintf("thread-%d", os.Getpid())
	workspace := ""
	selectedModel := ""
	state := 0
	prompt := ""
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		var m struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params struct {
				ClientInfo struct {
					Name string `json:"name"`
				} `json:"clientInfo"`
				Capabilities struct {
					OptOut []string `json:"optOutNotificationMethods"`
				} `json:"capabilities"`
				Model        string                        `json:"model"`
				Effort       string                        `json:"effort"`
				Cwd          string                        `json:"cwd"`
				Sandbox      string                        `json:"sandbox"`
				Approval     json.RawMessage               `json:"approvalPolicy"`
				Reviewer     json.RawMessage               `json:"approvalsReviewer"`
				ThreadID     string                        `json:"threadId"`
				ExcludeTurns bool                          `json:"excludeTurns"`
				TurnID       string                        `json:"turnId"`
				Input        []struct{ Type, Text string } `json:"input"`
			} `json:"params"`
			Result map[string]any `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(13)
		}
		switch m.Method {
		case "initialize":
			if state != 0 || m.Params.ClientInfo.Name == "" {
				os.Exit(14)
			}
			state = 1
			for _, method := range m.Params.Capabilities.OptOut {
				muted[method] = true
			}
			if m.Params.ClientInfo.Name != "pot_management" && (!muted["item/commandExecution/outputDelta"] || !muted["item/agentMessage/delta"] || muted["item/completed"] || muted["turn/completed"] || muted["turn/started"] || !muted["item/autoApprovalReview/started"] || !muted["item/autoApprovalReview/completed"]) {
				os.Exit(22)
			}
			reply(m.ID, map[string]any{"userAgent": "fake-codex/0.163.0"})
		case "initialized":
			if state != 1 {
				os.Exit(15)
			}
			state = 2
		case "model/list":
			if state != 2 {
				os.Exit(25)
			}
			reply(m.ID, map[string]any{"data": []any{map[string]any{"id": "model-one", "model": "model-one", "displayName": "Model One", "supportedReasoningEfforts": []any{map[string]string{"reasoningEffort": "low"}, map[string]string{"reasoningEffort": "high"}}, "defaultReasoningEffort": "low"}, map[string]any{"id": "model-two", "model": "model-two", "displayName": "Model Two"}}, "nextCursor": nil})
		case "thread/start", "thread/resume":
			if state != 2 || m.Params.Cwd == "" || len(m.Params.Approval) != 0 || len(m.Params.Reviewer) != 0 || (m.Params.Sandbox != "read-only" && m.Params.Sandbox != "workspace-write") {
				os.Exit(16)
			}
			if m.Params.ThreadID == "busy-thread" {
				send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "thread busy-thread already has an active writer"}})
				continue
			}
			if m.Params.ThreadID == "missing-thread" || filepath.Base(m.Params.Cwd) == "reject-thread-start" {
				send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32600, "message": "fixture thread unavailable"}})
				continue
			}
			state = 3
			workspace = m.Params.Cwd
			selectedModel = m.Params.Model
			if m.Method == "thread/resume" {
				if m.Params.ThreadID == "" || !m.Params.ExcludeTurns {
					os.Exit(21)
				}
				threadID = m.Params.ThreadID
			}
			reply(m.ID, map[string]any{"thread": map[string]string{"id": threadID}})
		case "turn/start":
			if state != 3 || m.Params.ThreadID != threadID || len(m.Params.Input) != 1 || m.Params.Input[0].Type != "text" {
				os.Exit(17)
			}
			state = 4
			prompt = m.Params.Input[0].Text
			if selectedModel != "" {
				settings, _ := json.Marshal(map[string]string{"model": selectedModel, "effort": m.Params.Effort})
				if os.WriteFile(filepath.Join(workspace, "native-settings.json"), settings, 0600) != nil {
					os.Exit(26)
				}
			}
			if prompt == "early-overflow-count" || prompt == "early-overflow-bytes" {
				count, text := 65, "x"
				if prompt == "early-overflow-bytes" {
					count, text = 3, strings.Repeat("x", 2*1024*1024)
				}
				for i := 0; i < count; i++ {
					notification("item/completed", map[string]any{"threadId": threadID, "turnId": "turn-1", "item": map[string]string{"id": "early", "type": "agentMessage", "text": text}})
				}
				continue
			}
			if prompt == "early-events" || prompt == "early-approval" {
				notification("turn/started", map[string]any{"threadId": threadID, "turn": map[string]string{"id": "old", "status": "inProgress"}})
				notification("turn/started", map[string]any{"threadId": threadID, "turn": map[string]string{"id": "turn-1", "status": "inProgress"}})
				notification("item/completed", map[string]any{"threadId": threadID, "turnId": "old", "item": map[string]string{"id": "old", "type": "agentMessage", "text": "wrong"}})
				if prompt == "early-approval" {
					send(map[string]any{"id": "approval-1", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": threadID, "turnId": "turn-1", "itemId": "command-1", "command": "untrusted"}})
				} else {
					notification("item/completed", map[string]any{"threadId": threadID, "turnId": "turn-1", "item": map[string]string{"id": "early", "type": "agentMessage", "text": "early answer"}})
					notification("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]string{"id": "turn-1", "status": "completed"}})
				}
			}
			reply(m.ID, map[string]any{"turn": map[string]string{"id": "turn-1", "status": "inProgress"}})
			if prompt == "failure-without-started" {
				notification("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": "turn-1", "status": "failed", "error": map[string]any{"message": "private", "codexErrorInfo": "unauthorized"}}})
				continue
			}
			if prompt == "early-events" || prompt == "early-approval" {
				continue
			}

			notification("turn/started", map[string]any{"threadId": threadID, "turn": map[string]string{"id": "turn-1", "status": "inProgress"}})
			notification("item/started", map[string]any{"threadId": threadID, "turnId": "turn-1", "item": map[string]string{"id": "command-1", "type": "commandExecution", "command": "fixture only"}})
			if prompt == "approve" || strings.HasPrefix(prompt, "approve\n") {
				send(map[string]any{"id": "approval-1", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": threadID, "turnId": "turn-1", "itemId": "command-1", "command": "untrusted"}})
				continue
			}
			if prompt == "auto-review" || strings.HasPrefix(prompt, "auto-review\n") {
				notification("item/autoApprovalReview/started", map[string]any{"threadId": threadID, "turnId": "turn-1", "itemId": "command-1"})
				notification("item/autoApprovalReview/completed", map[string]any{"threadId": threadID, "turnId": "turn-1", "itemId": "command-1", "review": map[string]string{"status": "approved", "rationale": "private automatic review"}})
			}
			if prompt == "fail-with-private-diagnostic" {
				notification("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]any{"id": "turn-1", "status": "failed", "error": map[string]any{"message": "private-native-error-secret", "additionalDetails": "https://private.invalid/token", "codexErrorInfo": map[string]any{"httpConnectionFailed": map[string]any{"httpStatusCode": 503}}}}})
				continue
			}
			if prompt == "hang" {
				continue
			}
			if prompt == "large-tool-result" {
				notification("item/completed", map[string]any{"threadId": threadID, "turnId": "turn-1", "item": map[string]any{"type": "mcpToolCall", "result": map[string]string{"text": strings.Repeat("x", 722126)}}})
			}
			go func() {
				notification("item/completed", map[string]any{"threadId": threadID, "turnId": "turn-1", "item": map[string]string{"type": "agentMessage", "phase": "commentary", "text": "Checking the request."}})
				for i := 0; i < 3; i++ {
					notification("item/agentMessage/delta", map[string]any{"threadId": threadID, "itemId": "answer-1", "delta": fmt.Sprint(i)})
					time.Sleep(15 * time.Millisecond)
				}
				notification("item/completed", map[string]any{"threadId": threadID, "turnId": "turn-1", "item": map[string]string{"type": "agentMessage", "phase": "partial_answer", "text": "Preliminary answer."}})
				notification("item/completed", map[string]any{"threadId": threadID, "turnId": "turn-1", "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": "result: " + prompt}})
				if prompt == "hold-after-answer" {
					if os.WriteFile(filepath.Join(workspace, "answer-ready"), nil, 0600) != nil {
						os.Exit(23)
					}
					for {
						if _, err := os.Stat(filepath.Join(workspace, "complete-turn")); err == nil {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
				}
				notification("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]string{"id": "turn-1", "status": "completed"}})
			}()
		case "turn/interrupt":
			if m.Params.ThreadID != threadID || m.Params.TurnID != "turn-1" {
				os.Exit(18)
			}
			reply(m.ID, map[string]any{})
			time.Sleep(80 * time.Millisecond)
			notification("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]string{"id": "turn-1", "status": "interrupted"}})
		case "":
			if m.ID == "approval-1" {
				if m.Result["decision"] == "accept" {
					if os.WriteFile(filepath.Join(workspace, "approval-executed"), []byte("approved once"), 0600) != nil {
						os.Exit(24)
					}
					notification("item/completed", map[string]any{"threadId": threadID, "turnId": "turn-1", "item": map[string]string{"type": "agentMessage", "phase": "final_answer", "text": "approved operation completed"}})
				} else if m.Result["decision"] != "decline" {
					os.Exit(19)
				}
				notification("turn/completed", map[string]any{"threadId": threadID, "turn": map[string]string{"id": "turn-1", "status": "completed"}})
			}
		default:
			os.Exit(20)
		}
	}
}
