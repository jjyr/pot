package platformhelper

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func socketServer(t *testing.T, respond func(net.Conn, map[string]any)) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pot-h-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "h.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0600)
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			return
		}
		var request map[string]any
		if json.Unmarshal(line, &request) != nil {
			return
		}
		respond(conn, request)
	}()
	return path
}
func TestPrivateHelperExchangeAndIdentity(t *testing.T) {
	path := socketServer(t, func(conn net.Conn, request map[string]any) {
		if request["version"] != float64(1) {
			t.Error("lost macOS Helper protocol version")
		}
		json.NewEncoder(conn).Encode(map[string]any{"version": 1, "id": request["id"], "result": map[string]any{"phase": "stopped", "generation": 0}})
	})
	result, err := (Client{SocketPath: path}).Call(context.Background(), Request{Method: "status"})
	if err != nil || result["phase"] != "stopped" {
		t.Fatalf("%v %v", result, err)
	}
	path = socketServer(t, func(conn net.Conn, _ map[string]any) { conn.Write([]byte("{\"id\":\"wrong\",\"result\":{}}\n")) })
	if _, err = (Client{SocketPath: path}).Call(context.Background(), Request{Method: "status"}); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatal(err)
	}
}
func TestBoundAndCancellation(t *testing.T) {
	path := socketServer(t, func(conn net.Conn, _ map[string]any) { conn.Write([]byte(strings.Repeat("x", 100))) })
	if _, err := (Client{SocketPath: path, MaxResponseBytes: 32}).Call(context.Background(), Request{Method: "status"}); err == nil {
		t.Fatal("accepted unbounded response")
	}
	path = socketServer(t, func(conn net.Conn, _ map[string]any) { buf := make([]byte, 1); conn.Read(buf) })
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := (Client{SocketPath: path}).Call(ctx, Request{Method: "status"}); err == nil {
		t.Fatal("ignored cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not close transport")
	}
}
func TestSwiftMacOSStatusWithoutVM(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("POT_SWIFT_PROBE") != "1" {
		t.Skip("set POT_SWIFT_PROBE=1 on macOS for the retained real Swift status probe")
	}
	compiler, err := exec.LookPath("swiftc")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "pot-sw-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	binary := filepath.Join(dir, "helper")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, compiler, "../../helper/Sources/PotMacOSHelper/main.swift", "-o", binary)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Swift build: %v\n%s", err, output)
	}
	cmd := exec.CommandContext(ctx, binary, dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	path := filepath.Join(dir, "macos-helper.sock")
	for range 100 {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	result, err := (Client{SocketPath: path}).Call(ctx, Request{Method: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if result["phase"] != "stopped" || result["protocol"] != "pot-macos-helper-v1" || result["generation"] != json.Number("0") {
		t.Fatalf("unexpected no-VM status: %v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "macos")); !os.IsNotExist(err) {
		t.Fatal("status prepared VM state")
	}
}
