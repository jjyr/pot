package transport

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/localipc"
	"github.com/jjyr/pot/pkg/privatefs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Endpoint struct {
	APIVersion uint32 `json:"api_version"`
	RuntimeID  string `json:"runtime_id"`
	BootID     string `json:"boot_id"`
	Socket     string `json:"socket"`
	Token      string `json:"token"`
}
type SocketServer struct {
	Endpoint Endpoint
	server   *http.Server
	done     chan error
	path     string
	once     sync.Once
	closeErr error
}

func StartSocket(ctx context.Context, root string, app *Application) (*SocketServer, error) {
	path := filepath.Join(root, "runtime.sock")
	listener, err := localipc.Listen(path)
	if err != nil {
		return nil, err
	}
	endpoint := Endpoint{3, app.RuntimeID, app.BootID, path, rand.Text()}
	server := &http.Server{Handler: Handler(app, endpoint.Token), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384, BaseContext: func(net.Listener) context.Context { return ctx }}
	result := &SocketServer{Endpoint: endpoint, server: server, done: make(chan error, 1), path: path}
	if err = instance.WriteJSON(filepath.Join(root, "runtime.json"), endpoint); err != nil {
		listener.Close()
		return nil, err
	}
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		result.done <- err
	}()
	return result, nil
}
func (s *SocketServer) Close(ctx context.Context) error {
	s.once.Do(func() {
		err := s.server.Shutdown(ctx)
		if err != nil {
			s.server.Close()
		}
		serveErr := <-s.done
		s.closeErr = errors.Join(err, serveErr)
	})
	return s.closeErr
}
func ReadEndpoint(root string) (Endpoint, error) {
	var endpoint Endpoint
	root, err := filepath.Abs(root)
	if err != nil {
		return endpoint, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return endpoint, err
	}
	if !info.IsDir() || privatefs.Check(root) != nil {
		return endpoint, errors.New("Runtime root is not private")
	}
	path := filepath.Join(root, "runtime.json")
	info, err = os.Lstat(path)
	if err != nil {
		return endpoint, err
	}
	if !info.Mode().IsRegular() || privatefs.Check(path) != nil {
		return endpoint, errors.New("Runtime endpoint is not a private regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return endpoint, err
	}
	if err = core.DecodeJSON(body, &endpoint); err != nil {
		return endpoint, err
	}
	if endpoint.APIVersion != 3 || endpoint.Token == "" || endpoint.BootID == "" || endpoint.Socket != filepath.Join(root, "runtime.sock") {
		return endpoint, errors.New("invalid Runtime endpoint identity")
	}
	return endpoint, nil
}
func (e Endpoint) Client() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return localipc.Dial(ctx, e.Socket)
	}}, Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (e Endpoint) MarshalJSON() ([]byte, error) { type plain Endpoint; return json.Marshal(plain(e)) }
