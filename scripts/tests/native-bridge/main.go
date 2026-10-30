package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/jjyr/pot/internal/localui"
)

const runtimeJSON = `{"runtime_id":"control","boot_id":"boot-1"}`
const resetJSON = `{"kind":"reset","base_cursor":0,"view":{"state":{"boot_id":"boot-1","conversation_id":"conversation","cursor":1},"messages":[],"replies":[],"history":{"has_more":false,"has_newer":false}}}`

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	caller := localui.Functions{
		Request: func(ctx context.Context, method, path string, body json.RawMessage) (json.RawMessage, error) {
			if path == "/api/v1/runtime" {
				return json.RawMessage(runtimeJSON), nil
			}
			if path == "/echo" {
				return body, nil
			}
			return nil, errors.New("unknown request")
		},
		Events: func(ctx context.Context, path string, emit func(localui.Event) error) error {
			if err := emit(localui.Event{Type: "conversation", Data: json.RawMessage(resetJSON)}); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		childCtx, cancelRequest := context.WithCancel(r.Context())
		stop := context.AfterFunc(ctx, cancelRequest)
		defer stop()
		defer cancelRequest()
		localui.ServeWebSocket(w, r.WithContext(childCtx), localui.SocketOptions{Caller: caller, ExpectedControlID: "control"})
	}))

	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"url": server.URL + "/api/v1/ws"}); err != nil {
		panic(err)
	}

	_, _ = io.Copy(io.Discard, os.Stdin)
	cancel()
	server.Close()
}
