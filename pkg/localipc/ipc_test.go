package localipc

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsAndUnixPrivateTransport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sock")
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		_, e = c.Write([]byte("ready"))
		done <- e
	}()
	client, err := Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	data := make([]byte, 5)
	if _, err = io.ReadFull(client, data); err != nil || string(data) != "ready" {
		t.Fatal(string(data), err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
