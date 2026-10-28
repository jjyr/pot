package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/mcp"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "models" {
		fmt.Println("model-one\tModel one\nmodel-two\tModel two")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		if _, err := os.Stat("execution-tools-binary"); err == nil {
			cwd, _ := os.Getwd()
			raw, _ := json.Marshal(map[string]string{"workspace": cwd, "root": os.Getenv("POT_AGENT_HOST_ROOT"), "listen": os.Getenv("POT_AGENT_HOST_LISTEN"), "job": os.Getenv("POT_AGENT_JOB_ID"), "run": os.Getenv("POT_AGENT_RUN_ID"), "inherited": os.Getenv("POT_AGENT_UNTRUSTED")})
			if os.WriteFile("preflight-"+os.Getenv("POT_AGENT_RUN_ID")+".json", raw, 0600) != nil {
				os.Exit(2)
			}
		}
		fmt.Println(`[{"name":"pot-receiver","enabled":true,"hooks":["pot-execution-scope"],"mcpServers":["pot_receiver"]}]`)
		return
	}
	flags := map[string]string{}
	for i := 1; i < len(os.Args)-1; i++ {
		if strings.HasPrefix(os.Args[i], "--") {
			flags[os.Args[i]] = os.Args[i+1]
		}
	}
	id := flags["--conversation"]
	if id == "" {
		var raw [16]byte
		rand.Read(raw[:])
		id = hex.EncodeToString(raw[:])
	}
	if flags["--model"] == "wrong-resume" {
		id = "different-session"
	}
	encoder := json.NewEncoder(os.Stdout)
	if flags["--model"] == "start-error" {
		encoder.Encode(map[string]any{"event": "result", "result": map[string]string{"conversation_id": "", "status": "ERROR", "error": "PRIVATE_NATIVE_ERROR /secret/config token=fixture"}})
		return
	}
	encoder.Encode(map[string]any{"event": "init", "conversation_id": id, "init": map[string]string{"model": flags["--model"]}})
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return
	}
	var input struct{ Message struct{ Content string } }
	json.Unmarshal(scanner.Bytes(), &input)
	os.WriteFile("received-prompt", []byte(input.Message.Content), 0600)
	switch input.Message.Content {
	case "hold":
		time.Sleep(time.Minute)
		return
	case "malformed":
		fmt.Println("{bad json")
		return
	case "native-error":
		encoder.Encode(map[string]any{"event": "result", "result": map[string]string{"conversation_id": id, "status": "ERROR", "response": "PRIVATE_NATIVE_RESPONSE", "error": "PRIVATE_NATIVE_ERROR /secret/config token=fixture"}})
		return
	case "wrong-result":
		id = "wrong-session"
	}
	if _, err := os.Stat("execution-tools-binary"); err == nil {
		if err := probeExecutionContext(); err != nil {
			encoder.Encode(map[string]any{"event": "result", "result": map[string]string{"conversation_id": id, "status": "ERROR", "error": err.Error()}})
			return
		}
	}
	status := "SUCCESS"
	if input.Message.Content == "unknown-state" {
		status = "RUNNING"
	}
	encoder.Encode(map[string]any{"event": "result", "result": map[string]string{"conversation_id": id, "status": status, "response": "native: " + input.Message.Content}})
}

// Exercise the shipped env-only stdio entry, using the real HTTP host and MCP
// handshake. Only AGY's stream producer is synthetic in this fixture.
func probeExecutionContext() error {
	binary, err := os.ReadFile("execution-tools-binary")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Match AGY's actual plugin namespace and PascalCase dispatcher schema.
	// Exercise the installed hook contract before dispatching the MCP tool.
	policy := exec.CommandContext(ctx, string(binary), "policy")
	policy.Env = append(os.Environ(), "POT_AGENTHOST_CLI_FIXTURE=1", "POT_AGENTHOST_CLI_ADAPTER=antigravity")
	policy.Stdin = strings.NewReader(`{"toolCall":{"name":"call_mcp_tool","args":{"ServerName":"pot-receiver_pot_receiver","ToolName":"pot_execution_context","Arguments":{}}}}`)
	decisionRaw, err := policy.Output()
	if err != nil {
		return err
	}
	var decision map[string]string
	if json.Unmarshal(decisionRaw, &decision) != nil || decision["decision"] != "allow" {
		return fmt.Errorf("receiver context denied by native hook")
	}
	cmd := exec.CommandContext(ctx, string(binary), "execution-tools")
	cmd.Env = append(os.Environ(), "POT_AGENTHOST_CLI_FIXTURE=1", "POT_AGENTHOST_CLI_ADAPTER=antigravity")
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer output.Close()
	if err = cmd.Start(); err != nil {
		return err
	}
	defer func() { input.Close(); cmd.Process.Kill(); cmd.Wait() }()
	session, err := mcp.ConnectStdio(ctx, output, input, mcp.Options{})
	if err != nil {
		return err
	}
	defer session.Close()
	tools, err := session.ListTools(ctx)
	if err != nil {
		return err
	}
	if len(tools) != 1 || tools[0].Name != "pot_execution_context" {
		return fmt.Errorf("unexpected task tools")
	}
	result, err := session.CallTool(ctx, "pot_execution_context", json.RawMessage(`{}`))
	if err != nil {
		return err
	}
	if result.IsError {
		return fmt.Errorf("execution context fenced")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return err
	}
	var binding agentlink.MCPExecutionBinding
	if err = json.Unmarshal(raw, &binding); err != nil {
		return err
	}
	if binding.Validate() != nil || binding.Task.AgentID != os.Getenv("POT_AGENT_JOB_ID") || binding.RunID != os.Getenv("POT_AGENT_RUN_ID") || binding.Actor != binding.Task {
		return fmt.Errorf("execution binding mismatch")
	}
	return os.WriteFile("context-"+binding.RunID+".json", raw, 0600)
}
