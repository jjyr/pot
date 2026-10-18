package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/creack/pty"
	"github.com/jjyr/pot/pkg/processscope"
	"github.com/jjyr/pot/pkg/sandbox"
	"golang.org/x/sys/unix"
)

type process struct {
	markers        processscope.Markers
	supervisor     *processscope.Scope
	mode           string
	interactive    bool
	finishSandbox  func() error
	opMu           sync.Mutex
	terminalMu     sync.Mutex
	mu             sync.Mutex
	cmd            *exec.Cmd
	input          *os.File
	terminal       *os.File
	stdout, stderr []byte
	truncated      bool
	changed        chan struct{}
	done           chan struct{}
	stop           chan struct{}
	stopOnce       sync.Once
	touched        time.Time
	exit           *int
	reason         any
	err            error
	readers        []*os.File
	stdinClosed    bool
	maxOutput      int
}

func (p *process) notify() {
	select {
	case p.changed <- struct{}{}:
	default:
	}
}
func (p *process) terminate() { p.stopOnce.Do(func() { close(p.stop) }) }
func (p *process) finished() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
func (p *process) drain(reader *os.File, stderr bool, done chan<- error) {
	defer p.closeReader(reader)
	pending := []byte{}
	chunk := make([]byte, 8192)
	for {
		n, err := reader.Read(chunk)
		pending = append(pending, chunk[:n]...)
		complete := len(pending)
		if err == nil {
			complete = completeUTF8(pending)
		}
		if complete > 0 {
			p.mu.Lock()
			buffer := &p.stdout
			if stderr {
				buffer = &p.stderr
			}
			keep := min(complete, max(0, p.maxOutput-len(*buffer)))
			*buffer = append(*buffer, pending[:keep]...)
			p.truncated = p.truncated || keep < complete
			p.mu.Unlock()
			pending = pending[complete:]
			p.notify()
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed) {
				done <- nil
			} else {
				done <- err
			}
			return
		}
	}
}
func killGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
func confirmGroupGone(pid int, deadline time.Time) error {
	for time.Now().Before(deadline) {
		err := syscall.Kill(-pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return errors.Join(ErrCleanupUnconfirmed, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("%w: process group %d remains", ErrCleanupUnconfirmed, pid)
}
func (p *process) run(idle time.Duration) {
	waited := make(chan error, 1)
	go func() { waited <- p.cmd.Wait() }()
	readerDone := make(chan error, len(p.readers))
	for index, reader := range p.readers {
		go p.drain(reader, index == 1, readerDone)
	}
	idleTimer := time.NewTimer(idle)
	defer idleTimer.Stop()
	var waitErr error
	exited := false
	var reason any
waiting:
	for {
		select {
		case waitErr = <-waited:
			exited = true
			break waiting
		case <-p.stop:
			reason = "cancelled"
			break waiting
		case <-idleTimer.C:
			p.mu.Lock()
			remaining := idle - time.Since(p.touched)
			p.mu.Unlock()
			if remaining > 0 {
				idleTimer.Reset(remaining)
			} else {
				reason = "idle_timeout"
				break waiting
			}
		}
	}
	var cleanupErr error
	if p.supervisor != nil && !exited {
		cleanupErr = p.supervisor.Stop(p.cmd.Process.Pid)
	} else {
		cleanupErr = killGroup(p.cmd.Process.Pid)
	}
	if !exited {
		select {
		case waitErr = <-waited:
		case <-time.After(5 * time.Second):
			cleanupErr = errors.Join(cleanupErr, ErrCleanupUnconfirmed)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	if p.supervisor != nil {
		cleanupErr = errors.Join(cleanupErr, p.supervisor.Finish())
	}
	cleanupErr = errors.Join(cleanupErr, confirmGroupGone(p.cmd.Process.Pid, deadline))
	for range p.readers {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			for _, reader := range p.readers {
				p.closeReader(reader)
			}
			cleanupErr = errors.Join(cleanupErr, ErrCleanupUnconfirmed)
			break
		}
		select {
		case err := <-readerDone:
			cleanupErr = errors.Join(cleanupErr, err)
		case <-time.After(remaining):
			for _, reader := range p.readers {
				p.closeReader(reader)
			}
			cleanupErr = errors.Join(cleanupErr, ErrCleanupUnconfirmed)
		}
	}
	if p.input != nil && p.terminal == nil {
		p.input.Close()
	}
	if p.terminal != nil {
		p.closeReader(p.terminal)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reason = reason
	if exited && p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		code := p.cmd.ProcessState.ExitCode()
		p.exit = &code
	}
	var exitError *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitError) {
		cleanupErr = errors.Join(cleanupErr, waitErr)
	}
	if cleanupErr != nil {
		p.err = errors.Join(ErrCleanupUnconfirmed, cleanupErr)
	}
	if p.finishSandbox != nil && p.err == nil {
		if err := p.finishSandbox(); err != nil {
			p.err = errors.Join(p.err, ErrCleanupUnconfirmed, err)
		}
	}
	close(p.done)
	p.notify()
}

func (e *Engine) startProcess(ctx context.Context, r Request) (string, *process, error) {
	// Check before shell snapshots, which can themselves run login code.
	if r.Args["tty"] == true && r.Mode == "host" && r.Args["interactive"] != true {
		return "", nil, &NotStartedError{Cause: errors.New("host TTY requires interactive:true on the original exec_command; command was not started. Set tty:true and interactive:true together, or omit tty for noninteractive commands")}
	}
	policy := r.Policy
	var err error
	shell := policy.Executable
	if value, ok := r.Args["shell"].(string); ok {
		shell = value
	}
	if !strings.HasPrefix(shell, "/") || strings.ContainsRune(shell, 0) {
		return "", nil, errors.New("shell must be absolute")
	}
	parts := strings.Split(shell, "/")
	switch parts[len(parts)-1] {
	case "sh", "bash", "zsh", "dash":
	default:
		return "", nil, errors.New("unsupported shell; use sh/bash/zsh")
	}
	login := policy.AllowLoginShell
	if value, ok := r.Args["login"].(bool); ok {
		login = value
	}
	if login && !policy.AllowLoginShell {
		return "", nil, errors.New("login shell disabled by execution policy")
	}
	workdir := r.CWD
	if value, ok := r.Args["workdir"].(string); ok {
		if value == "" {
			return "", nil, errors.New("invalid workdir")
		}
		workdir = value
	}
	workdir, err = resolveWorkingDirectory(workdir, r.CWD)
	if err != nil {
		return "", nil, err
	}
	home := e.options.Home
	if home == "" {
		home = r.CWD
	}
	var command *exec.Cmd
	if r.Mode == "sandbox" || e.options.Admit != nil {
		flag := "-c"
		if login {
			flag = "-lc"
		}
		command = exec.Command(shell, flag, r.Args["cmd"].(string))
		command.Dir = workdir
		command.Env = sandbox.Environment()
		if r.Mode == "host" {
			command.Env = append(command.Env, "HOME="+home)
		}
	} else {
		command, err = e.snapshots.command(ctx, shell, workdir, home, r.Args["cmd"].(string), login, policy.ShellSnapshot)
	}
	if err != nil {
		return "", nil, err
	}
	command.Env = mergeEnvironment(command.Env, r.Environment)
	p := &process{mode: r.Mode, interactive: r.Args["interactive"] == true, cmd: command, changed: make(chan struct{}, 1), done: make(chan struct{}), stop: make(chan struct{}), touched: time.Now(), maxOutput: int(policy.MaxOutputBytes)}

	if r.Mode == "session" {
		return "", nil, errors.New("session authority cannot launch a new command")
	}
	if r.Mode == "sandbox" {
		launcher := e.sandboxLauncher()
		launcher.Markers = &p.markers
		p.finishSandbox, err = launcher.Prepare(ctx, command, r.CWD, nil, nil, r.Args["tty"] == true)
		if err != nil {
			return "", nil, &PreparationError{Cause: err}
		}
	}
	if len(e.options.ProcessWorker) > 0 {
		p.supervisor, err = processscope.Prepare(command, e.options.ProcessWorker, p.markers)
		if err != nil {
			if p.finishSandbox != nil {
				_ = p.finishSandbox()
			}
			return "", nil, err
		}
	}
	started := false
	defer func() {
		if !started && p.supervisor != nil {
			p.supervisor.Abort()
		}
		if !started && p.finishSandbox != nil {
			_ = p.finishSandbox()
		}
	}()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.draining || e.fence != nil {
		return "", nil, errors.Join(errors.New("execution scope ended"), e.fence)
	}
	for id, existing := range e.sessions {
		if existing.finished() {
			if existing.err != nil {
				e.fence = errors.Join(e.fence, existing.err)
				return "", nil, e.fence
			}
			if uint64(len(e.sessions)) >= policy.MaxSessions {
				delete(e.sessions, id)
			}
		}
	}
	if uint64(len(e.sessions)) >= policy.MaxSessions {
		return "", nil, errors.New("process session limit reached")
	}

	var release func()
	if e.options.Admit != nil {
		release, err = e.options.Admit(r.Revision)
		if err != nil {
			return "", nil, err
		}
		defer func() {
			if release != nil {
				release()
			}
		}()
	}
	tty, _ := r.Args["tty"].(bool)
	if tty {
		terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 24, Cols: 80})
		if err != nil {
			return "", nil, err
		}
		// creack/pty's master starts in blocking mode. Rewrap a nonblocking
		// duplicate so Go's poller can interrupt blocked reads/writes on cleanup.
		fd, err := unix.Dup(int(terminal.Fd()))
		if err == nil {
			unix.CloseOnExec(fd)
			err = unix.SetNonblock(fd, true)
		}
		if err != nil {
			if fd >= 0 {
				unix.Close(fd)
			}
			terminal.Close()
			killGroup(command.Process.Pid)
			command.Wait()
			return "", nil, err
		}
		master := os.NewFile(uintptr(fd), terminal.Name())
		terminal.Close()
		p.terminal = master
		p.input = master
		p.readers = []*os.File{master}
	} else {
		stdinRead, stdinWrite, err := os.Pipe()
		if err != nil {
			return "", nil, err
		}
		stdoutRead, stdoutWrite, err := os.Pipe()
		if err != nil {
			stdinRead.Close()
			stdinWrite.Close()
			return "", nil, err
		}
		stderrRead, stderrWrite, err := os.Pipe()
		if err != nil {
			stdinRead.Close()
			stdinWrite.Close()
			stdoutRead.Close()
			stdoutWrite.Close()
			return "", nil, err
		}
		command.Stdin = stdinRead
		command.Stdout = stdoutWrite
		command.Stderr = stderrWrite
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		err = command.Start()
		stdinRead.Close()
		stdoutWrite.Close()
		stderrWrite.Close()
		if err != nil {
			stdinWrite.Close()
			stdoutRead.Close()
			stderrRead.Close()
			return "", nil, err
		}
		p.input = stdinWrite
		p.readers = []*os.File{stdoutRead, stderrRead}
	}
	if p.supervisor != nil {
		if err = p.supervisor.Started(); err != nil {
			_ = killGroup(command.Process.Pid)
			_ = command.Wait()
			return "", nil, err
		}
	}
	if release != nil {
		release()
		release = nil
	}
	id := identity()
	started = true
	e.sessions[id] = p
	go p.run(time.Duration(policy.SessionIdleTimeoutMS) * time.Millisecond)
	return id, p, nil
}
func (e *Engine) executeProcess(ctx context.Context, r Request, emit func(Event) error) (map[string]any, error) {
	cap, err := number(r.Args, "max_output_chars", r.Policy.DefaultOutputBytes, r.Policy.MaxOutputBytes)
	if err != nil {
		return nil, err
	}
	wait, err := number(r.Args, "yield_time_ms", r.Policy.DefaultYieldMS, r.Policy.MaxYieldMS)
	if err != nil {
		return nil, err
	}
	var id string
	var p *process
	if r.Operation == "exec_command" {
		id, p, err = e.startProcess(ctx, r)
		if err != nil {
			return nil, err
		}
	} else {
		id = r.Args["session_id"].(string)
		e.mu.Lock()
		p = e.sessions[id]
		e.mu.Unlock()
		if p == nil {
			return nil, errors.New("process session expired or missing in this agent session")
		}
	}
	p.opMu.Lock()
	defer p.opMu.Unlock()
	p.mu.Lock()
	p.touched = time.Now()
	p.mu.Unlock()
	if !p.finished() {
		if r.Args["terminate"] == true {
			p.terminate()
		}
		if r.Args["interrupt"] == true || r.Args["chars"] == "\x03" {
			group := p.cmd.Process.Pid
			if p.terminal != nil {
				var foreground int
				err := p.terminalControl(func(fd int) error { var err error; foreground, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP); return err })
				if err != nil {
					return nil, err
				}
				group = foreground
			}
			if err := syscall.Kill(-group, syscall.SIGINT); err != nil && !errors.Is(err, syscall.ESRCH) {
				return nil, err
			}
		}
		if r.Args["rows"] != nil || r.Args["cols"] != nil {
			if p.terminal == nil {
				return nil, errors.New("resize requires a PTY")
			}
			err := p.terminalControl(func(fd int) error {
				size, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
				if err != nil {
					return err
				}
				if value, ok := r.Args["rows"]; ok {
					n, _ := integer(value)
					size.Row = uint16(n)
				}
				if value, ok := r.Args["cols"]; ok {
					n, _ := integer(value)
					size.Col = uint16(n)
				}
				return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, size)
			})
			if err != nil {
				return nil, err
			}
		}
		if chars, ok := r.Args["chars"].(string); ok && chars != "" && chars != "\x03" {
			if p.mode == "host" && !p.interactive {
				return nil, errors.New("host stdin requires an explicitly authorized interactive session")
			}
			if p.input == nil || p.stdinClosed {
				return nil, errors.New("process stdin is closed")
			}
			written := make(chan error, 1)
			go func() {
				if e.options.Admit != nil {
					release, err := e.options.Admit(r.Revision)
					if err != nil {
						written <- err
						return
					}
					defer release()
				}
				_, err := io.WriteString(p.input, chars)
				written <- err
			}()
			select {
			case err := <-written:
				if err != nil {
					return nil, err
				}
			case <-ctx.Done():
				p.terminate()
				return nil, e.waitCancelled(p, ctx.Err())
			case <-time.After(time.Duration(r.Policy.MaxYieldMS) * time.Millisecond):
				p.terminate()
				return nil, e.waitCancelled(p, errors.New("process stdin write timed out"))
			}
		}
		if r.Args["close_stdin"] == true {
			if p.terminal != nil {
				if _, err := p.input.Write([]byte{4}); err != nil {
					return nil, err
				}
			} else if p.input != nil {
				p.input.Close()
				p.stdinClosed = true
			}
		}
	}
	var stdout, stderr []byte
	truncated := false
	collect := func() error {
		p.mu.Lock()
		a, b, overflow := p.stdout, p.stderr, p.truncated
		p.stdout = nil
		p.stderr = nil
		p.truncated = false
		p.mu.Unlock()
		truncated = truncated || overflow
		for index, chunk := range [][]byte{a, b} {
			dest := &stdout
			stream := "stdout"
			if index == 1 {
				dest = &stderr
				stream = "stderr"
			}
			keep := min(len(chunk), max(0, int(cap)-len(*dest)))
			before := display(*dest)
			*dest = append(*dest, chunk[:keep]...)
			after := display(*dest)
			delta := strings.TrimPrefix(after, before)
			if delta != "" && emit != nil {
				if err := emit(outputEvent(r.ID, stream, delta)); err != nil {
					return err
				}
			}
			truncated = truncated || keep < len(chunk)
		}
		return nil
	}
	timer := time.NewTimer(time.Duration(wait) * time.Millisecond)
	defer timer.Stop()
