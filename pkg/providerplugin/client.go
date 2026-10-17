package providerplugin

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/sandbox"
)

type Client struct {
	metadata         info
	token, directory string
	command          *exec.Cmd
	stdin            io.WriteCloser
	done             chan struct{}
	waitErr          error
	transport        *http.Transport
	http             *http.Client
	closeOnce        sync.Once
	closeErr         error
}

func start(ctx context.Context, executable string, args []string, config Config) (*Client, error) {
	// A short, private path also fits macOS's Unix socket path limit.
	directory, err := os.MkdirTemp("/tmp", "pot-provider-")
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(directory, "rpc.sock")
	p := &Client{token: rand.Text() + rand.Text(), directory: directory, done: make(chan struct{})}
	p.command = exec.Command(executable, args...)
	p.command.Dir = filepath.Dir(executable)
	p.command.Env = safeEnvironment()
	p.command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.command.Stderr = io.Discard
	p.stdin, err = p.command.StdinPipe()
	if err != nil {
		os.RemoveAll(directory)
		return nil, err
	}
	stdout, err := p.command.StdoutPipe()
	if err != nil {
		p.stdin.Close()
		os.RemoveAll(directory)
		return nil, err
	}
	if err = p.command.Start(); err != nil {
		p.stdin.Close()
		stdout.Close()
		os.RemoveAll(directory)
		return nil, errors.New("provider executable unavailable")
	}
	go func() { p.waitErr = p.command.Wait(); close(p.done) }()
	success := false
	defer func() {
		if !success {
			_ = p.stdin.Close()
			_ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL)
			<-p.done
			_ = os.RemoveAll(directory)
		}
	}()
	if err = json.NewEncoder(p.stdin).Encode(bootstrap{Version: Version, Config: config, Socket: socket, Token: p.token}); err != nil {
		return nil, err
	}
	initialized := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(io.LimitReader(stdout, 65537))
		line, err := reader.ReadBytes('\n')
		var greeting hello
		if err == nil && len(line) <= 65536 {
			err = core.DecodeJSON(line, &greeting)
		} else if err == nil {
			err = errors.New("provider handshake exceeds limit")
		}
		if err == nil && greeting.Version != Version {
			err = errors.New("provider protocol version mismatch")
		}
		if err == nil {
			p.metadata = greeting.Info
		}
		initialized <- err
		if err == nil {
			_, _ = io.Copy(io.Discard, stdout)
		}
	}()
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case err = <-initialized:
	case <-startup.Done():
		err = startup.Err()
	}
	if err != nil {
		return nil, errors.New("provider handshake failed")
	}
	relative, err := filepath.Rel(config.Root, p.metadata.Workspace)
	if err != nil || filepath.Clean(p.metadata.Workspace) != p.metadata.Workspace || relative == "." || relative == ".." || strings.HasPrefix(relative, "../") || strings.Count(p.metadata.Workspace, "{id}") != 1 || !filepath.IsAbs(p.metadata.GuestWorkspace) || (p.metadata.Cleanup != sandbox.ProcessContainment && p.metadata.Cleanup != sandbox.SessionClosure) {
		return nil, errors.New("invalid provider workspace or cleanup contract")
	}
	p.transport = &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	p.http = &http.Client{Transport: p.transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	success = true
	return p, nil
}
func (p *Client) Descriptor() sandbox.Descriptor      { return p.metadata.Descriptor }
func (p *Client) DefaultResources() sandbox.Resources { return p.metadata.Resources }
func (p *Client) Workspace(i sandbox.Instance) string {
	return strings.ReplaceAll(p.metadata.Workspace, "{id}", i.ID)
}
func (p *Client) GuestWorkspace() string                           { return p.metadata.GuestWorkspace }
func (p *Client) CleanupPolicy() sandbox.CleanupPolicy             { return p.metadata.Cleanup }
func (p *Client) DesktopCapabilities() sandbox.DesktopCapabilities { return p.metadata.Desktop }
func (p *Client) call(ctx context.Context, call request, output any) error {
	call.Version = Version
	call.ID = rand.Text()
	data, err := json.Marshal(call)
	if err != nil {
		return err
	}
	if len(data) > maxMessage {
		return errors.New("provider request exceeds limit")
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://provider/rpc", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	reply, err := p.http.Do(req)
	if err != nil {
		return errors.Join(core.ErrCleanupUnconfirmed, errors.New("provider acknowledgement lost; inspect before retrying"), ctx.Err())
	}
	defer reply.Body.Close()
	body, err := io.ReadAll(io.LimitReader(reply.Body, maxMessage+1))
	var result response
	if err != nil || len(body) > maxMessage || reply.StatusCode != 200 || core.DecodeJSON(body, &result) != nil || result.Version != Version || result.ID != call.ID || (result.Error == nil) == (len(result.Result) == 0) {
		return errors.Join(core.ErrCleanupUnconfirmed, errors.New("invalid provider acknowledgement"))
	}
	if result.Error != nil {
		err = errors.New(result.Error.Message)
		if result.Error.CleanupUnconfirmed {
			err = errors.Join(core.ErrCleanupUnconfirmed, err)
		}
		return err
	}
	if output != nil {
		if err = core.DecodeJSON(result.Result, output); err != nil {
			return errors.Join(core.ErrCleanupUnconfirmed, err)
		}
	}
	return nil
}
func (p *Client) ValidateResources(i sandbox.Instance, r sandbox.Resources) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.call(ctx, request{Method: "validate", Instance: i, Resources: r}, nil)
}
func (p *Client) BindResources(ctx context.Context, i sandbox.Instance, r sandbox.PreparedResources) error {
	return p.call(ctx, request{Method: "bind", Instance: i, Prepared: r}, nil)
}
func (p *Client) Lifecycle(ctx context.Context, i sandbox.Instance, method string) (sandbox.Status, error) {
	var out sandbox.Status
	err := p.call(ctx, request{Method: "lifecycle", Instance: i, Operation: method}, &out)
	return out, err
}
func (p *Client) Health(ctx context.Context, i sandbox.Instance) (bool, error) {
	var out bool
	err := p.call(ctx, request{Method: "health", Instance: i}, &out)
	return out, err
}
func (p *Client) Request(ctx context.Context, i sandbox.Instance, op string, input json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	var out json.RawMessage
	err := p.call(ctx, request{Method: "request", Instance: i, Operation: op, Input: input, TimeoutMS: timeout.Milliseconds()}, &out)
	return out, err
}
func (p *Client) PrepareDesktop(ctx context.Context, i sandbox.Instance) error {
	return p.call(ctx, request{Method: "desktop_prepare", Instance: i}, nil)
}
func (p *Client) DesktopMetrics(ctx context.Context, i sandbox.Instance) (json.RawMessage, error) {
	var out json.RawMessage
	err := p.call(ctx, request{Method: "desktop_metrics", Instance: i}, &out)
	return out, err
}
func (p *Client) DesktopBrowser(ctx context.Context, i sandbox.Instance) error {
	return p.call(ctx, request{Method: "desktop_browser", Instance: i}, nil)
}
func (p *Client) OpenDesktop(ctx context.Context, i sandbox.Instance) (io.ReadWriteCloser, error) {
	data, err := json.Marshal(request{Version: Version, ID: rand.Text(), Method: "desktop_open", Instance: i})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "http://provider/desktop", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "pot-provider-desktop-v1")
	reply, err := p.http.Do(req)
	if err != nil {
		return nil, errors.New("provider desktop transport unavailable")
	}
	if reply.StatusCode != 101 || reply.Header.Get("Upgrade") != "pot-provider-desktop-v1" {
		reply.Body.Close()
		return nil, errors.New("provider desktop upgrade failed")
	}
	stream, ok := reply.Body.(io.ReadWriteCloser)
	if !ok {
		reply.Body.Close()
		return nil, errors.New("provider desktop is not duplex")
	}
	return stream, nil
}
func (p *Client) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p.closeOnce.Do(func() {
		select {
		case <-p.done:
			p.closeErr = errors.Join(core.ErrCleanupUnconfirmed, errors.New("provider exited before shutdown"))
		default:
			p.closeErr = p.call(ctx, request{Method: "shutdown"}, nil)
		}
		_ = p.stdin.Close()
		select {
		case <-p.done:
		case <-ctx.Done():
			_ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL)
			<-p.done
			p.closeErr = errors.Join(p.closeErr, core.ErrCleanupUnconfirmed, ctx.Err())
		}
		if p.waitErr != nil {
			p.closeErr = errors.Join(p.closeErr, core.ErrCleanupUnconfirmed, errors.New("provider process exited unsuccessfully"))
		}
		p.transport.CloseIdleConnections()
		p.closeErr = errors.Join(p.closeErr, os.RemoveAll(p.directory))
	})
	return p.closeErr
}
