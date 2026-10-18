package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/processscope"
	"syscall"
	"time"
)

// RunBackground performs one bounded service exchange. Only owner-installed
// package files run here. Transient credentials are never in argv or diagnostics.
func RunBackground(ctx context.Context, node string, p plugin.Package, input core.BackgroundRequest, credentials map[string]string, worker []string) (core.BackgroundResponse, error) {
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
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, entry)
	cmd.Dir = p.Root
	cmd.Stdin = bytes.NewReader(data)
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
		return result, errors.New("background exchange failed")
	}
	if err = core.DecodeJSON(raw, &result); err != nil {
		return result, errors.New("invalid background protocol")
	}
	if err = result.Validate(); err != nil {
		return result, err
	}
	// Defense in depth: no credential material may enter durable state/events.
	for _, value := range credentials {
		if value != "" && strings.Contains(string(raw), value) {
			return core.BackgroundResponse{}, errors.New("credential in background response")
		}
	}
	return result, nil
}
