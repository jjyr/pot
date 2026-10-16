// Package platformhelper calls the retained Swift sandbox helpers. It owns only
// bounded Unix JSON-lines transport; sandbox leases, policy and lifecycle intent
// belong to the caller. Failed exchanges are never retried: an operation may
// already have changed the VM before its acknowledgement was lost.
package platformhelper

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

type Protocol int

const (
	Containerization    Protocol = iota // PotHelper: unversioned, with sandbox_id.
	MacOSVirtualization                 // PotMacOSHelper: version 1, one enrolled VM.
)

type Client struct {
	SocketPath       string
	Protocol         Protocol
	Timeout          time.Duration
	MaxResponseBytes int
}

type RemoteError struct{ Message string }

func (e *RemoteError) Error() string { return "Swift helper: " + e.Message }

type Request struct {
	Method    string
	SandboxID string
}

// Call exchanges one request on a fresh connection, checking response identity.
// The socket must be private and owned by this process's user. Status does not
// prepare or start a VM, and is suitable for a no-VM integration probe.
func (c Client) Call(ctx context.Context, request Request) (map[string]any, error) {
	if c.Protocol != Containerization && c.Protocol != MacOSVirtualization {
		return nil, errors.New("unknown Swift helper protocol")
	}
	if !strings.HasPrefix(c.SocketPath, "/") || len(c.SocketPath) >= 104 {
		return nil, errors.New("invalid Swift helper socket path")
	}
	switch request.Method {
	case "prepare", "start", "status", "stop", "delete", "shutdown", "display":
	default:
		return nil, errors.New("unsupported Swift helper method")
	}
	if request.SandboxID != "" {
		if len(request.SandboxID) > 48 || strings.Trim(request.SandboxID, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return nil, errors.New("invalid Sandbox ID")
		}
		if c.Protocol == MacOSVirtualization {
			return nil, errors.New("macOS helper owns one enrolled VM")
		}
	}
	info, err := os.Lstat(c.SocketPath)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("Swift helper socket must be private and owned by current user")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err = conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(random)
	wire := map[string]any{"id": id, "method": request.Method}
	if c.Protocol == MacOSVirtualization {
		wire["version"] = 1
	} else if request.SandboxID != "" {
		wire["sandbox_id"] = request.SandboxID
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(conn, strings.NewReader(string(encoded)+"\n")); err != nil {
		return nil, fmt.Errorf("Swift helper acknowledgement unknown: %w", err)
	}
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = 65536
	}
	if limit > 2*1024*1024 {
		return nil, errors.New("Swift helper response limit exceeds 2 MiB")
	}
	line, err := bufio.NewReader(io.LimitReader(conn, int64(limit+1))).ReadBytes('\n')
	if err != nil || len(line) > limit || len(line) == 0 || line[len(line)-1] != '\n' {
		return nil, errors.Join(errors.New("Swift helper response incomplete; operation outcome unknown"), err, ctx.Err())
	}
	var response struct {
		Version int            `json:"version"`
		ID      string         `json:"id"`
		Result  map[string]any `json:"result"`
		Error   *string        `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	if err = decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("invalid Swift helper response; operation outcome unknown: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("multiple Swift helper response values; operation outcome unknown")
	}
	if response.ID != id || (c.Protocol == MacOSVirtualization && response.Version != 1) {
		return nil, errors.New("Swift helper response identity mismatch; operation outcome unknown")
	}
	if response.Error != nil {
		return nil, &RemoteError{*response.Error}
	}
	if response.Result == nil {
		return nil, errors.New("Swift helper omitted result; operation outcome unknown")
	}
	return response.Result, nil
}