waiting:
	for {
		if err := collect(); err != nil {
			p.terminate()
			return nil, e.waitCancelled(p, err)
		}
		if p.finished() {
			break
		}
		select {
		case <-p.changed:
		case <-p.done:
		case <-timer.C:
			break waiting
		case <-ctx.Done():
			p.terminate()
			return nil, e.waitCancelled(p, ctx.Err())
		}
	}
	if err := collect(); err != nil {
		p.terminate()
		return nil, e.waitCancelled(p, err)
	}
	var session any = id
	var exit any
	var reason any
	p.mu.Lock()
	if p.exit != nil {
		exit = *p.exit
	}
	reason = p.reason
	p.mu.Unlock()
	if p.finished() {
		session = nil
		e.mu.Lock()
		delete(e.sessions, id)
		e.mu.Unlock()
		if p.err != nil {
			e.fenceCleanup(p.err)
			return nil, p.err
		}
	}
	status := "finished"
	if session != nil {
		status = "running"
	}
	return map[string]any{"execution_mode": p.mode, "execution_status": status, "stdout": strings.ToValidUTF8(string(stdout), "�"), "stderr": strings.ToValidUTF8(string(stderr), "�"), "truncated": truncated, "exit_code": exit, "reason": reason, "session_id": session}, nil
}
func completeUTF8(data []byte) int {
	offset := 0
	for offset < len(data) {
		if !utf8.FullRune(data[offset:]) {
			return offset
		}
		_, size := utf8.DecodeRune(data[offset:])
		offset += size
	}
	return offset
}
func display(data []byte) string {
	return strings.ToValidUTF8(string(data[:completeUTF8(data)]), "�")
}
func (e *Engine) waitCancelled(p *process, cause error) error {
	select {
	case <-p.done:
		if p.err != nil {
			e.fenceCleanup(p.err)
		}
		return errors.Join(cause, p.err)
	case <-time.After(11 * time.Second):
		err := errors.Join(ErrCleanupUnconfirmed, cause)
		e.fenceCleanup(err)
		return err
	}
}
func (e *Engine) Inspect() (map[string]any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fence != nil {
		return nil, e.fence
	}
	ids := []string{}
	active := false
	for id, p := range e.sessions {
		ids = append(ids, id)
		if p.finished() {
			if p.err != nil {
				e.fence = errors.Join(e.fence, p.err)
				return nil, e.fence
			}
		} else {
			active = true
		}
	}
	pluginIDs, err := e.plugins.inventory()
	if err != nil {
		return nil, err
	}
	ids = append(ids, pluginIDs...)
	active = active || len(pluginIDs) > 0
	sort.Strings(ids)
	return map[string]any{"sessions": ids, "active": active, "ended": e.closed}, nil
}

