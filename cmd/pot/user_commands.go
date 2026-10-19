package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/serverconfig"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// userCommand keeps every effect in the authenticated Runtime APIs. A CLI
// process never opens an independently writable product database.
func userCommand(ctx context.Context, args []string, out io.Writer) error {
	root, owner := "", "main"
	positionals := []string{}
	literal := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			literal = true
			continue
		}
		if !literal && (arg == "-root" || arg == "--root" || arg == "-assistant" || arg == "--assistant") {
			if i+1 == len(args) {
				return errors.New("flag value missing")
			}
			i++
			if strings.Contains(arg, "assistant") {
				owner = args[i]
			} else {
				root = args[i]
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	args = positionals
	if len(args) == 0 {
		return errors.New("user command missing")
	}
	if !core.Identifier(owner) {
		return errors.New("invalid assistant identity")
	}
	var err error
	if root == "" {
		root, err = instance.DefaultPath()
		if err != nil {
			return err
		}
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	api := func(method, path string, body any) (json.RawMessage, error) {
		return userAPI(ctx, root, owner, method, path, body)
	}
	switch args[0] {
	case "codex-pair":
		if len(args) != 2 {
			return errors.New("usage: pot codex-pair [-root PATH] PAIRING_FILE")
		}
		raw, e := os.ReadFile(args[1])
		if e != nil {
			return e
		}
		defer clear(raw)
		if len(raw) > 16384 {
			return errors.New("pairing_file_too_large")
		}
		value, e := api("POST", "/api/v1/integrations/codex/connection", json.RawMessage(raw))
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(out, string(value))
		return e

	case "chat":
		if len(args) < 2 || strings.TrimSpace(strings.Join(args[1:], " ")) == "" {
			return errors.New("usage: pot chat [-root PATH] [-assistant ID] TEXT")
		}
		value, err := api("POST", "/api/v1/messages", protocol.SendMessage{RequestID: storage.ID(), ConversationID: owner, Text: strings.Join(args[1:], " ")})
		if err != nil {
			return err
		}
		return printUserJSON(out, value)
	case "tasks":
		if len(args) < 2 {
			return errors.New("usage: pot tasks list|show ID|interrupt ID|cancel ID")
		}
		path := "/api/v1/tasks"
		method := "GET"
		var body any
		switch args[1] {
		case "list":
			path = "/api/v1/tasks/query"
			if len(args) != 2 {
				return errors.New("tasks list takes no positional arguments")
			}
		case "show", "interrupt", "cancel":
			if len(args) != 3 || !core.Identifier(args[2]) {
				return errors.New("Task identity required")
			}
			path += "/" + url.PathEscape(args[2])
			if args[1] != "show" {
				path += "/" + args[1]
				method = "POST"
				body = map[string]any{"request_id": storage.ID()}
			}
		default:
			return errors.New("unknown Task action")
		}
		value, err := api(method, path, body)
		if err != nil {
			return err
		}
		return printUserJSON(out, value)
	case "config":
		if len(args) == 3 && args[1] == "server" {
			raw, err := readUserJSON(args[2])
			if err != nil {
				return err
			}
			var settings serverconfig.Settings
			if err = protocol.DecodeStrict(raw, &settings); err != nil {
				return err
			}
			if err = serverconfig.Save(root, settings); err != nil {
				return err
			}
			_, err = fmt.Fprintln(out, "Server settings saved; restart the instance to apply")
			return err
		}

		if len(args) < 3 || args[1] != "get" && args[1] != "set" {
			return errors.New("usage: pot config get SECTION | set SECTION JSON_FILE; sections: providers, agent, routing, usage-policy, instructions")
		}
		paths := map[string]string{"providers": "/api/v1/providers", "agent": "/api/v1/agent", "routing": "/api/v1/agent/routing", "usage-policy": "/api/v1/usage/summary", "instructions": "/api/v1/instructions"}
		path, ok := paths[args[2]]
		if !ok {
			return errors.New("unknown configuration section")
		}
		if args[1] == "get" {
			if len(args) != 3 {
				return errors.New("config get requires one section")
			}
			value, err := api("GET", path, nil)
			if err != nil {
				return err
			}
			return printUserJSON(out, value)
		}
		if len(args) != 4 {
			return errors.New("config set requires JSON file, or - for stdin")
		}
		data, err := readUserJSON(args[3])
		if err != nil {
			return err
		}
		if args[2] == "agent" {
			// The editable selection is intentionally distinct from the immutable active
			// resolved specification. Obtain the latest assistant CAS before saving it.
			var request control.SaveAssistantConfiguration
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(data, &fields); err != nil {
				return err
			}
			if _, wrapped := fields["selection"]; wrapped {
				if err = protocol.DecodeStrict(data, &request); err != nil {
					return err
				}
			} else {
				if err = protocol.DecodeStrict(data, &request.Selection); err != nil {
					return err
				}
				current, err := api("GET", path, nil)
				if err != nil {
					return err
				}
				var configuration control.AssistantConfiguration
				if err = json.Unmarshal(current, &configuration); err != nil {
					return err
				}
				request.ExpectedGeneration = configuration.Generation
				request.ExpectedRevision = configuration.Revision
			}
			if request.RequestID == "" {
				request.RequestID = storage.ID()
			}
			data, err = json.Marshal(request)
			if err != nil {
				return err
			}
		}
		if args[2] == "usage-policy" {
			path = "/api/v1/usage/policy"
		}
		value, err := api("POST", path, data)
		if err != nil {
			return err
		}
		return printUserJSON(out, value)
	case "open":
		if len(args) != 1 {
			return errors.New("open takes no positional arguments")
		}
		return openUserBrowser(ctx, root, owner)
	case "service":
		if len(args) != 2 {
			return errors.New("usage: pot service install|start|stop|uninstall")
		}
		return userService(ctx, root, args[1], out)
	case "doctor":
		if len(args) != 1 {
			return errors.New("doctor takes no positional arguments")
		}
		healthCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		state, healthErr := userAPI(healthCtx, root, owner, "GET", "/api/v1/runtime", nil)
		report := map[string]any{"instance": root, "server_running": healthErr == nil, "go_toolchain": runtime.Version(), "os": runtime.GOOS, "architecture": runtime.GOARCH, "web_embedded": true}
		if healthErr == nil {
			report["runtime"] = state
		} else {
			report["runtime_error"] = healthErr.Error()
		}
		for key, path := range map[string]string{"runtime_socket": "runtime.sock"} {
			info, err := os.Stat(filepath.Join(root, path))
			report[key] = err == nil && info.Mode()&os.ModeSocket != 0
		}
		return printUserJSON(out, report)
	}
	return errors.New("unknown user command")
}
func readUserJSON(path string) (json.RawMessage, error) {
	var reader io.Reader
	var file *os.File
	if path == "-" {
		reader = os.Stdin
	} else {
		var err error
		file, err = os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16*1024*1024 || !json.Valid(data) {
		return nil, errors.New("requires JSON body at most16MiB")
	}
	return data, nil
}
func userAPI(ctx context.Context, root, owner, method, path string, body any) (json.RawMessage, error) {
	endpoint, err := transport.ReadEndpoint(root)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/api/v1/") {
		return nil, errors.New("invalid local API path")
	}
	query := u.Query()
	query.Set("assistant_id", owner)
	u.RawQuery = query.Encode()
	var payload []byte
	if body != nil {
		switch v := body.(type) {
		case json.RawMessage:
			payload = v
		default:
			payload, err = json.Marshal(body)
			if err != nil {
				return nil, err
			}
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://runtime"+u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+endpoint.Token)
	request.Header.Set("X-Pot-Control-ID", endpoint.RuntimeID)
	request.Header.Set("Content-Type", "application/json")
	client := endpoint.Client()
	client.Timeout = 30 * time.Second
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 16*1024*1024 {
		return nil, errors.New("Runtime response exceeds limit")
	}
	if response.StatusCode >= 400 {
		var failure protocol.APIError
		if json.Unmarshal(data, &failure) == nil && failure.Code != "" {
			return nil, failure
		}
		return nil, fmt.Errorf("Runtime HTTP %d", response.StatusCode)
	}
	if !json.Valid(data) {
		return nil, errors.New("Runtime returned invalid JSON")
	}
	return data, nil
}
func printUserJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
