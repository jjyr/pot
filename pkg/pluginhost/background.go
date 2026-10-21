package pluginhost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/processscope"
)

// RunBackground performs one bounded service exchange. Only owner-installed
// package files run here. Transient credentials are never in argv or diagnostics.
func RunBackground(ctx context.Context, node string, p plugin.Package, input core.BackgroundRequest, credentials map[string]string, worker []string) (core.BackgroundResponse, error) {
	return runBackground(ctx, node, p, input, credentials, worker, nil)
}

// RunBackgroundStream maintains one supervised process. Each bounded frame is
// validated and durably accepted before ACK; no unacknowledged checkpoint can
// escape into the next exchange. Cancellation drains the entire process scope.
func RunBackgroundStream(ctx context.Context, node string, p plugin.Package, input core.BackgroundRequest, credentials map[string]string, worker []string, accept func(core.BackgroundResponse) error) error {
	if accept == nil || p.Manifest.Contributes.Background == nil || p.Manifest.Contributes.Background.Mode != "stream" {
		return errors.New("stream capability required")
	}
	_, err := runBackground(ctx, node, p, input, credentials, worker, accept)
	return err
}
func runBackground(ctx context.Context, node string, p plugin.Package, input core.BackgroundRequest, credentials map[string]string, worker []string, accept func(core.BackgroundResponse) error) (core.BackgroundResponse, error) {
	var result core.BackgroundResponse
	if err := p.Manifest.Validate(); err != nil {
		return result, err
	}
	b := p.Manifest.Contributes.Background
	if b == nil {
		return result, errors.New("background capability unavailable")
	}
	if len(credentials) != len(b.Credentials) {
		return result, errors.New("background credential binding mismatch")
	}
	for name, value := range credentials {
		if _, ok := b.Credentials[name]; !ok || value == "" {
			return result, errors.New("background credential binding mismatch")
		}
	}
	for _, name := range b.Files {
		if _, err := p.ReadFile(name, 512*1024); err != nil {
			return result, err
		}
	}
	entry, err := p.Path(b.Entrypoint)
	if err != nil {
		return result, err
	}
	if node == "" {
		node = "node"
	}
	node, err = exec.LookPath(node)
	if err != nil {
		return result, errors.New("background Node unavailable")
	}
	node, err = filepath.Abs(node)
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	// Package code cannot select the entrypoint or impersonate another package.
	script := `import {pathToFileURL} from 'node:url'; let input=''; for await(const c of process.stdin) {input+=c;if(input.length>3000000)throw Error('input limit');} const {poll}=await import(pathToFileURL(process.argv[1])); process.stdout.write(JSON.stringify(await poll(JSON.parse(input))));`
	if accept != nil {
		script = `import {pathToFileURL} from 'node:url';import {createInterface} from 'node:readline';const lines=createInterface({input:process.stdin,crlfDelay:Infinity})[Symbol.asyncIterator]();const first=await lines.next();if(first.done||first.value.length>3000000)throw Error('input limit');const {stream}=await import(pathToFileURL(process.argv[1]));await stream(JSON.parse(first.value),async frame=>{process.stdout.write(JSON.stringify(frame)+'\n');const ack=await lines.next();if(ack.done||ack.value!=='{"accepted":true}')throw Error('acceptance failed');});`
	}
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, entry)
	cmd.Dir = p.Root
	var inputPipe io.WriteCloser
	if accept == nil {
		cmd.Stdin = bytes.NewReader(data)
	} else {
		inputPipe, err = cmd.StdinPipe()
		if err != nil {
			return result, err
		}
		defer inputPipe.Close()
	}
	// Do not inherit unrelated service tokens from the host environment.
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SYSTEMROOT"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	if filepath.IsAbs(node) {
		cmd.Env = append(cmd.Env, "PATH="+filepath.Dir(node)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	for key, value := range credentials {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return result, err
	}
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	supervisor, err := processscope.Prepare(cmd, worker, processscope.Markers{})
	if err != nil {
		return result, err
	}
	cmd.Cancel = func() error { return supervisor.Stop(cmd.Process.Pid) }
	cmd.WaitDelay = 3 * time.Second
	if err = cmd.Start(); err != nil {
		supervisor.Abort()
		return result, errors.New("background process unavailable")
	}
	if err = supervisor.Started(); err != nil {
		_ = supervisor.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		_ = supervisor.Finish()
		return result, errors.New("background supervisor unavailable")
	}
	if accept != nil {
		streamErr := func() error {
			if _, err := inputPipe.Write(append(data, '\n')); err != nil {
				return err
			}
			scanner := bufio.NewScanner(output)
			scanner.Buffer(make([]byte, 4096), 3*1024*1024+1)
			for scanner.Scan() {
				raw := scanner.Bytes()
				if len(raw) > 3*1024*1024 {
					return errors.New("background frame limit")
				}
				var frame core.BackgroundResponse
				if err := core.DecodeJSON(raw, &frame); err != nil {
					return errors.New("invalid background protocol")
				}
				if err := validateBackgroundRouting(p, frame); err != nil {
					return err
				}
				for _, value := range credentials {
					if containsBackgroundCredential(raw, value) {
						return errors.New("credential in background response")
					}
				}
				if err := accept(frame); err != nil {
					return err
				}
				if _, err := io.WriteString(inputPipe, "{\"accepted\":true}\n"); err != nil {
					return err
				}
			}
			if err := scanner.Err(); err != nil {
				return errors.New("background stream failed")
			}
			return errors.New("background stream ended")
		}()
		_ = supervisor.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		if supervisor.Finish() != nil {
			return result, errors.New("background cleanup unconfirmed")
		}
		return result, streamErr
	}
	raw, readErr := io.ReadAll(io.LimitReader(output, 3*1024*1024+1))
	if len(raw) > 3*1024*1024 {
		_ = supervisor.Stop(cmd.Process.Pid)
	}
	waitErr := cmd.Wait()
	cleanupErr := supervisor.Finish()
	if cleanupErr != nil {
		return result, errors.New("background cleanup unconfirmed")
	}
	if readErr != nil || waitErr != nil || len(raw) > 3*1024*1024 {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return result, errors.New("background exchange timed out")
		}
		return result, errors.New("background exchange failed")
	}
	if err = core.DecodeJSON(raw, &result); err != nil {
		return result, errors.New("invalid background protocol")
	}
	if err = validateBackgroundRouting(p, result); err != nil {
		return result, err
	}
	// Defense in depth: no credential material may enter durable state/events.
	for _, value := range credentials {
		if containsBackgroundCredential(raw, value) {
			return core.BackgroundResponse{}, errors.New("credential in background response")
		}
	}
	return result, nil
}

// Structured credentials may carry public routing metadata alongside a token.
// Check secret fields too, without treating public device IDs as credentials.
func containsBackgroundCredential(raw []byte, value string) bool {
	if value != "" && strings.Contains(string(raw), value) {
		return true
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &object) == nil {
		for _, key := range []string{"token", "access_token", "refresh_token", "secret"} {
			var token string
			if json.Unmarshal(object[key], &token) == nil && len(token) >= 8 && strings.Contains(string(raw), token) {
				return true
			}
		}
	}
	return false
}

func validateBackgroundRouting(p plugin.Package, response core.BackgroundResponse) error {
	if err := response.Validate(); err != nil {
		return err
	}
	for _, event := range response.Events {
		if event.Assistant != "" && !p.Manifest.Contributes.Background.EventRouting {
			return errors.New("event routing unavailable")
		}
	}
	return nil
}
