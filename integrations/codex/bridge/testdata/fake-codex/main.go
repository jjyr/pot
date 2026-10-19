// Deterministic JSONL fixture, using App Server v2 method/field names. No model,
// filesystem command, or shell is executed by this process.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
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
	notification := func(method string, p any) { send(map[string]any{"method": method, "params": p}) }
	state := 0
	prompt := ""
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 65536)
	for scanner.Scan() {
		var m struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Params struct {
				ClientInfo any                           `json:"clientInfo"`
				Cwd        string                        `json:"cwd"`
				Sandbox    string                        `json:"sandbox"`
				Approval   string                        `json:"approvalPolicy"`
				ThreadID   string                        `json:"threadId"`
				TurnID     string                        `json:"turnId"`
				Input      []struct{ Type, Text string } `json:"input"`
			} `json:"params"`
			Result map[string]any `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			os.Exit(13)
		}
		switch m.Method {
		case "initialize":
			if state != 0 || m.Params.ClientInfo == nil {
				os.Exit(14)
			}
			state = 1
			reply(m.ID, map[string]any{"userAgent": "fake-codex/0.163.0"})
		case "initialized":
			if state != 1 {
				os.Exit(15)
			}
			state = 2
		case "thread/start":
			if state != 2 || m.Params.Cwd == "" || m.Params.Approval != "on-request" || (m.Params.Sandbox != "read-only" && m.Params.Sandbox != "workspace-write") {
				os.Exit(16)
			}
			state = 3
			reply(m.ID, map[string]any{"thread": map[string]string{"id": "thread-1"}})
		case "turn/start":
			if state != 3 || m.Params.ThreadID != "thread-1" || len(m.Params.Input) != 1 || m.Params.Input[0].Type != "text" {
				os.Exit(17)
			}
			state = 4
			prompt = m.Params.Input[0].Text
			reply(m.ID, map[string]any{"turn": map[string]string{"id": "turn-1", "status": "inProgress"}})
			notification("turn/started", map[string]any{"threadId": "thread-1", "turn": map[string]string{"id": "turn-1", "status": "inProgress"}})
			notification("item/started", map[string]any{"threadId": "thread-1", "item": map[string]string{"id": "command-1", "type": "commandExecution", "command": "fixture only"}})
			if prompt == "approve" {
				send(map[string]any{"id": "approval-1", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "itemId": "command-1", "command": "untrusted"}})
				continue
			}
			if prompt == "hang" {
				continue
			}
			go func() {
				for i := 0; i < 3; i++ {
					notification("item/agentMessage/delta", map[string]any{"threadId": "thread-1", "itemId": "answer-1", "delta": fmt.Sprint(i)})
					time.Sleep(15 * time.Millisecond)
				}
				notification("item/completed", map[string]any{"threadId": "thread-1", "item": map[string]string{"type": "agentMessage", "text": "result: " + prompt}})
				notification("turn/completed", map[string]any{"threadId": "thread-1", "turn": map[string]string{"id": "turn-1", "status": "completed"}})
			}()
		case "turn/interrupt":
			if m.Params.ThreadID != "thread-1" || m.Params.TurnID != "turn-1" {
				os.Exit(18)
			}
			reply(m.ID, map[string]any{})
			time.Sleep(80 * time.Millisecond)
			notification("turn/completed", map[string]any{"threadId": "thread-1", "turn": map[string]string{"id": "turn-1", "status": "interrupted"}})
		case "":
			if m.ID == "approval-1" {
				if m.Result["decision"] != "decline" {
					os.Exit(19)
				}
				notification("turn/completed", map[string]any{"threadId": "thread-1", "turn": map[string]string{"id": "turn-1", "status": "completed"}})
			}
		default:
			os.Exit(20)
		}
	}
}
