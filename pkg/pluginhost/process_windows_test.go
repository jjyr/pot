package pluginhost

import (
	"context"
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
	"testing"
)

func testProcessSignal(pid int) error {
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return syscall.ESRCH
	}
	defer windows.CloseHandle(h)
	var code uint32
	if e = windows.GetExitCodeProcess(h, &code); e != nil {
		return e
	}
	if code != 259 {
		return syscall.ESRCH
	}
	return nil
}

func TestWindowsNodePluginLifecycle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for Windows plugin qualification", err)
	}
	c, err := New(Config{Node: node, Workspace: t.TempDir(), Identity: "windows-test-fixture", Entrypoint: "index.mjs", Mode: "text", Files: map[string]string{"index.mjs": `export function createPlugin({emit}){let count=0;return{async execute(){emit({type:'text',text:'windows-node-'+(++count)})},async dispose(){}}}`}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, want := range []string{"windows-node-1", "windows-node-2"} {
		result, err := c.Execute(context.Background(), "fixture", map[string]any{})
		if err != nil || result.IsError || len(result.Content) != 1 || result.Content[0].Text != want {
			t.Fatal(result, err)
		}
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
}
