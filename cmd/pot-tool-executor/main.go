// Command pot-tool-executor is the JSON-lines execution subprocess used by
// the retained Python guest brokers. Protocol stdout contains only events/results.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	if len(args) == 1 && args[0] == "--capabilities" {
		return encoder.Encode(map[string]any{"protocol_version": 1, "plugins": map[string]any{"protocol_version": 1, "runtime": "node", "requirements": []string{"node", "chromium", "git", "gh"}}, "observation_modes": []string{"text", "mixed"}})
	}
	if len(args) != 0 {
		return errors.New("usage: pot-tool-executor [--capabilities]")
	}
	engine := executor.New(executor.Options{ExecutionTarget: "sandbox"})
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 65536), 8*1024*1024)
	for scanner.Scan() {
		var request executor.Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return errors.Join(err, engine.Close())
		}
		// UseNumber preserves tool integers without routing them through float64.
		var envelope struct {
			Args json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			return errors.Join(err, engine.Close())
		}
		if len(envelope.Args) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(envelope.Args))
			decoder.UseNumber()
			if err := decoder.Decode(&request.Args); err != nil {
				return errors.Join(err, engine.Close())
			}
		}
		result, err := engine.Execute(ctx, request, func(event executor.Event) error { return encoder.Encode(map[string]any{"event": event}) })
		if err != nil {
			result = map[string]any{"isError": true, "error": err.Error()}
			if core.IsCleanupUnconfirmed(err) {
				result["cleanup_unconfirmed"] = true
			}
		}
		if err := encoder.Encode(map[string]any{"result": result}); err != nil {
			return errors.Join(err, engine.Close())
		}
		if ctx.Err() != nil {
			return errors.Join(ctx.Err(), engine.Close())
		}
	}
	return errors.Join(scanner.Err(), engine.Close())
}