// Drain closes this turn's sessions while preserving the bounded login cache.
// The caller must serialize turn lifecycle; new operations are rejected while it
// drains. Close also permanently invalidates the Agent/epoch scope.
func (e *Engine) Drain() error { return e.drain(false) }
func (e *Engine) Close() error { return e.drain(true) }
func (e *Engine) drain(end bool) error {
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	e.mu.Lock()
	e.closed = e.closed || end
	e.draining = true
	e.cancel()
	processes := make([]*process, 0, len(e.sessions))
	for _, p := range e.sessions {
		p.terminate()
		processes = append(processes, p)
	}
	e.mu.Unlock()
	var result error
	for _, p := range processes {
		result = errors.Join(result, e.waitCancelled(p, nil))
	}
	result = errors.Join(result, e.plugins.close())
	settled := make(chan struct{})
	go func() { e.inflight.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(11 * time.Second):
		result = errors.Join(result, ErrCleanupUnconfirmed)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fence = errors.Join(e.fence, result)
	if e.fence == nil {
		e.sessions = map[string]*process{}
		if !e.closed {
			e.scope, e.cancel = context.WithCancel(context.Background())
		}
	}
	e.draining = false
	return e.fence
}

func (p *process) closeReader(reader *os.File) {
	if reader == p.terminal {
		p.terminalMu.Lock()
		defer p.terminalMu.Unlock()
	}
	_ = reader.Close()
}

func (p *process) terminalControl(operation func(int) error) error {
	p.terminalMu.Lock()
	defer p.terminalMu.Unlock()
	raw, err := p.terminal.SyscallConn()
	if err != nil {
		return err
	}
	var result error
	if err := raw.Control(func(fd uintptr) { result = operation(int(fd)) }); err != nil {
		return err
	}
	return result
}
