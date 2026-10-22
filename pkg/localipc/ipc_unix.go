//go:build darwin || linux

// Package localipc provides authenticated local HTTP's OS transport.
package localipc

import (
	"context"
	"errors"
	"net"
	"os"
)

func Listen(path string) (net.Listener, error) {
	if i, e := os.Lstat(path); e == nil {
		if i.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("local socket path is occupied")
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	l, e := net.Listen("unix", path)
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(path, 0600); e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}
