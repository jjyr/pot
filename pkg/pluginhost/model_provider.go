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
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/processscope"
)

// RunModelProvider reads connection metadata through the Control-local adapter.
// This unary JSON RPC never transports model inference or enters Runner bundles.
// Credentials are scoped by the host to this connection and injected only into
// the child's environment. Package stderr and exception text are not diagnostics.
func RunModelProvider(ctx context.Context, node string, p plugin.Package, input core.ModelProviderRequest, credentials map[string]string, worker []string) (core.ModelProviderResponse, error) {
	var result core.ModelProviderResponse
	identity, err := p.ModelProviderIdentity()
	if err != nil {
		return result, err
	}
	c := p.Manifest.Contributes.ModelProviders
	if err = input.Validate(*c); err != nil {
		return result, err
	}
	a := c.Adapter
	if a == nil {
		return result, errors.New("model provider adapter unavailable")
	}
	if len(credentials) > 1 || len(credentials) > 0 && a.CredentialEnv == "" {
		return result, errors.New("model provider credential binding mismatch")
	}
	for key, value := range credentials {
		if key != a.CredentialEnv || value == "" || strings.ContainsRune(value, '\x00') {
			return result, errors.New("model provider credential binding mismatch")
		}
	}
	entry, err := p.Path(a.Entrypoint)
	if err != nil {
		return result, err
	}
	if node == "" {
		node = "node"
	}
	node, err = exec.LookPath(node)
	if err != nil {
		return result, errors.New("model provider Node unavailable")
	}
	node, err = filepath.Abs(node)
	if err != nil {
		return result, errors.New("model provider Node unavailable")
	}
	data, err := json.Marshal(input)
	if err != nil {
		return result, errors.New("invalid model provider request")
	}
	for _, value := range credentials {
		if containsModelProviderCredential(data, value) {
			return result, errors.New("credential in model provider request")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	script := `import {pathToFileURL} from 'node:url';let input='';for await(const c of process.stdin){input+=c;if(input.length>32768)throw Error('input limit');}const {read}=await import(pathToFileURL(process.argv[1]));process.stdout.write(JSON.stringify(await read(JSON.parse(input))));`
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, entry)
	cmd.Dir = p.Root
	cmd.Stdin = bytes.NewReader(data)
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SYSTEMROOT"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+filepath.Dir(node)+string(os.PathListSeparator)+os.Getenv("PATH"))
	for key, value := range credentials {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return result, errors.New("model provider process unavailable")
	}
	cmd.Stderr = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	supervisor, err := processscope.Prepare(cmd, worker, processscope.Markers{})
	if err != nil {
		return result, errors.New("model provider supervisor unavailable")
	}
	cmd.Cancel = func() error { return supervisor.Stop(cmd.Process.Pid) }
	cmd.WaitDelay = 3 * time.Second
	if err = cmd.Start(); err != nil {
		supervisor.Abort()
		return result, errors.New("model provider process unavailable")
	}
	if err = supervisor.Started(); err != nil {
		_ = supervisor.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		_ = supervisor.Finish()
		return result, errors.New("model provider supervisor unavailable")
	}
	const limit = 3 * 1024 * 1024
	raw, readErr := io.ReadAll(io.LimitReader(output, limit+1))
	if len(raw) > limit || readErr != nil {
		_ = supervisor.Stop(cmd.Process.Pid)
	}
	waitErr := cmd.Wait()
	cleanupErr := supervisor.Finish()
	if cleanupErr != nil {
		return result, errors.New("model provider cleanup unconfirmed")
	}
	if readErr != nil || waitErr != nil || len(raw) > limit {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return result, errors.New("model provider exchange timed out")
		}
		return result, errors.New("model provider exchange failed")
	}
	for _, value := range credentials {
		if containsModelProviderCredential(raw, value) {
			return result, errors.New("credential in model provider response")
		}
	}
	if err = core.DecodeJSON(raw, &result); err != nil {
		return core.ModelProviderResponse{}, errors.New("invalid model provider protocol")
	}
	if err = result.Validate(input.Method); err != nil {
		return core.ModelProviderResponse{}, err
	}
	current, err := p.ModelProviderIdentity()
	if err != nil || current != identity {
		return core.ModelProviderResponse{}, errors.New("model provider package changed")
	}
	if result.Error != nil {
		return core.ModelProviderResponse{}, result.Error
	}
	return result, nil
}

// Inspect decoded string values as well as wire bytes: JSON escaping must not
// turn a secret into apparently safe metadata. No plugin diagnostics are returned.
func containsModelProviderCredential(raw []byte, value string) bool {
	if containsBackgroundCredential(raw, value) {
		return true
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return false
	}
	var inspect func(any) bool
	inspect = func(v any) bool {
		switch node := v.(type) {
		case string:
			return containsBackgroundCredential([]byte(node), value)
		case []any:
			for _, child := range node {
				if inspect(child) {
					return true
				}
			}
		case map[string]any:
			for key, child := range node {
				if containsBackgroundCredential([]byte(key), value) || inspect(child) {
					return true
				}
			}
		}
		return false
	}
	return inspect(decoded)
}
