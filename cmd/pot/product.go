package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/instance"
	product "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/internal/webui"
	assets "github.com/jjyr/pot/web"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func productCommand(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(out)
	root := flags.String("root", "", "private Go data root (default ~/.pot-go)")
	workspace := flags.String("workspace", "", "Host Agent workspace")
	ui := flags.String("ui", "", "explicit loopback browser address, for example 127.0.0.1:17703")
	method := flags.String("method", "GET", "API method")
	body := flags.String("body", "", "API JSON request body, or - for stdin")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *root == "" {
		var err error
		*root, err = instance.DefaultPath()
		if err != nil {
			return err
		}
	}
	var err error
	*root, err = filepath.Abs(*root)
	if err != nil {
		return err
	}
	switch args[0] {
	case "init", "serve":
		if flags.NArg() != 0 {
			return errors.New("unexpected positional arguments")
		}
		runtime, err := product.Open(ctx, product.Options{Root: *root, Control: control.Options{Workspace: *workspace}})
		if err != nil {
			return err
		}
		defer runtime.Close()
		if args[0] == "init" {
			return json.NewEncoder(out).Encode(runtime.Root.Identity)
		}
		app := &transport.Application{Control: runtime.Control, RuntimeID: runtime.Root.Identity.RuntimeID, BootID: runtime.BootID, Name: "Pot"}
		socket, err := transport.StartSocket(ctx, runtime.Root.Path, app)
		if err != nil {
			return err
		}
		defer func() {
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = socket.Close(shutdown)
		}()
		fmt.Fprintf(out, "Runtime %s ready (%s)\n", app.RuntimeID, runtime.Root.Path)
		if *ui != "" {
			listener, err := net.Listen("tcp", *ui)
			if err != nil {
				return err
			}
			defer listener.Close()
			path := filepath.Join(runtime.Root.Path, "browser-access.json")
			config := webui.NewConfig()
			data, err := os.ReadFile(path)
			if err == nil {
				if err = json.Unmarshal(data, &config); err != nil {
					return err
				}
			} else if os.IsNotExist(err) {
				if err = config.Save(path); err != nil {
					return err
				}
			} else {
				return err
			}
			client := socket.Endpoint.Client()
			client.Timeout = 0
			origin := "http://" + listener.Addr().String()
			browser, err := webui.New(webui.Options{Origin: origin, Config: config, Upstream: webui.Upstream{URL: "http://runtime", Credential: socket.Endpoint.Token, Client: client}, Assets: assets.FS()})
			if err != nil {
				return err
			}
			defer browser.Close()
			server := &http.Server{Handler: browser, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			defer func() {
				browser.Close()
				shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = server.Shutdown(shutdown)
			}()
			fmt.Fprintf(out, "Browser: %s\nAccess token is stored privately in %s\n", origin, path)
			select {
			case <-ctx.Done():
				return nil
			case err := <-done:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			}
		}
		<-ctx.Done()
		return nil
	case "status", "api":
		endpoint, err := transport.ReadEndpoint(*root)
		if err != nil {
			return err
		}
		path := "/api/v1/runtime"
		if args[0] == "api" {
			if flags.NArg() != 1 {
				return errors.New("usage: pot api [-root PATH] [-method POST] [-body JSON|-] /api/v1/PATH")
			}
			path = flags.Arg(0)
		} else if flags.NArg() != 0 {
			return errors.New("unexpected status argument")
		}
		payload := []byte(*body)
		if *body == "-" {
			payload, err = io.ReadAll(io.LimitReader(os.Stdin, 16*1024*1024+1))
			if err != nil {
				return err
			}
			if len(payload) > 16*1024*1024 {
				return errors.New("body exceeds limit")
			}
		}
		req, err := http.NewRequestWithContext(ctx, *method, "http://runtime"+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+endpoint.Token)
		req.Header.Set("Content-Type", "application/json")
		response, err := endpoint.Client().Do(req)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		_, err = io.Copy(out, response.Body)
		if err != nil {
			return err
		}
		if response.StatusCode >= 400 {
			return fmt.Errorf("Runtime HTTP %d", response.StatusCode)
		}
		return nil
	}
	return errors.New("unknown command")
}
