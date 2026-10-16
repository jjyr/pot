package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const snapshotLimit = 512 * 1024
const stateMarker = "\x00POT_SNAPSHOT_STATE\x00"
const envMarker = "\x00POT_SNAPSHOT_ENV\x00"

type shellSnapshot struct {
	state string
	env   []string
}
type snapshotEntry struct {
	key   [32]byte
	value *shellSnapshot
}
type snapshots struct {
	mu      sync.Mutex
	entries []snapshotEntry
}

func snapshotKey(shell, cwd, home string) [32]byte {
	hash := sha256.New()
	fmt.Fprintf(hash, "%q %q %q", shell, cwd, home)
	files := []string{shell, "/etc/profile", "/etc/bash.bashrc", "/etc/zshenv", "/etc/zprofile", "/etc/zsh/zshenv", "/etc/zsh/zprofile"}
	for _, name := range []string{".profile", ".bash_profile", ".bash_login", ".bashrc", ".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
		files = append(files, filepath.Join(home, name))
	}
	for _, path := range files {
		fmt.Fprintf(hash, "%q", path)
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(hash, "%d %d", info.Size(), info.ModTime().UnixNano())
		}
		if path != shell {
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				if f, err := os.Open(path); err == nil {
					data, _ := io.ReadAll(io.LimitReader(f, snapshotLimit+1))
					hash.Write(data)
					f.Close()
				}
			}
		}
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}
func (s *snapshots) command(ctx context.Context, shell, cwd, home, source string, login, enabled bool) (*exec.Cmd, error) {
	command := exec.Command(shell)
	command.Dir = cwd
	command.Env = append(os.Environ(), "HOME="+home)
	name := filepath.Base(shell)
	if !login || !enabled || (name != "bash" && name != "zsh") {
		flag := "-c"
		if login {
			flag = "-lc"
		}
		command.Args = append(command.Args, flag, source)
		return command, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := snapshotKey(shell, cwd, home)
	var snapshot *shellSnapshot
	found := false
	for index, entry := range s.entries {
		if entry.key == key {
			snapshot = entry.value
			s.entries = append(append(s.entries[:index:index], s.entries[index+1:]...), entry)
			found = true
			break
		}
	}
	if !found {
		var err error
		snapshot, err = captureSnapshot(ctx, shell, cwd, home)
		if errors.Is(err, ErrCleanupUnconfirmed) {
			return nil, err
		}
		if len(s.entries) == 16 {
			s.entries = s.entries[1:]
		}
		s.entries = append(s.entries, snapshotEntry{key, snapshot})
	}
	if snapshot == nil {
		command.Args = append(command.Args, "-lc", source)
		return command, nil
	}
	command.Env = append(append([]string{}, snapshot.env...), "POT_SHELL_BASELINE="+snapshot.state, "POT_SHELL_COMMAND="+source, "POT_SHELL_EXECUTABLE="+shell)
	flag, startup := "-pc", "set +o privileged"
	if name == "zsh" {
		flag = "-fc"
		startup = "setopt RCS"
	}
	replay := startup + "\nif ! builtin eval \"$POT_SHELL_BASELINE\"; then exec /usr/bin/env -u POT_SHELL_BASELINE -u POT_SHELL_COMMAND -u POT_SHELL_EXECUTABLE \"$POT_SHELL_EXECUTABLE\" -lc \"$POT_SHELL_COMMAND\"; fi\nbuiltin unset POT_SHELL_BASELINE POT_SHELL_EXECUTABLE\nbuiltin eval \"builtin unset POT_SHELL_COMMAND\n$POT_SHELL_COMMAND\""
	command.Args = append(command.Args, flag, replay)
	return command, nil
}

type boundedCapture struct {
	bytes.Buffer
	limit int
}

func (w *boundedCapture) Write(data []byte) (int, error) {
	if w.Len()+len(data) > w.limit {
		return 0, errors.New("shell snapshot exceeds 512 KiB")
	}
	return w.Buffer.Write(data)
}
func captureSnapshot(ctx context.Context, shell, cwd, home string) (*shellSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	state := "declare -f; alias -p; set +o; shopt -p || true; printf 'umask '; umask"
	if filepath.Base(shell) == "zsh" {
		state = "functions; alias -L; setopt | sed 's/^/setopt /'; printf 'umask '; umask"
	}
	source := "printf '\\0POT_SNAPSHOT_STATE\\0'\n" + state + "\nprintf '\\0POT_SNAPSHOT_ENV\\0'\nenv -0"
	command := exec.Command(shell, "-lc", source)
	command.Dir = cwd
	command.Env = append(os.Environ(), "HOME="+home)
	command.SysProcAttr = processGroup()
	var output boundedCapture
	output.limit = snapshotLimit
	command.Stdout = &output
	if err := command.Start(); err != nil {
		return nil, err
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	var err error
	select {
	case err = <-waited:
	case <-ctx.Done():
		_ = killGroup(command.Process.Pid)
		select {
		case <-waited:
		case <-time.After(5 * time.Second):
			return nil, errors.Join(ErrCleanupUnconfirmed, ctx.Err())
		}
		err = ctx.Err()
	}
	if cleanup := confirmGroupGone(command.Process.Pid, time.Now().Add(time.Second)); cleanup != nil {
		return nil, cleanup
	}
	if err != nil {
		return nil, err
	}
	data := output.Bytes()
	offset := bytes.Index(data, []byte(envMarker))
	start := bytes.Index(data, []byte(stateMarker))
	if offset < 0 || start < 0 || start+len(stateMarker) > offset {
		return nil, errors.New("invalid shell snapshot markers")
	}
	baseline := string(data[start+len(stateMarker) : offset])
	if len(baseline) > 60*1024 {
		return nil, errors.New("shell state exceeds 60 KiB")
	}
	snapshot := &shellSnapshot{state: baseline}
	for _, record := range bytes.Split(data[offset+len(envMarker):], []byte{0}) {
		if len(record) == 0 {
			continue
		}
		name, value, ok := strings.Cut(string(record), "=")
		if !ok || len(value) > 60*1024 {
			return nil, errors.New("invalid shell snapshot environment")
		}
		if name != "POT_SHELL_BASELINE" && name != "POT_SHELL_COMMAND" && name != "POT_SHELL_EXECUTABLE" {
			snapshot.env = append(snapshot.env, name+"="+value)
		}
	}
	return snapshot, nil
}
