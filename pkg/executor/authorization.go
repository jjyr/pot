package executor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jjyr/pot/pkg/core"
)

type CommandContext struct {
	Command     string          `json:"command"`
	Directory   string          `json:"directory"`
	Shell       string          `json:"shell"`
	Login       bool            `json:"login"`
	Snapshot    bool            `json:"snapshot"`
	TTY         bool            `json:"tty"`
	Interactive bool            `json:"interactive"`
	Credentials json.RawMessage `json:"credentials,omitempty"`
}

func (c CommandContext) Validate() error {
	if c.Command == "" || len(c.Command) > 1024*1024 || strings.ContainsRune(c.Command, 0) || !filepath.IsAbs(c.Directory) || filepath.Clean(c.Directory) != c.Directory || !filepath.IsAbs(c.Shell) {
		return errors.New("invalid exact command context")
	}
	if !supportedShell(c.Shell) {
		return errors.New("unsupported command shell")
	}
	if len(c.Credentials) > 0 {
		var refs core.CredentialBindings
		if err := core.DecodeJSON(c.Credentials, &refs); err != nil {
			return err
		}
		if err := refs.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ResolveCommandContext runs on the destination, before approval and again
// before admission, so aliases/symlinks cannot expand a saved cwd grant.
func ResolveCommandContext(cwd string, args map[string]any, policy core.ShellSettings) (*CommandContext, error) {
	dir := cwd
	if value, ok := args["workdir"].(string); ok {
		if value == "" {
			return nil, errors.New("empty working directory")
		}
		if filepath.IsAbs(value) {
			dir = value
		} else {
			dir = filepath.Join(cwd, value)
		}
	}
	var err error
	dir, err = resolveWorkingDirectory(dir, cwd)
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	shell := policy.Executable
	if value, ok := args["shell"].(string); ok {
		shell = value
	}
	if !filepath.IsAbs(shell) {
		return nil, errors.New("shell must be absolute")
	}
	shell, err = filepath.EvalSymlinks(shell)
	if err != nil {
		return nil, err
	}
	command, _ := args["cmd"].(string)
	login := policy.AllowLoginShell
	if value, ok := args["login"].(bool); ok {
		login = value
	}
	if login && !policy.AllowLoginShell {
		return nil, errors.New("login shell is disabled")
	}
	interactive := args["interactive"] == true
	result := &CommandContext{Command: command, Directory: dir, Shell: shell, Login: login, Snapshot: policy.ShellSnapshot, Interactive: interactive, TTY: args["tty"] == true}
	if value := args["credentials"]; value != nil {
		result.Credentials, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	return result, result.Validate()
}

func resolveWorkingDirectory(path, workspace string) (string, error) {
	dir, err := resolve(path, workspace)
	if err == nil {
		var info os.FileInfo
		info, err = os.Stat(dir)
		if err == nil && !info.IsDir() {
			err = errors.New("path is not a directory")
		}
	}
	if err != nil {
		instruction := ""
		if errors.Is(err, os.ErrNotExist) {
			instruction = ". Create the directory from an existing workdir first"
		}
		return "", &NotStartedError{Cause: fmt.Errorf("cannot use workdir %q; command was not started%s: %w", path, instruction, err)}
	}
	return dir, nil
}
