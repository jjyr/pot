package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/jjyr/pot/pkg/privatefs"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/access"
	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/internal/selfupdate"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/internal/webui"
	"github.com/jjyr/pot/pkg/instance"
	assets "github.com/jjyr/pot/web"
)

func privateText(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if privatefs.Check(path) != nil || !info.Mode().IsRegular() || info.Size() > 8192 {
		return "", errors.New("credential must be a private current-user regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
func browserCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("web-ui-server", flag.ContinueOnError)
	flags.SetOutput(out)
	root := flags.String("root", "", "private browser service root")
	control := flags.String("control", "local", "local autonomous node")
	controlRoot := flags.String("control-root", "", "local Runtime root")
	listen := flags.String("listen", "127.0.0.1:17703", "browser listener")
	origin := flags.String("origin", "", "exact browser public origin")
	cert := flags.String("tls-cert", "", "browser TLS certificate")
	key := flags.String("tls-key", "", "browser TLS key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *control != "local" || *root == "" || *origin == "" {
		return errors.New("usage: pot web-ui-server -root PATH -origin PUBLIC_ORIGIN [-control-root PATH]")
	}
	if (*cert == "") != (*key == "") {
		return errors.New("both TLS certificate and key required")
	}
	if *cert != "" {
		if _, err := tls.LoadX509KeyPair(*cert, *key); err != nil {
			return err
		}
	}
	directory, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	if err = privatefs.Mkdir(directory); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if privatefs.Check(directory) != nil || !info.IsDir() {
		return errors.New("browser data root must be private and owned")
	}
	lock, err := privatefs.Lock(filepath.Join(directory, "web-ui-server.lock"), false, true)
	if err != nil {
		return err
	}
	defer lock.Close()
	path := filepath.Join(directory, "browser-access.json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		entries, e := os.ReadDir(directory)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if entry.Name() != "web-ui-server.lock" {
				return errors.New("refusing to initialize unrecognized nonempty browser data root")
			}
		}
	}
	store := webui.AccessStore{Path: path}
	_, err = store.Load(true)
	if err != nil {
		return err
	}
	upstream := webui.Upstream{}
	if *control == "local" {
		if *controlRoot == "" {
			*controlRoot, err = instance.DefaultPath()
			if err != nil {
				return err
			}
		}
		endpoint, err := transport.ReadEndpoint(*controlRoot)
		if host := selfupdate.FromContext(ctx); host != nil && host.Maintaining() {
			deadline := time.Now().Add(40 * time.Second)
			for time.Now().Before(deadline) {
				endpoint, err = transport.ReadEndpoint(*controlRoot)
				if err == nil {
					readyctx, cancel := context.WithTimeout(ctx, time.Second)
					req, _ := http.NewRequestWithContext(readyctx, "GET", "http://runtime/api/v1/runtime", nil)
					req.Header.Set("Authorization", "Bearer "+endpoint.Token)
					res, e := endpoint.Client().Do(req)
					if e == nil {
						var v struct{ Version string }
						e = json.NewDecoder(res.Body).Decode(&v)
						res.Body.Close()
						if e == nil && v.Version == buildinfo.Version {
							cancel()
							break
						}
					}
					cancel()
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		if err != nil {
			return err
		}
		upstream = webui.Upstream{URL: "http://runtime", Credential: endpoint.Token, Client: endpoint.Client()}
	}
	upstream.Client.Timeout = 0
	requestctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	request, _ := http.NewRequestWithContext(requestctx, "GET", upstream.URL+"/api/v1/control", nil)
	request.Header.Set("Authorization", "Bearer "+upstream.Credential)
	response, err := upstream.Client.Do(request)
	if err != nil {
		cancel()
		return err
	}
	var identity struct{ ID, RuntimeID, Role string }
	identityData, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	response.Body.Close()
	cancel()
	if err != nil || len(identityData) > 65536 || response.StatusCode != 200 {
		return errors.New("configured Control authentication failed")
	}
	var value map[string]any
	if err = json.Unmarshal(identityData, &value); err != nil {
		return err
	}
	identity.ID, _ = value["id"].(string)
	identity.RuntimeID, _ = value["runtime_id"].(string)
	identity.Role, _ = value["role"].(string)
	if identity.ID == "" || identity.ID != identity.RuntimeID {
		return errors.New("configured upstream is not the selected local node")
	}
	upstream.ControlID = identity.ID
	if err = pinBrowserControl(directory, identity.ID); err != nil {
		return err
	}
	publication, err := access.Open(access.Options{Root: directory, Origin: *origin, Upstream: upstream, Assets: assets.FS()})
	if err != nil {
		return err
	}
	defer publication.Close()
	operator, err := access.StartOperator(directory, publication)
	if err != nil {
		return err
	}
	defer operator.Close()
	watchctx, stopwatch := context.WithCancel(ctx)
	defer stopwatch()
	watchDone := make(chan error, 1)
	go func() { watchDone <- publication.WatchAccess(watchctx) }()
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	httpServer := &http.Server{Handler: publication, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() {
		if *cert != "" {
			done <- httpServer.ServeTLS(listener, *cert, *key)
		} else {
			done <- httpServer.Serve(listener)
		}
	}()
	defer func() {
		publication.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	if host := selfupdate.FromContext(ctx); host != nil {
		if err = host.Bind(directory, nil); err != nil {
			return err
		}
	}
	if host := selfupdate.FromContext(ctx); host != nil {
		host.Ready()
	}
	fmt.Fprintf(out, "Browser server: %s (Control %s)\nAccess configuration: %s\n", *origin, identity.ID, path)
	select {
	case <-ctx.Done():
		return nil
	case err := <-watchDone:
		if ctx.Err() != nil {
			return nil
		}
		return err
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func pinBrowserControl(directory, id string) error {
	path := filepath.Join(directory, "browser-control.json")
	data, err := privateText(path)
	if os.IsNotExist(err) {
		return instance.WriteJSON(path, map[string]string{"control_id": id})
	}
	if err != nil {
		return err
	}
	var saved struct {
		ControlID string `json:"control_id"`
	}
	if err = json.Unmarshal([]byte(data), &saved); err != nil {
		return err
	}
	if saved.ControlID != id {
		return errors.New("browser service is bound to a different Control identity; choose a fresh browser data root")
	}
	return nil
}
