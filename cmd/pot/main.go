// Command pot is the new Go entry point. Product serving remains on the Rust
// entry point until its Go replacement reaches the migration acceptance gates.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/preview"
	webassets "github.com/jjyr/pot/web"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 1 && args[0] == "--version" {
		_, err := fmt.Fprintln(output, "pot Go migration preview")
		return err
	}
	if len(args) == 0 || args[0] != "preview" {
		return errors.New("usage: pot preview [-port 17702]; product serve is not migrated yet")
	}
	flags := flag.NewFlagSet("preview", flag.ContinueOnError)
	flags.SetOutput(output)
	port := flags.Int("port", 17702, "loopback preview port; zero selects a free port")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *port < 0 || *port > 65535 {
		return errors.New("invalid preview arguments")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	defer listener.Close()
	service := preview.New(ctx)
	defer service.Close()
	server := &http.Server{
		Handler:           preview.Handler(service, webassets.FS()),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16384,
	}
	stopped := make(chan struct{})
	shutdownDone := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := server.Shutdown(shutdown)
			if err != nil {
				_ = server.Close()
			}
			shutdownDone <- err
		case <-stopped:
			shutdownDone <- nil
		}
	}()
	fmt.Fprintf(output, "Disposable UI preview: http://%s\nNo model calls, tools or product data.\n", listener.Addr())
	serveErr := server.Serve(listener)
	close(stopped)
	shutdownErr := <-shutdownDone
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return shutdownErr
}
