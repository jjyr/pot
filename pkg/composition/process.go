package composition

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

// WorkerArgument is the private mode understood by Pot's product executables.
// The standalone pot-composition-worker command does not require this argument.
const WorkerArgument = "--internal-composition-worker"
const workerFrameLimit = 6 * 1024 * 1024

var ErrMemoryThreshold = errors.New("composition worker exceeded sampled heap threshold")

// ErrWorkerCommandRequired means that the host has not configured an isolated
// composition worker. ProcessRunner never assumes the host executable implements
// the worker protocol.
var ErrWorkerCommandRequired = errors.New("composition requires an explicit worker command")

type frame struct {
	Kind        string          `json:"kind"`
	ID          int             `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Code        string          `json:"code,omitempty"`
	Names       []string        `json:"names,omitempty"`
	Limits      Limits          `json:"limits,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
	Text        string          `json:"text,omitempty"`
	Error       string          `json:"error,omitempty"`
	Unconfirmed bool            `json:"unconfirmed,omitempty"`
}
type frameWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *frameWriter) send(f frame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return json.NewEncoder(w.writer).Encode(f)
}
func frameScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), workerFrameLimit)
	return s
}
func decodeFrame(data []byte) (frame, error) {
	var f frame
	err := json.Unmarshal(data, &f)
	return f, err
}

// ProcessRunner isolates each VM in a disposable Go process. Permissions and
// effects remain in the parent through JSON tool RPC. The worker has a 64 MiB Go
// soft memory target and cancellation at a sampled 96 MiB live-heap threshold.
// Neither is a hard per-VM heap/RSS limit: an allocation can overshoot between
// samples. Worker failure cannot corrupt the parent's Go heap. A process failure
// cancels child tools and requires confirmed cleanup before returning success.
type ProcessRunner struct {
	Limits Limits
	// Command contains the worker executable and its arguments. For example,
	// []string{"/path/to/pot-composition-worker"}. The executable must serve the
	// ServeWorker protocol over stdin/stdout; use an absolute path when embedding.
	Command []string
}

func (r ProcessRunner) Run(ctx context.Context, code string, tools map[string]Tool, emit func(Event) error) error {
	limits, err := r.Limits.defaults()
	if err != nil {
		return err
	}
	if len(code) == 0 || len(code) > limits.CodeBytes {
		return ErrCodeLimit
	}
	names := make([]string, 0, len(tools))
	for name, tool := range tools {
		if name == "exec" || name == "wait" || name == "" || tool == nil {
			return errors.New("invalid composition capability")
		}
		names = append(names, name)
	}
	command := r.Command
	if len(command) == 0 || command[0] == "" {
		return ErrWorkerCommandRequired
	}
	ctx, cancel := context.WithTimeoutCause(ctx, limits.WallTime, ErrWallTimeLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Do not inherit provider keys, user configuration or language runtime paths.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return errors.New("composition worker unavailable")
	}
	writer := &frameWriter{writer: input}
	cleanup := func() { cancel(); input.Close() }
	if err = writer.send(frame{Kind: "start", Code: code, Names: names, Limits: limits}); err != nil {
		cleanup()
		cmd.Wait()
		return errors.New("composition worker initialization failed")
	}
	toolctx, toolcancel := context.WithCancel(ctx)
	defer toolcancel()
	var calls sync.WaitGroup
	var activeMu sync.Mutex
	active, total := 0, 0
	seen := map[int]bool{}
	var fatal error
	var fatalMu sync.Mutex
	recordFatal := func(e error) {
		fatalMu.Lock()
		if fatal == nil {
			fatal = e
		}
		fatalMu.Unlock()
		cancel()
	}
	scanner := frameScanner(output)
	finished := false
	outputBytes := 0
	var result error
	for scanner.Scan() {
		f, err := decodeFrame(scanner.Bytes())
		if err != nil {
			result = errors.New("invalid composition worker frame")
			break
		}
		switch f.Kind {
		case "call":
			activeMu.Lock()
			active++
			total++
			tooMany := active > limits.ConcurrentCalls || total > limits.Calls
			activeMu.Unlock()
			tool, ok := tools[f.Name]
			if !ok || seen[f.ID] || f.ID < 1 || tooMany || len(f.Args) > limits.JSONBytes || !json.Valid(f.Args) {
				result = errors.New("invalid composition worker tool request")
				break
			}
			seen[f.ID] = true
			calls.Add(1)
			go func(f frame, tool Tool) {
				defer calls.Done()
				value := invoke(toolctx, tool, f.Args, limits.JSONBytes)
				activeMu.Lock()
				active--
				activeMu.Unlock()
				response := frame{Kind: "result", ID: f.ID, Value: value.value}
				if value.err != nil {
					response.Error = value.err.Error()
					var uncertain *UnconfirmedCleanupError
					response.Unconfirmed = errors.As(value.err, &uncertain)
					if response.Unconfirmed {
						recordFatal(value.err)
					}
				}
				if err := writer.send(response); err != nil && toolctx.Err() == nil {
					recordFatal(errors.New("composition worker tool channel failed"))
				}
			}(f, tool)
		case "text", "yield":
			outputBytes += len(f.Text)
			if outputBytes > limits.OutputBytes {
				result = ErrOutputLimit
				break
			}
			if emit != nil {
				result = emit(Event{Type: f.Kind, Text: f.Text})
			}
		case "finish":
			finished = true
			if f.Error != "" {
				result = fmt.Errorf("composition worker: %s", f.Error)
				if f.Unconfirmed {
					result = &UnconfirmedCleanupError{Cause: result}
				}
			}
		default:
			result = errors.New("unexpected composition worker frame")
		}
		if result != nil || finished {
			break
		}
	}
	priorCause := context.Cause(ctx)
	toolcancel()
	cleanup()
	waitErr := cmd.Wait()
	settled := make(chan struct{})
	go func() { calls.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(2 * time.Second):
		return &UnconfirmedCleanupError{Cause: errors.New("composition child tools did not settle")}
	}
	fatalMu.Lock()
	fatalErr := fatal
	fatalMu.Unlock()
	if fatalErr != nil {
		return fatalErr
	}
	if result != nil {
		return result
	}
	if !finished {
		if priorCause != nil {
			return priorCause
		}
		if cause := context.Cause(toolctx); cause != nil && cause != context.Canceled {
			return cause
		}
		if scanner.Err() != nil {
			return errors.New("composition worker output failed")
		}
		return fmt.Errorf("composition worker stopped before completion: %v", waitErr)
	}
	return nil
}

