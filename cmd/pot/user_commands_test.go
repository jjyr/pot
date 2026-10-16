package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/transport"
)

func TestUserCLIUsesAuthenticatedLocalRuntimeAndAssistantScope(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root, err := os.MkdirTemp("/tmp", "pot-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	os.Chmod(root, 0700)
	manager, _ := execution.New("local", "boot", execution.DefaultLimits())
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	service, err := control.Open(control.Options{Root: root, RuntimeID: "local", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err = service.Registry.Create(ctx, "create", "Pot"); err != nil {
		t.Fatal(err)
	}
	socket, err := transport.StartSocket(ctx, root, &transport.Application{Control: service, RuntimeID: "local", BootID: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close(context.Background())
	for _, command := range [][]string{{"doctor"}, {"tasks", "list"}, {"config", "get", "routing"}, {"config", "get", "agent"}} {
		var out bytes.Buffer
		args := append(command, "-root", root, "-assistant", "main")
		if err = userCommand(ctx, args, &out); err != nil {
			t.Fatal(command, err)
		}
		if !json.Valid(out.Bytes()) {
			t.Fatal("CLI returned invalid JSON")
		}
	}
	policy := filepath.Join(t.TempDir(), "policy.json")
	os.WriteFile(policy, []byte(`{"revision":0,"profile_limits":{},"provider_limits":{}}`), 0600)
	var out bytes.Buffer
	if err = userCommand(ctx, []string{"config", "set", "routing", policy, "-root", root}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err = userCommand(ctx, []string{"config", "set", "routing", policy, "-root", root}, &out); err == nil {
		t.Fatal("CLI silently replaced stale quota policy")
	}
}
func TestUserServiceDefinitionsPreserveLiteralPaths(t *testing.T) {
	mac, err := renderUserService("darwin", "/Applications/Pot & Tools/pot", "/Users/example/A <B>")
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(strings.NewReader(mac))
	for {
		_, err = decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(mac, "Pot &amp; Tools") {
		t.Fatal("XML path lost")
	}
	linux, err := renderUserService("linux", "/opt/Pot Tools/pot", "/home/example/100% data")
	if err != nil || !strings.Contains(linux, "100%% data") || !strings.Contains(linux, `ExecStart="/opt/Pot Tools/pot" serve -root`) {
		t.Fatal(linux, err)
	}
	if _, err = renderUserService("linux", "/opt/pot", "/tmp/evil\nExecStart=/bin/false"); err == nil {
		t.Fatal("service directive injection accepted")
	}
}
