package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/access"
	"github.com/jjyr/pot/internal/tailnet"
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
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
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
	control := flags.String("control", "", "explicit Control origin or local")
	controlRoot := flags.String("control-root", "", "local Runtime root when -control local")
	tokenFile := flags.String("control-token-file", "", "private remote Control owner credential file")
	ca := flags.String("control-ca", "", "additional remote Control CA PEM")
	listen := flags.String("listen", "127.0.0.1:17703", "browser listener")
	origin := flags.String("origin", "", "exact browser public origin")
	cert := flags.String("tls-cert", "", "browser TLS certificate")
	key := flags.String("tls-key", "", "browser TLS key")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *control == "" || *root == "" || *origin == "" {
		return errors.New("usage: pot web-ui-server -root PATH -control local|ORIGIN -origin PUBLIC_ORIGIN [-control-root PATH | -control-token-file PATH]")
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
	if err = os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("browser data root must be private and owned")
	}
	lock, err := os.OpenFile(filepath.Join(directory, "web-ui-server.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("browser data root already in use")
	}
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
		if *tokenFile != "" || *ca != "" {
			return errors.New("remote credential/CA cannot accompany explicit local Control")
		}
		if *controlRoot == "" {
			*controlRoot, err = instance.DefaultPath()
			if err != nil {
				return err
			}
		}
		endpoint, err := transport.ReadEndpoint(*controlRoot)
		if err != nil {
			return err
		}
		upstream = webui.Upstream{URL: "http://runtime", Credential: endpoint.Token, Client: endpoint.Client()}
	} else {
		if *tokenFile == "" || *controlRoot != "" {
			return errors.New("remote Control requires explicit private credential file")
		}
		token, err := privateText(*tokenFile)
		if err != nil {
			return err
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if *ca != "" {
			data, err := os.ReadFile(*ca)
			if err != nil {
				return err
			}
			roots, err := x509.SystemCertPool()
			if err != nil || roots == nil {
				roots = x509.NewCertPool()
			}
			if !roots.AppendCertsFromPEM(data) {
				return errors.New("invalid Control CA")
			}
			tr.TLSClientConfig.RootCAs = roots
		}
		upstream = webui.Upstream{URL: strings.TrimSuffix(*control, "/") + "/control/v1/owner", Credential: token, Client: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
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
	if identity.ID == "" || identity.ID != identity.RuntimeID || *control != "local" && identity.Role != "control" {
		return errors.New("configured upstream is not the selected standalone Control")
	}
	upstream.ControlID = identity.ID
	if err = pinBrowserControl(directory, identity.ID); err != nil {
		return err
	}
	publication, err := access.Open(ctx, access.Options{Root: directory, Origin: *origin, Upstream: upstream, Assets: assets.FS(), Tailnet: tailnet.New(directory, "pot")})
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
	fmt.Fprintf(out, "Browser server: %s (Control %s)\nAccess configuration: %s\n", *origin, identity.ID, path)
	select {
	case <-ctx.Done():
		return nil
	case err := <-watchDone:
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
