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
const startupTimeout = 5 * time.Second

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
	startupDone      chan struct{}
	startupOnce      sync.Once
	startupMu        sync.Mutex
	stopRequested    bool
	startupCancelled bool
	config           *os.File
	proof            *os.File
	childFiles       []*os.File
	payload          launch
	once             sync.Once
	result           error
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
	s := &Scope{startupDone: make(chan struct{}), config: config, proof: proof, childFiles: []*os.File{input, output}, payload: launch{Markers: markers, Argv: command.Args, Env: command.Env, Directory: command.Dir}}
	command.Path = worker[0]
	command.Args = append(append([]string{}, worker...), WorkerArgument)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "GORACE=atexit_sleep_ms=0"}
	command.ExtraFiles = s.childFiles
	return s, nil
}

// Started confirms native preparation and signal handling before authorizing the
// workload. A stop requested during startup instead authorizes clean cancellation.
func (s *Scope) Started() error {
	defer s.startupOnce.Do(func() { close(s.startupDone) })
	defer func() { s.payload = launch{}; _ = s.config.Close() }()
	for _, f := range s.childFiles {
		_ = f.Close()
	}
	s.childFiles = nil
	deadline := time.Now().Add(startupTimeout)
	if err := s.config.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("supervisor configuration deadline: %w", err)
	}
	if err := s.proof.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("supervisor readiness deadline: %w", err)
	}
	if err := json.NewEncoder(s.config).Encode(s.payload); err != nil {
		return fmt.Errorf("supervisor configuration: %w", err)
	}
	var ready [1]byte
	_, readErr := io.ReadFull(s.proof, ready[:])
	resetErr := s.proof.SetReadDeadline(time.Time{})
	if err := errors.Join(readErr, resetErr); err != nil {
		return fmt.Errorf("supervisor readiness: %w", err)
	}
	if ready[0] != 'R' {
		return fmt.Errorf("unexpected supervisor readiness byte %q", ready[0])
	}
	// Serialize the final launch decision with early cancellation. No fallible
	// operation follows a successful ACK: before ACK there is no workload.
	s.startupMu.Lock()
	defer s.startupMu.Unlock()
	decision := byte('A')
	if s.stopRequested {
		decision = 'C'
	}
	if _, err := s.config.Write([]byte{'"', decision, '"', '\n'}); err != nil {
		return fmt.Errorf("supervisor startup decision: %w", err)
	}
	s.startupCancelled = s.stopRequested
	return nil
}
func (s *Scope) Abort() {
	for _, f := range s.childFiles {
		_ = f.Close()
	}
	s.config.Close()
	s.proof.Close()
	s.startupOnce.Do(func() { close(s.startupDone) })
}

// Stop requests cancellation; only Finish supplies cleanup proof.
func (s *Scope) Stop(pid int) error {
	s.startupMu.Lock()
	s.stopRequested = true
	s.startupMu.Unlock()
	timer := time.NewTimer(startupTimeout)
	defer timer.Stop()
	select {
	case <-s.startupDone:
	case <-timer.C:
		return errors.New("supervisor startup did not settle before cancellation")
	}
	s.startupMu.Lock()
	cancelled := s.startupCancelled
	s.startupMu.Unlock()
	if cancelled {
		return nil
	}
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
	decoder := json.NewDecoder(io.LimitReader(config, 8*1024*1024))
	if decoder.Decode(&spec) != nil || len(spec.Argv) == 0 {
		return 125
	}
	if err := prepareNative(); err != nil {
		_ = json.NewEncoder(proof).Encode(receipt{Error: err.Error()})
		return 125
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	// Ready and the final cleanup receipt share a private pipe. The parent reads
	// exactly one ready byte, leaving the JSON cleanup proof untouched.
	if _, err := proof.Write([]byte{'R'}); err != nil {
		return 125
	}
	var decision string
	// Preserve decoder buffering and whitespace across the configuration and
	// decision frames. Until launch authorization, no workload may be started.
	if decoder.Decode(&decision) != nil {
		return 125
	}
	switch decision {
	case "C":
		if json.NewEncoder(proof).Encode(receipt{Clean: true}) != nil {
			return 125
		}
		return 130
	case "A":
	default:
		return 125
	}
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
		if stopErr := cancelOriginal(observer, child.Process, done); stopErr != nil {
			err = stopErr
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

func stopOriginal(original *os.Process, done <-chan error) error {
	_ = original.Kill()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("original process did not exit")
	}
}
