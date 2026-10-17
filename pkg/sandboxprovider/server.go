package sandboxprovider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/sandbox"
)

// Serve is the entry point for a provider executable. Factory must only register
// the provider; guest creation and downloads require explicit later requests.
func Serve(factory func(Config) (sandbox.Provider, error)) error {
	input := bufio.NewReader(io.LimitReader(os.Stdin, 65537))
	line, err := input.ReadBytes('\n')
	if err != nil || len(line) > 65536 {
		return errors.New("invalid provider bootstrap")
	}
	var boot bootstrap
	if err = core.DecodeJSON(line, &boot); err != nil {
		return err
	}
	if boot.Version != Version || !filepath.IsAbs(boot.Config.Root) || !filepath.IsAbs(boot.Socket) || len(boot.Token) < 32 {
		return errors.New("unsupported provider bootstrap")
	}
	provider, err := factory(boot.Config)
	if err != nil {
		return err
	}
	listener, err := net.Listen("unix", boot.Socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(boot.Socket)
	if err = os.Chmod(boot.Socket, 0600); err != nil {
		return err
	}
	capabilities := sandbox.DesktopCapabilities{}
	if desktop, ok := provider.(sandbox.DesktopProvider); ok {
		capabilities = desktop.DesktopCapabilities()
	}
	metadata := info{Descriptor: provider.Descriptor(), Resources: provider.DefaultResources(), Workspace: provider.Workspace(sandbox.Instance{Root: boot.Config.Root, ID: "{id}"}), GuestWorkspace: provider.GuestWorkspace(), Cleanup: provider.CleanupPolicy(), Desktop: capabilities}
	stopping := make(chan struct{})
	var stopOnce sync.Once
	stop := func() { stopOnce.Do(func() { close(stopping) }) }
	var cleanupOnce sync.Once
	var cleanupErr error
	shutdown := func(ctx context.Context) error {
		cleanupOnce.Do(func() { cleanupErr = provider.Shutdown(ctx) })
		return cleanupErr
	}
	serviceContext, cancelService := context.WithCancel(context.Background())
	defer cancelService()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return serviceContext }}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || (r.URL.Path != "/rpc" && r.URL.Path != "/desktop") || r.Header.Get("Authorization") != "Bearer "+boot.Token {
			http.Error(w, "provider request denied", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxMessage+1))
		if err != nil || len(body) > maxMessage {
			http.Error(w, "provider request too large", http.StatusBadRequest)
			return
		}
		var call request
		if err = core.DecodeJSON(body, &call); err != nil || call.Version != Version || !core.Identifier(call.ID) {
			http.Error(w, "invalid provider protocol", http.StatusBadRequest)
			return
		}
		if call.Method != "shutdown" && (!core.Identifier(call.Instance.ID) || call.Instance.Root != boot.Config.Root) {
			http.Error(w, "invalid provider instance", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/desktop" {
			desktop, ok := provider.(sandbox.DesktopProvider)
			if !ok || call.Method != "desktop_open" || r.Header.Get("Upgrade") != "pot-provider-desktop-v1" {
				http.Error(w, "desktop unavailable", http.StatusBadRequest)
				return
			}
			stream, err := desktop.OpenDesktop(r.Context(), call.Instance)
			if err != nil {
				writeResponse(w, call.ID, nil, err)
				return
			}
			defer stream.Close()
			conn, buffer, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, err = fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: pot-provider-desktop-v1\r\n\r\n")
			if err != nil {
				return
			}
			if buffer.Flush() != nil {
				return
			}
			done := make(chan struct{})
			go func() { _, _ = io.Copy(stream, buffer); _ = stream.Close(); _ = conn.Close(); close(done) }()
			_, _ = io.Copy(conn, stream)
			_ = stream.Close()
			_ = conn.Close()
			<-done
			return
		}
		var result any = true
		switch call.Method {
		case "validate":
			err = provider.ValidateResources(call.Instance, call.Resources)
		case "bind":
			err = provider.BindResources(r.Context(), call.Instance, call.Prepared)
		case "lifecycle":
			result, err = provider.Lifecycle(r.Context(), call.Instance, call.Operation)
		case "health":
			result, err = provider.Health(r.Context(), call.Instance)
		case "request":
			if call.TimeoutMS < 1 || call.TimeoutMS > int64((30*time.Minute)/time.Millisecond) {
				err = errors.New("invalid guest request timeout")
			} else {
				result, err = provider.Request(r.Context(), call.Instance, call.Operation, call.Input, time.Duration(call.TimeoutMS)*time.Millisecond)
			}
		case "desktop_prepare", "desktop_metrics", "desktop_browser":
			desktop, ok := provider.(sandbox.DesktopProvider)
			if !ok {
				err = errors.New("desktop unavailable")
				break
			}
			switch call.Method {
			case "desktop_prepare":
				err = desktop.PrepareDesktop(r.Context(), call.Instance)
			case "desktop_metrics":
				result, err = desktop.DesktopMetrics(r.Context(), call.Instance)
			case "desktop_browser":
				err = desktop.DesktopBrowser(r.Context(), call.Instance)
			}
		case "shutdown":
			err = shutdown(r.Context())
			defer stop()
		default:
			err = errors.New("unsupported provider method")
		}
		writeResponse(w, call.ID, result, err)
	})
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	if err = json.NewEncoder(os.Stdout).Encode(hello{Version: Version, Info: metadata}); err != nil {
		stop()
	}
	// Parent loss closes stdin. Never keep an orphaned provider process alive.
	go func() { _, _ = io.Copy(io.Discard, input); stop() }()
	select {
	case <-stopping:
	case err = <-served:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	cancelService()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	closeErr := server.Shutdown(ctx)
	return errors.Join(err, closeErr, shutdown(ctx))
}
func writeResponse(w http.ResponseWriter, id string, value any, err error) {
	out := response{Version: Version, ID: id}
	if err == nil {
		out.Result, err = json.Marshal(value)
	}
	if err != nil {
		out.Result = nil
		out.Error = &remoteError{Message: err.Error(), CleanupUnconfirmed: errors.Is(err, core.ErrCleanupUnconfirmed)}
	}
	data, encodeErr := json.Marshal(out)
	if encodeErr != nil || len(data) > maxMessage {
		http.Error(w, "invalid provider response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// Inherit only OS/runtime settings, never model credential environment variables.
func safeEnvironment() []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "TMPDIR": true, "TMP": true, "TEMP": true, "LANG": true, "TZ": true, "XDG_RUNTIME_DIR": true, "DBUS_SESSION_BUS_ADDRESS": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true}
	out := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if allowed[key] || strings.HasPrefix(key, "LC_") {
			out = append(out, entry)
		}
	}
	return out
}