// ServeWorker serves a single isolated composition execution. Call it from a
// dedicated worker command or an explicit worker mode in a host executable.
// Input/output carry a private protocol channel, never a user REPL. It changes
// the worker process's Go memory target while running, so do not serve workers
// inside the parent application's process.
func ServeWorker(ctx context.Context, input io.Reader, output io.Writer) error {
	scanner := frameScanner(input)
	if !scanner.Scan() {
		return errors.New("missing worker initialization")
	}
	start, err := decodeFrame(scanner.Bytes())
	if err != nil || start.Kind != "start" {
		return errors.New("invalid worker initialization")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	previous := debug.SetMemoryLimit(64 * 1024 * 1024)
	defer debug.SetMemoryLimit(previous)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				if stats.HeapAlloc > 96*1024*1024 {
					cancel(ErrMemoryThreshold)
					return
				}
			}
		}
	}()
	writer := &frameWriter{writer: output}
	var mu sync.Mutex
	next := 0
	pending := map[int]chan frame{}
	// The reader never touches VM values; each completion enters Runner's owner.
	go func() {
		for scanner.Scan() {
			f, err := decodeFrame(scanner.Bytes())
			if err != nil || f.Kind != "result" {
				cancel(errors.New("invalid parent result"))
				return
			}
			mu.Lock()
			ch := pending[f.ID]
			delete(pending, f.ID)
			mu.Unlock()
			if ch == nil {
				cancel(errors.New("unknown parent result"))
				return
			}
			ch <- f
		}
		cancel(io.EOF)
	}()
	tools := map[string]Tool{}
	for _, name := range start.Names {
		if tools[name] != nil {
			return errors.New("duplicate worker capability")
		}
		tools[name] = func(ctx context.Context, args json.RawMessage) (any, error) {
			mu.Lock()
			next++
			id := next
			ch := make(chan frame, 1)
			pending[id] = ch
			mu.Unlock()
			if err := writer.send(frame{Kind: "call", ID: id, Name: name, Args: args}); err != nil {
				return nil, err
			}
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case f := <-ch:
				if f.Error != "" {
					err := errors.New(f.Error)
					if f.Unconfirmed {
						return nil, &UnconfirmedCleanupError{Cause: err}
					}
					return nil, err
				}
				return f.Value, nil
			}
		}
	}
	err = (Runner{Limits: start.Limits}).Run(ctx, start.Code, tools, func(event Event) error { return writer.send(frame{Kind: event.Type, Text: event.Text}) })
	finish := frame{Kind: "finish"}
	if err != nil {
		finish.Error = err.Error()
		var uncertain *UnconfirmedCleanupError
		finish.Unconfirmed = errors.As(err, &uncertain)
	}
	return writer.send(finish)
}
