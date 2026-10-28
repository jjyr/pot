// A synthetic stdin/stdout native binding. Uses the production Manager and
// subscription bridge; only the local Control's caller and Wails emitter are fake.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/localui"
)

type command struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Args   []string        `json:"args"`
	Stream int             `json:"stream"`
	Frame  json.RawMessage `json:"frame"`
}
type input struct {
	frame json.RawMessage
	stop  bool
	ack   chan error
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	encoder := json.NewEncoder(os.Stdout)
	write := func(value any) {
		mu.Lock()
		defer mu.Unlock()
		if err := encoder.Encode(value); err != nil {
			panic(err)
		}
	}
	streams := map[int]chan input{}
	var streamMu sync.Mutex
	opened := make(chan int, 256)
	next := 0
	caller := localui.Functions{
		Request: func(_ context.Context, _, path string, _ json.RawMessage) (json.RawMessage, error) {
			switch path {
			case "/api/v1/runtime":
				return json.RawMessage(`{"runtime_id":"control","boot_id":"boot-1"}`), nil
			case "/api/v1/assistants":
				return json.RawMessage(`[]`), nil
			case "/api/v1/agent-links":
				return json.RawMessage(`{"items":[]}`), nil
			}
			return nil, errors.New("unexpected synthetic request")
		},
		Events: func(ctx context.Context, path string, receive func(localui.Event) error) error {
			if path == "/api/v1/changes" {
				<-ctx.Done()
				return ctx.Err()
			}
			streamMu.Lock()
			next++
			id := next
			ch := make(chan input)
			streams[id] = ch
			streamMu.Unlock()
			opened <- id
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case value := <-ch:
					if value.stop {
						value.ack <- nil
						return errors.New("synthetic disconnect")
					}
					err := receive(localui.Event{Type: "conversation", Data: value.frame})
					value.ack <- err
					if err != nil {
						return err
					}
				}
			}
		},
	}
	manager, err := localui.Open(ctx, localui.Options{Local: caller, LocalID: "control"})
	if err != nil {
		panic(err)
	}
	bridge := localui.NewNativeSubscriptions(ctx, manager, func(name string, value localui.NativeEvent) { write(map[string]any{"event": name, "data": value}) })
	defer manager.Close()
	defer bridge.Close()
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var cmd command
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err != nil {
			panic(err)
		}
		var value any
		var err error
		switch cmd.Method {
		case "Call":
			value = `{"runtime_id":"control","boot_id":"boot-1"}`
		case "AssistantReads":
			value = []any{}
		case "Subscribe":
			if len(cmd.Args) != 4 {
				err = errors.New("invalid Subscribe arguments")
				break
			}
			err = bridge.Subscribe(cmd.Args[0], cmd.Args[1], cmd.Args[2], cmd.Args[3])
			if err == nil {
				select {
				case id := <-opened:
					value = id
				case <-time.After(2 * time.Second):
					err = errors.New("stream did not open")
				}
			}
		case "Unsubscribe":
			bridge.Unsubscribe(cmd.Args[0])
		case "frame", "disconnect":
			streamMu.Lock()
			ch := streams[cmd.Stream]
			streamMu.Unlock()
			if ch == nil {
				err = errors.New("unknown synthetic stream")
				break
			}
			ack := make(chan error, 1)
			select {
			case ch <- input{frame: cmd.Frame, stop: cmd.Method == "disconnect", ack: ack}:
				err = <-ack
			case <-time.After(2 * time.Second):
				err = errors.New("retired synthetic stream")
			}
		case "reconnected":
			select {
			case id := <-opened:
				value = id
			case <-time.After(2 * time.Second):
				err = errors.New("stream did not reconnect")
			}
		case "close":
			cancel()
			bridge.Close()
			write(map[string]any{"id": cmd.ID})
			return
		default:
			err = fmt.Errorf("unexpected method %s", cmd.Method)
		}
		result := map[string]any{"id": cmd.ID, "value": value}
		if err != nil {
			result["error"] = err.Error()
		}
		write(result)
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
}
