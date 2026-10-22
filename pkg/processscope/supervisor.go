//go:build darwin || linux

// Package processscope supervises descendants in a separate trusted process.
// Linux uses a child subreaper; macOS uses original process identities and OS sandbox membership. A parent exit without a cleanup receipt never proves containment.
package processscope

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const WorkerArgument = "--internal-process-supervisor"
const childArgument = "--internal-process-child"

type Markers struct {
	Allow string
	Deny  string
}

type launch struct {
	Markers   Markers
	Argv, Env []string
	Directory string
}
type receipt struct {
	Clean bool   `json:"clean"`
	Error string `json:"error,omitempty"`
}
type Scope struct {
	config     *os.File
	proof      *os.File
	childFiles []*os.File
	payload    launch
	once       sync.Once
	result     error
}

// Prepare reserves private descriptors, never forwarded into workload code.
func Prepare(command *exec.Cmd, worker []string, markers Markers) (*Scope, error) {
	if len(worker) == 0 {
		return nil, errors.New("execution supervisor command is required")
	}
	if len(command.ExtraFiles) != 0 {
		return nil, errors.New("unexpected inherited execution descriptors")
	}
	input, config, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	proof, output, err := os.Pipe()
	if err != nil {
		input.Close()
		config.Close()
		return nil, err
	}
	s := &Scope{config: config, proof: proof, childFiles: []*os.File{input, output}, payload: launch{Markers: markers, Argv: command.Args, Env: command.Env, Directory: command.Dir}}
	command.Path = worker[0]
	command.Args = append(append([]string{}, worker...), WorkerArgument)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles = s.childFiles
	return s, nil
}
func (s *Scope) Started() error {
	for _, f := range s.childFiles {
		_ = f.Close()
	}
	s.childFiles = nil
	err := json.NewEncoder(s.config).Encode(s.payload)
	s.payload = launch{}
	_ = s.config.Close()
	return err
}
func (s *Scope) Abort() {
	for _, f := range s.childFiles {
		_ = f.Close()
	}
	s.config.Close()
	s.proof.Close()
}
func (s *Scope) Stop(pid int) error {
	err := syscall.Kill(pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func (s *Scope) Finish() error {
	s.once.Do(func() {
		defer s.proof.Close()
		done := make(chan error, 1)
		go func() {
			var result receipt
			err := json.NewDecoder(io.LimitReader(s.proof, 16384)).Decode(&result)
			if err == nil && !result.Clean {
				err = fmt.Errorf("descendant cleanup unconfirmed: %s", result.Error)
			}
			done <- err
		}()
		select {
		case s.result = <-done:
		case <-time.After(2 * time.Second):
			s.result = errors.New("descendant cleanup receipt missing")
		}
	})
	return s.result
}

// HandleWorker must be called before public argument parsing by host binaries.
func HandleWorker() bool {
	if len(os.Args) != 2 {
		return false
	}
	switch os.Args[1] {
	case childArgument:
		input := os.NewFile(3, "launch")
		var spec launch
		if json.NewDecoder(io.LimitReader(input, 8*1024*1024)).Decode(&spec) != nil {
			os.Exit(125)
		}
		input.Close()
		if len(spec.Argv) == 0 {
			os.Exit(125)
		}
		if spec.Directory != "" && os.Chdir(spec.Directory) != nil {
			os.Exit(125)
		}
		if unix.Exec(spec.Argv[0], spec.Argv, spec.Env) != nil {
			os.Exit(125)
		}
		return true
	case WorkerArgument:
		code := supervise()
		os.Exit(code)
		return true
	}
	return false
}
func supervise() int {
	config := os.NewFile(3, "configuration")
	proof := os.NewFile(4, "cleanup-receipt")
	defer config.Close()
	defer proof.Close()
	var spec launch
	if json.NewDecoder(io.LimitReader(config, 8*1024*1024)).Decode(&spec) != nil || len(spec.Argv) == 0 {
		return 125
	}
	if err := prepareNative(); err != nil {
		_ = json.NewEncoder(proof).Encode(receipt{Error: err.Error()})
		return 125
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	read, write, err := os.Pipe()
	if err != nil {
		return 125
	}
	defer read.Close()
	defer write.Close()
	binary, err := os.Executable()
	if err != nil {
		return 125
	}
	child := exec.Command(binary, childArgument)
	child.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "GORACE=atexit_sleep_ms=0"}
	child.Dir = spec.Directory
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	child.ExtraFiles = []*os.File{read}
	if child.Start() != nil {
		return 125
	}
	read.Close()
	observer, err := observeNative(child.Process.Pid, spec.Markers)
	if err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		_ = json.NewEncoder(proof).Encode(receipt{Error: err.Error()})
		return 125
	}
	defer observer.Close()
	// Register kernel tracking before the gate child can execute/fork workloads.
	if err = json.NewEncoder(write).Encode(spec); err != nil {
		_ = child.Process.Kill()
	}
	write.Close()
	spec = launch{}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	interrupted := false
	select {
	case <-done:
	case <-signals:
		interrupted = true
		_ = child.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			err = errors.New("original process did not exit")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cleanup := observer.Drain(ctx)
	err = errors.Join(err, cleanup)
	result := receipt{Clean: err == nil}
	if err != nil {
		result.Error = err.Error()
	}
	if json.NewEncoder(proof).Encode(result) != nil {
		return 125
	}
	if err != nil {
		return 125
	}
	if interrupted {
		return 130
	}
	if child.ProcessState == nil {
		return 125
	}
	code := child.ProcessState.ExitCode()
	if code < 0 {
		return 128
	}
	return code
}
