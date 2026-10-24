package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/jjyr/pot/pkg/sandbox"
)

const PatchWorkerArgument = "--internal-patch-worker"

// Private executor-to-worker input, supplied on stdin before workload startup.
// WriteScope is not a model-visible tool argument or an OS permission override.
type patchWorkerRequest struct {
	Workspace  string           `json:"workspace"`
	Patch      string           `json:"patch"`
	WriteScope *patchWriteScope `json:"write_scope,omitempty"`
}

// ServePatchWorker performs only the supplied authorized patch under its filesystem sandbox. Hosts
// dispatch the private worker argument before parsing their public CLI flags.
func ServePatchWorker(input io.Reader, output io.Writer) error {
	var request patchWorkerRequest
	if err := json.NewDecoder(io.LimitReader(input, patchLimit+1024*1024)).Decode(&request); err != nil {
		return err
	}
	report, err := applyPatch(request.Workspace, request.Patch, request.WriteScope)
	result := struct {
		Report string `json:"report"`
		Error  string `json:"error,omitempty"`
	}{Report: report}
	if err != nil {
		result.Error = err.Error()
	}
	return json.NewEncoder(output).Encode(result)
}
func (e *Engine) sandboxPatch(ctx context.Context, r Request) (string, error) {
	worker := e.options.PatchWorker
	if len(worker) == 0 {
		binary, err := os.Executable()
		if err != nil {
			return "", err
		}
		worker = []string{binary, PatchWorkerArgument}
	}
	command := exec.Command(worker[0], worker[1:]...)
	command.Dir = r.CWD
	command.Env = sandbox.Environment()
	scope, write := patchScope(r.CWD, e.options.SandboxWrite)
	body, err := json.Marshal(patchWorkerRequest{Workspace: r.CWD, Patch: r.Args["patch"].(string), WriteScope: scope})
	if err != nil {
		return "", err
	}
	command.Stdin = bytes.NewReader(body)
	var output, diagnostic bytes.Buffer
	command.Stdout = &output
	command.Stderr = &diagnostic
	finish, err := e.sandboxLauncher().Prepare(ctx, command, r.CWD, e.options.SandboxRead, write, false)
	if err != nil {
		return "", &PreparationError{Cause: err}
	}
	command.SysProcAttr = processGroup()
	var release func()
	if e.options.Admit != nil {
		release, err = e.options.Admit(r.Revision)
		if err != nil {
			_ = finish()
			return "", err
		}
	}
	if err = command.Start(); err != nil {
		if release != nil {
			release()
		}
		_ = finish()
		return "", err
	}
	if release != nil {
		release()
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = killGroup(command.Process.Pid)
		err = ctx.Err()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			err = errors.Join(err, ErrCleanupUnconfirmed)
		}
	}
	if cleanup := confirmGroupGone(command.Process.Pid, time.Now().Add(time.Second)); cleanup != nil {
		return "", cleanup
	}
	if cleanup := finish(); cleanup != nil {
		return "", errors.Join(ErrCleanupUnconfirmed, cleanup)
	}
	if err != nil {
		return "", fmt.Errorf("patch worker: %w: %s", err, diagnostic.String())
	}
	var result struct {
		Report string `json:"report"`
		Error  string `json:"error"`
	}
	if err = json.Unmarshal(output.Bytes(), &result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return result.Report, errors.New(result.Error)
	}
	return result.Report, nil
}
