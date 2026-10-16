package sandboxprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/sandbox"
)

func podmanTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func podmanMachineFixture(t *testing.T) *Podman {
	t.Helper()
	root := podmanTestRoot(t)
	for _, name := range []string{"execution", "user-skills", "builtin-skills"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	p := NewPodman(PodmanConfig{RuntimeRoot: filepath.Join(root, "execution"), UserSkills: filepath.Join(root, "user-skills"), BuiltinSkills: filepath.Join(root, "builtin-skills")})
	p.hostOS = "darwin"
	p.prepareMachineImage = func(ctx context.Context, root, reference string) (string, error) {
		return filepath.Join(root, "verified-machine.raw.zst"), nil
	}
	if err := os.Mkdir(filepath.Join(p.config.RuntimeRoot, "podman-shared"), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p.config.Binary = filepath.Join(root, "fake-podman")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run=^TestPodmanMachineProcessHelper$ -- \"$@\"\n"
	if err = os.WriteFile(p.config.Binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	name, _, err := p.machineIdentity()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("POT_PODMAN_HELPER_NAME", name)
	t.Setenv("POT_PODMAN_HELPER", "1")
	t.Setenv("POT_PODMAN_HELPER_LOG", filepath.Join(root, "commands.jsonl"))
	return p
}

// The subprocess implements only the external CLI boundary: the production
// provider still builds and executes every command and persists its own plan.
func TestPodmanMachineProcessHelper(t *testing.T) {
	if os.Getenv("POT_PODMAN_HELPER") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i + 1
			break
		}
	}
	args := os.Args[separator:]
	file, _ := os.OpenFile(os.Getenv("POT_PODMAN_HELPER_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	json.NewEncoder(file).Encode(args)
	file.Close()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	dir := filepath.Join(configHome, "containers/podman/machine/applehv")
	if len(args) > 1 && args[0] == "machine" {
		name := args[len(args)-1]
		statePath := filepath.Join(configHome, "machine-state")
		switch args[1] {
		case "list":
			switch os.Getenv("POT_PODMAN_HELPER_LIST_ERROR") {
			case "exit":
				os.Exit(125)
			case "invalid":
				fmt.Print(`null`)
				os.Exit(0)
			}
			if _, err := os.Stat(statePath); err == nil {
				json.NewEncoder(os.Stdout).Encode([]any{map[string]string{"Name": os.Getenv("POT_PODMAN_HELPER_NAME")}})
			} else {
				fmt.Print(`[]`)
			}
		case "inspect":
			if os.Getenv("POT_PODMAN_HELPER_INSPECT_ERROR") == "1" {
				os.Exit(125)
			}
			state, err := os.ReadFile(statePath)
			if err != nil {
				os.Exit(125)
			}
			json.NewEncoder(os.Stdout).Encode([]any{map[string]any{"Name": name, "Rootful": false, "State": strings.TrimSpace(string(state)), "ConfigDir": map[string]string{"Path": dir}}})
		case "init":
			var mounts []map[string]any
			for _, arg := range args {
				if strings.HasPrefix(arg, "--volume=") {
					parts := strings.Split(strings.TrimPrefix(arg, "--volume="), ":")
					mounts = append(mounts, map[string]any{"Source": parts[0], "Target": parts[1], "ReadOnly": parts[2] == "ro"})
				}
			}
			os.MkdirAll(dir, 0700)
			body, _ := json.Marshal(map[string]any{"Mounts": mounts})
			os.WriteFile(filepath.Join(dir, name+".json"), body, 0600)
			os.WriteFile(statePath, []byte("stopped"), 0600)
		case "start":
			os.WriteFile(statePath, []byte("running"), 0600)
		case "stop":
			os.WriteFile(statePath, []byte("stopped"), 0600)
		default:
			os.Exit(3)
		}
	} else if len(args) > 2 && os.Getenv("POT_PODMAN_HELPER_CONTAINER") != "" {
		engine := args[2:]
		if len(engine) > 1 && engine[0] == "container" && engine[1] == "inspect" {
			engine = engine[1:]
		}
		name := os.Getenv("POT_PODMAN_HELPER_CONTAINER")
		statePath := filepath.Join(configHome, "container-state")
		switch engine[0] {
		case "info":
			fmt.Print(`{"host":{"security":{"rootless":true},"cgroupVersion":"v2","cgroupManager":"systemd"}}`)
		case "container":
			state, err := os.ReadFile(statePath)
			if len(engine) != 3 || engine[1] != "exists" || engine[2] != name {
				os.Exit(7)
			}
			if err != nil || string(state) == "absent" {
				os.Exit(1)
			}
		case "inspect":
			if len(engine) != 2 || engine[1] != name {
				os.Exit(7)
			}
			state, err := os.ReadFile(statePath)
			if err != nil || string(state) == "absent" {
				os.Exit(125)
			}
			started := "2026-10-17T01:00:00Z"
			if string(state) == "running" {
				started = "2026-10-17T02:00:00Z"
			}
			json.NewEncoder(os.Stdout).Encode([]any{map[string]any{"Image": os.Getenv("POT_PODMAN_HELPER_IMAGE"), "Config": map[string]any{"Labels": map[string]string{"dev.pot.instance": name}}, "State": map[string]any{"Running": string(state) == "running", "StartedAt": started}}})
		case "update":
			if engine[len(engine)-1] != name {
				os.Exit(7)
			}
		case "start":
			if len(engine) != 2 || engine[1] != name {
				os.Exit(7)
			}
			os.WriteFile(statePath, []byte("running"), 0600)
		case "stop":
			if engine[len(engine)-1] != name {
				os.Exit(7)
			}
			os.WriteFile(statePath, []byte("stopped"), 0600)
		case "rm":
			if engine[len(engine)-1] != name {
				os.Exit(7)
			}
			os.WriteFile(statePath, []byte("absent"), 0600)
		default:
			os.Exit(8)
		}
	} else if len(args) > 2 && args[2] == "ps" {
		if os.Getenv("POT_PODMAN_HELPER_RUNNING") == "1" {
			fmt.Print(`[{"Names":["unknown-running"]}]`)
		} else {
			fmt.Print(`[]`)
		}
	} else {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"args": args, "config": configHome, "data": os.Getenv("XDG_DATA_HOME"), "connections": os.Getenv("PODMAN_CONNECTIONS_CONF"), "host": os.Getenv("CONTAINER_HOST"), "storage": os.Getenv("CONTAINERS_STORAGE_CONF")})
	}
	os.Exit(0)
}
func machineCommands(t *testing.T) [][]string {
	t.Helper()
	raw, err := os.ReadFile(os.Getenv("POT_PODMAN_HELPER_LOG"))
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var args []string
		if err = json.Unmarshal([]byte(line), &args); err != nil {
			t.Fatal(err)
		}
		out = append(out, args)
	}
	return out
}
func TestPodmanMachineSharesExcludePrivateStateAndTranslate(t *testing.T) {
	p := podmanMachineFixture(t)
	ctx := context.Background()
	i := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared"}
	if err := p.ensureMachine(ctx, i); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(p.dataDirectory(i), "workspace/tasks/first")
	mapped, err := p.mountSource(source)
	if err != nil || mapped != "/mnt/pot/shared/sandboxes/shared/workspace/tasks/first" {
		t.Fatalf("%q %v", mapped, err)
	}
	for _, private := range []string{p.config.RuntimeRoot, filepath.Dir(p.config.RuntimeRoot), filepath.Join(p.config.RuntimeRoot, "sandboxes/shared/executor-token"), filepath.Join(p.config.RuntimeRoot, "podman-machine")} {
		if _, err = p.mountSource(private); err == nil {
			t.Fatalf("shared private path %s", private)
		}
	}
	commands := machineCommands(t)
	var init []string
	for _, args := range commands {
		if len(args) > 1 && args[0] == "machine" && args[1] == "init" {
			init = args
		}
	}
	if len(init) == 0 {
		t.Fatal("machine initialization absent")
	}
	joined := strings.Join(init, "\n")
	if !strings.Contains(joined, "--rootful=false") || !strings.Contains(joined, "--update-connection=false") || !strings.Contains(joined, "verified-machine.raw.zst") || !strings.Contains(joined, "--memory=8192") {
		t.Fatal(init)
	}
	count := 0
	for _, arg := range init {
		if strings.HasPrefix(arg, "--volume=") {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("unexpected shared roots: %v", init)
	}
	before := len(commands)
	if err = p.ensureMachine(ctx, i); err != nil {
		t.Fatal(err)
	}
	for _, args := range machineCommands(t)[before:] {
		if len(args) > 1 && args[0] == "machine" && (args[1] == "init" || args[1] == "start") {
			t.Fatalf("running machine replaced/restarted: %v", args)
		}
	}
}
func TestPodmanMachineRejectsPrivateAndLateMappingRoots(t *testing.T) {
	p := podmanMachineFixture(t)
	i := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared"}
	private := filepath.Join(p.config.RuntimeRoot, "sandboxes/shared")
	os.MkdirAll(private, 0700)
	for _, source := range []string{p.config.RuntimeRoot, filepath.Dir(p.config.RuntimeRoot), private} {
		i.Mappings = []sandbox.HostMapping{{Source: source, Destination: "/mnt/host/private"}}
		if _, err := p.machinePlan(i); err == nil {
			t.Fatalf("private runtime share accepted: %s", source)
		}
	}
	i.Mappings = nil
	if err := p.ensureMachine(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	additional := podmanTestRoot(t)
	i.Mappings = []sandbox.HostMapping{{Source: additional, Destination: "/mnt/host/new"}}
	if err := p.ensureMachine(context.Background(), i); err == nil || !strings.Contains(err.Error(), "pinned shares") {
		t.Fatalf("late share: %v", err)
	}
	for _, args := range machineCommands(t) {
		if len(args) > 1 && args[0] == "machine" && args[1] == "stop" {
			t.Fatal("late mapping stopped siblings")
		}
	}
}
func TestPodmanCommandsUseDedicatedConnectionAndIsolatedEnvironment(t *testing.T) {
	p := podmanMachineFixture(t)
	t.Setenv("CONTAINER_HOST", "ssh://owner-default")
	t.Setenv("CONTAINER_CONNECTION", "owner-default")
	t.Setenv("CONTAINERS_STORAGE_CONF", "owner-storage")
	t.Setenv("PODMAN_CONNECTIONS_CONF", "owner-connections")
	raw, code, err := p.command(context.Background(), "info")
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	var result struct {
		Args                                     []string
		Config, Data, Connections, Host, Storage string
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	name, root, _ := p.machineIdentity()
	if len(result.Args) != 3 || result.Args[0] != "--remote=true" || result.Args[1] != "--connection="+name || result.Host != "" || result.Storage != "" || result.Config != filepath.Join(root, "config") || result.Connections != filepath.Join(root, "connections.json") {
		t.Fatalf("%+v", result)
	}
	p.hostOS = "linux"
	raw, code, err = p.command(context.Background(), "info")
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Args[0] != "--remote=false" || result.Storage != "owner-storage" || result.Host != "" {
		t.Fatalf("%+v", result)
	}
}
func TestPodmanMachineShutdownLeavesRunningSiblingAndStopsOnlyDedicatedMachine(t *testing.T) {
	p := podmanMachineFixture(t)
	i := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared"}
	ctx := context.Background()
	if err := p.ensureMachine(ctx, i); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POT_PODMAN_HELPER_RUNNING", "1")
	if err := p.stopMachine(ctx); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatalf("running sibling: %v", err)
	}
	for _, args := range machineCommands(t) {
		if len(args) > 1 && args[0] == "machine" && args[1] == "stop" {
			t.Fatal("machine stopped with running sibling")
		}
	}
	t.Setenv("POT_PODMAN_HELPER_RUNNING", "0")
	if err := p.stopMachine(ctx); err != nil {
		t.Fatal(err)
	}
	name, _, _ := p.machineIdentity()
	found := false
	for _, args := range machineCommands(t) {
		if len(args) > 1 && args[0] == "machine" && args[1] == "stop" {
			found = true
			if len(args) != 3 || args[2] != name {
				t.Fatal(args)
			}
		}
	}
	if !found {
		t.Fatal("dedicated machine not stopped")
	}
}
func TestPodmanMachineRefusesUnownedOrExpandedMachineShares(t *testing.T) {
	p := podmanMachineFixture(t)
	ctx := context.Background()
	i := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared"}
	name, root, _ := p.machineIdentity()
	configHome := filepath.Join(root, "config")
	os.MkdirAll(configHome, 0700)
	os.WriteFile(filepath.Join(configHome, "machine-state"), []byte("stopped"), 0600)
	if err := p.ensureMachine(ctx, i); err == nil || !strings.Contains(err.Error(), "unowned") {
		t.Fatalf("unowned machine: %v", err)
	}
	for _, args := range machineCommands(t) {
		if len(args) > 1 && args[0] == "machine" && (args[1] == "init" || args[1] == "start") {
			t.Fatal("unowned machine mutated", args)
		}
	}
	os.Remove(filepath.Join(configHome, "machine-state"))
	if err := p.ensureMachine(ctx, i); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configHome, "containers/podman/machine/applehv", name+".json")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string][]map[string]any
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value["Mounts"] = append(value["Mounts"], map[string]any{"Source": filepath.Dir(p.config.RuntimeRoot), "Target": "/host-home", "ReadOnly": false})
	raw, _ = json.Marshal(value)
	os.WriteFile(config, raw, 0600)
	if err = p.ensureMachine(ctx, i); err == nil || !strings.Contains(err.Error(), "shares changed") {
		t.Fatalf("expanded machine shares accepted: %v", err)
	}
}
func TestSandboxStopDoesNotStopSharedMachine(t *testing.T) {
	p := podmanMachineFixture(t)
	ctx := context.Background()
	i := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared"}
	if err := p.ensureMachine(ctx, i); err != nil {
		t.Fatal(err)
	}
	if status, err := p.Lifecycle(ctx, i, "stop"); err != nil || status.Phase != "stopped" {
		t.Fatal(status, err)
	}
	for _, args := range machineCommands(t) {
		if len(args) > 1 && args[0] == "machine" && args[1] == "stop" {
			t.Fatal("sandbox stop stopped shared machine")
		}
	}
}
func TestPodmanMachineRejectsHomeEvenWithTemporaryRuntimeRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{home, filepath.Dir(home)} {
		if _, err = canonicalShare(source, "/mnt/pot/home", false); err == nil {
			t.Fatalf("broad home share accepted: %s", source)
		}
	}
	project := podmanTestRoot(t)
	if _, err = canonicalShare(project, "/mnt/pot/project", false); err != nil {
		t.Fatal(err)
	}
}
func TestPodmanMachineInventoryFailureCannotConfirmAbsenceOrCleanup(t *testing.T) {
	for _, mode := range []string{"exit", "invalid", "inspect"} {
		t.Run(mode, func(t *testing.T) {
			p := podmanMachineFixture(t)
			ctx := context.Background()
			i := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared"}
			if err := p.ensureMachine(ctx, i); err != nil {
				t.Fatal(err)
			}
			if mode == "inspect" {
				t.Setenv("POT_PODMAN_HELPER_INSPECT_ERROR", "1")
			} else {
				t.Setenv("POT_PODMAN_HELPER_LIST_ERROR", mode)
			}
			if row, err := p.machineInspect(ctx); row != nil || !errors.Is(err, core.ErrCleanupUnconfirmed) {
				t.Fatalf("inspection failure treated as absence: %+v %v", row, err)
			}
			if err := p.stopMachine(ctx); !errors.Is(err, core.ErrCleanupUnconfirmed) {
				t.Fatalf("cleanup confirmed despite unavailable inventory: %v", err)
			}
			for _, args := range machineCommands(t) {
				if len(args) > 1 && args[0] == "machine" && args[1] == "stop" {
					t.Fatal("uncertain machine stopped", args)
				}
			}
		})
	}
}
func TestPodmanMachineListFailureCannotAuthorizeInitialization(t *testing.T) {
	p := podmanMachineFixture(t)
	t.Setenv("POT_PODMAN_HELPER_LIST_ERROR", "exit")
	if err := p.ensureMachine(context.Background(), sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared"}); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatal(err)
	}
	_, root, _ := p.machineIdentity()
	if _, err := os.Stat(filepath.Join(root, "owner.json")); !os.IsNotExist(err) {
		t.Fatal("failed inventory authorized machine ownership", err)
	}
	for _, args := range machineCommands(t) {
		if len(args) > 1 && args[0] == "machine" && (args[1] == "init" || args[1] == "start") {
			t.Fatal("failed inventory authorized machine mutation", args)
		}
	}
}
func TestPodmanMachineAbsenceRequiresSuccessfulInventory(t *testing.T) {
	p := podmanMachineFixture(t)
	row, err := p.machineInspect(context.Background())
	if err != nil || row != nil {
		t.Fatal(row, err)
	}
	commands := machineCommands(t)
	if len(commands) != 1 || strings.Join(commands[0], " ") != "machine list --format=json" {
		t.Fatal(commands)
	}
}
func TestPodmanBinaryDiscoveryWithFinderPath(t *testing.T) {
	root := podmanTestRoot(t)
	directory := filepath.Join(root, "directory")
	nonexec := filepath.Join(root, "nonexec")
	installed := filepath.Join(root, "installed-podman")
	os.Mkdir(directory, 0700)
	os.WriteFile(nonexec, []byte("not executable"), 0600)
	os.WriteFile(installed, []byte("#!/bin/sh\nexit 0\n"), 0700)
	t.Setenv("PATH", "")
	candidates := []string{directory, nonexec, installed}
	if actual := findPodmanBinary("darwin", candidates); actual != installed {
		t.Fatal(actual)
	}
	if actual := findPodmanBinary("linux", candidates); actual != "podman" {
		t.Fatalf("Linux unexpectedly used macOS fallback: %s", actual)
	}
	pathDir := filepath.Join(root, "path")
	os.Mkdir(pathDir, 0700)
	pathBinary := filepath.Join(pathDir, "podman")
	os.WriteFile(pathBinary, []byte("#!/bin/sh\nexit 0\n"), 0700)
	t.Setenv("PATH", pathDir)
	if actual := findPodmanBinary("darwin", candidates); actual != pathBinary {
		t.Fatalf("PATH engine lost precedence: %s", actual)
	}
}
func TestPodmanVirtiofsSELinuxDomainIsMacOnlyAndKeepsOtherBoundaries(t *testing.T) {
	for _, host := range []string{"darwin", "linux"} {
		options := podmanSecurityOptions(host)
		hasNNP, hasEngine := false, false
		for _, option := range options {
			switch option {
			case "--security-opt=no-new-privileges":
				hasNNP = true
			case "--security-opt=label=type:container_engine_t":
				hasEngine = true
			default:
				t.Fatalf("unexpected security override for %s: %s", host, option)
			}
		}
		if !hasNNP || hasEngine != (host == "darwin") {
			t.Fatalf("%s security boundaries: %v", host, options)
		}
	}
}
func stoppedPodmanContainer(t *testing.T) (*Podman, sandbox.Instance, podmanInstance) {
	t.Helper()
	p := podmanMachineFixture(t)
	i := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared", Resources: p.DefaultResources()}
	ctx := context.Background()
	if err := p.ensureMachine(ctx, i); err != nil {
		t.Fatal(err)
	}
	saved := podmanInstance{Name: "pot-saved-qualification", Image: strings.Repeat("a", 64), Generation: 77}
	if err := privateJSON(filepath.Join(i.Directory(), "podman.json"), saved); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POT_PODMAN_HELPER_CONTAINER", saved.Name)
	t.Setenv("POT_PODMAN_HELPER_IMAGE", saved.Image)
	_, root, _ := p.machineIdentity()
	config := filepath.Join(root, "config")
	if err := os.WriteFile(filepath.Join(config, "machine-state"), []byte("stopped"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "container-state"), []byte("stopped"), 0600); err != nil {
		t.Fatal(err)
	}
	return p, i, saved
}
func TestStoppedDedicatedMachineStatusAndStopNeverBootOrCallEngine(t *testing.T) {
	p, i, saved := stoppedPodmanContainer(t)
	before := len(machineCommands(t))
	for _, action := range []string{"status", "stop"} {
		state, err := p.Lifecycle(context.Background(), i, action)
		if err != nil || state.Phase != "stopped" || state.Generation != saved.Generation {
			t.Fatalf("%s: %+v %v", action, state, err)
		}
	}
	for _, args := range machineCommands(t)[before:] {
		if args[0] != "machine" || (args[1] != "list" && args[1] != "inspect") {
			t.Fatalf("stopped status mutated engine: %v", args)
		}
	}
	if _, err := p.Request(context.Background(), i, "cancel", json.RawMessage(`{"id":"original"}`), 1); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatalf("stopped VM fabricated cleanup acknowledgment: %v", err)
	}
}
func TestMissingOrUninspectableOwnedMachineKeepsAcceptedContainerFenced(t *testing.T) {
	for _, mode := range []string{"missing", "inventory"} {
		t.Run(mode, func(t *testing.T) {
			p, i, _ := stoppedPodmanContainer(t)
			_, root, _ := p.machineIdentity()
			if mode == "missing" {
				os.Remove(filepath.Join(root, "config/machine-state"))
			} else {
				t.Setenv("POT_PODMAN_HELPER_LIST_ERROR", "exit")
			}
			before := len(machineCommands(t))
			for _, action := range []string{"status", "stop", "start", "delete"} {
				if _, err := p.Lifecycle(context.Background(), i, action); !errors.Is(err, core.ErrCleanupUnconfirmed) {
					t.Fatalf("%s machine %s escaped fence: %v", mode, action, err)
				}
			}
			if _, err := os.Stat(filepath.Join(i.Directory(), "podman.json")); err != nil {
				t.Fatal("uncertain accepted container identity deleted", err)
			}
			for _, args := range machineCommands(t)[before:] {
				if args[0] != "machine" || (args[1] != "list" && args[1] != "inspect") {
					t.Fatalf("missing/uncertain machine mutated: %v", args)
				}
			}
		})
	}
}
func TestStoppedMachineRestartStartsOnlyOriginalContainer(t *testing.T) {
	p, i, saved := stoppedPodmanContainer(t)
	before := len(machineCommands(t))
	state, err := p.Lifecycle(context.Background(), i, "start")
	if err != nil || state.Phase != "running" {
		t.Fatal(state, err)
	}
	machineStart, containerStart := 0, 0
	for _, args := range machineCommands(t)[before:] {
		if len(args) > 1 && args[0] == "machine" && args[1] == "start" {
			machineStart++
		}
		if len(args) > 2 && args[0] == "--remote=true" && args[2] == "start" {
			containerStart++
			if len(args) != 4 || args[3] != saved.Name {
				t.Fatal("started different container", args)
			}
		}
	}
	if machineStart != 1 || containerStart != 1 {
		t.Fatalf("machine starts %d container starts %d", machineStart, containerStart)
	}
}
func TestDeleteFromStoppedMachineStartsEngineButNeverSiblingContainers(t *testing.T) {
	p, i, saved := stoppedPodmanContainer(t)
	before := len(machineCommands(t))
	state, err := p.Lifecycle(context.Background(), i, "delete")
	if err != nil || state.Phase != "stopped" {
		t.Fatal(state, err)
	}
	machineStart, removed := 0, 0
	for _, args := range machineCommands(t)[before:] {
		if len(args) > 1 && args[0] == "machine" && args[1] == "start" {
			machineStart++
		}
		if len(args) > 2 && args[0] == "--remote=true" {
			if args[2] == "start" {
				t.Fatal("delete started a retained container", args)
			}
			if args[2] == "rm" {
				removed++
				if args[len(args)-1] != saved.Name {
					t.Fatal("removed different container", args)
				}
			}
		}
	}
	if machineStart != 1 || removed != 1 {
		t.Fatalf("machine starts %d removed %d", machineStart, removed)
	}
	if _, err := os.Stat(filepath.Join(i.Directory(), "podman.json")); !os.IsNotExist(err) {
		t.Fatal("deleted container identity retained", err)
	}
}
func TestNewSandboxCannotReplaceMissingInitializedSharedMachine(t *testing.T) {
	p := podmanMachineFixture(t)
	ctx := context.Background()
	first := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "first"}
	if err := p.ensureMachine(ctx, first); err != nil {
		t.Fatal(err)
	}
	plan, err := p.loadMachine()
	if err != nil || !plan.Initialized {
		t.Fatal("verified machine initialization not recorded", plan, err)
	}
	_, root, _ := p.machineIdentity()
	os.Remove(filepath.Join(root, "config/machine-state"))
	before := len(machineCommands(t))
	sibling := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "new-sibling", Resources: p.DefaultResources()}
	if _, err = p.Lifecycle(ctx, sibling, "start"); !errors.Is(err, core.ErrCleanupUnconfirmed) {
		t.Fatalf("new sibling replaced lost shared machine: %v", err)
	}
	for _, args := range machineCommands(t)[before:] {
		if args[0] != "machine" || (args[1] != "list" && args[1] != "inspect") {
			t.Fatal("missing initialized machine recreated", args)
		}
	}
	if _, err = os.Stat(filepath.Join(sibling.Directory(), "podman.json")); !os.IsNotExist(err) {
		t.Fatal("new container accepted on missing shared engine", err)
	}
}
func TestIncompleteMachineOwnershipCanRetryBeforeAnyVerifiedInitialization(t *testing.T) {
	p := podmanMachineFixture(t)
	i := sandbox.Instance{Root: p.config.RuntimeRoot, ID: "shared"}
	plan, err := p.machinePlan(i)
	if err != nil {
		t.Fatal(err)
	}
	_, root, _ := p.machineIdentity()
	if err = privateJSON(filepath.Join(root, "owner.json"), plan); err != nil {
		t.Fatal(err)
	}
	if err = p.ensureMachine(context.Background(), i); err != nil {
		t.Fatal("pre-initialization retry rejected", err)
	}
	plan, err = p.loadMachine()
	if err != nil || !plan.Initialized {
		t.Fatal("retry did not record verified initialization", plan, err)
	}
}
