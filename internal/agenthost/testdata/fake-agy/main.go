package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "models" {
		fmt.Println("model-one\tModel one\nmodel-two\tModel two")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
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
		encoder.Encode(map[string]any{"event": "result", "result": map[string]string{"conversation_id": "", "status": "ERROR", "error": "native model conflict"}})
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
	case "wrong-result":
		id = "wrong-session"
	}
	status := "SUCCESS"
	if input.Message.Content == "unknown-state" {
		status = "RUNNING"
	}
	encoder.Encode(map[string]any{"event": "result", "result": map[string]string{"conversation_id": id, "status": status, "response": "native: " + input.Message.Content}})
}
