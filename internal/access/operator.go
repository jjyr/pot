package access

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/instance"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type Operator struct {
	Socket string `json:"socket"`
	Token  string `json:"token"`
}
type OperatorServer struct {
	server   *http.Server
	listener net.Listener
	path     string
}

func StartOperator(root string, manager *Manager) (*OperatorServer, error) {
	path := filepath.Join(root, "browser-operator.sock")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("operator socket path is occupied")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	endpoint := Operator{Socket: path, Token: rand.Text()}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+endpoint.Token)) != 1 {
			http.Error(w, "operator authentication required", 401)
			return
		}
		if r.URL.Path != "/access" || (r.Method != "GET" && r.Method != "POST") {
			http.Error(w, "operator route unavailable", 404)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 65536))
		if err != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		result, err := manager.Call(r.Context(), r.Method, body)
		if err != nil {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(result)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	if err = instance.WriteJSON(filepath.Join(root, "browser-operator.json"), endpoint); err != nil {
		listener.Close()
		return nil, err
	}
	go server.Serve(listener)
	return &OperatorServer{server: server, listener: listener, path: filepath.Join(root, "browser-operator.json")}, nil
}
func (s *OperatorServer) Close() error { err := s.server.Close(); os.Remove(s.path); return err }
func ReadOperator(root string) (Operator, error) {
	var endpoint Operator
	path := filepath.Join(root, "browser-operator.json")
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return endpoint, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return endpoint, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
		return endpoint, errors.New("private browser operator descriptor required")
	}
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return endpoint, err
	}
	if len(data) > 8192 {
		return endpoint, errors.New("invalid browser operator descriptor")
	}
	if err = core.DecodeJSON(data, &endpoint); err != nil {
		return endpoint, err
	}
	if endpoint.Socket != filepath.Join(root, "browser-operator.sock") || len(endpoint.Token) < 20 {
		return endpoint, errors.New("invalid browser operator endpoint")
	}
	return endpoint, nil
}
func CallOperator(ctx context.Context, endpoint Operator, action string) (map[string]any, error) {
	method := "POST"
	var body []byte
	if action == "status" || action == "qr" {
		method = "GET"
	} else {
		var err error
		body, err = json.Marshal(map[string]string{"action": action})
		if err != nil {
			return nil, err
		}
	}
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", endpoint.Socket)
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, method, "http://operator/access", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+endpoint.Token)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("operator response too large")
	}
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		return nil, errors.New("browser operator: " + response.Status + ": " + string(data))
	}
	return result, nil
}

func (m *Manager) WatchAccess(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c, err := m.store.Load(false)
			if err != nil {
				return err
			}
			m.op.Lock()
			if m.closed {
				m.op.Unlock()
				return context.Canceled
			}
			if c.Generation > m.applied {
				m.mu.Lock()
				browser := m.browser
				m.mu.Unlock()
				err = browser.ReplaceAccess(c)
				if err == nil {
					m.applied = c.Generation
					m.settings.Enabled = !c.Disabled
				}
			}
			m.op.Unlock()
			if err != nil {
				return err
			}
		}
	}
}
