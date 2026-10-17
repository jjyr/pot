package sandboxprovider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/platformhelper"
	"github.com/jjyr/pot/pkg/sandbox"
)

type PodmanConfig struct {
	Binary           string
	UserSkills       string
	BuiltinSkills    string
	RuntimeRoot      string
	MachineName      string
	MachineImage     string
	MachineCPUs      uint64
	MachineMemoryGiB uint64
	MachineDiskGiB   uint64
}
type Podman struct {
	config              PodmanConfig
	hostOS              string
	machineMu           sync.Mutex
	prepareMachineImage func(context.Context, string, string) (string, error)
	mu                  sync.Mutex
	instances           map[string]sandbox.Instance
}
type podmanInstance struct {
	Name       string `json:"name"`
	Image      string `json:"image"`
	Generation uint64 `json:"generation"`
}

func NewPodman(config PodmanConfig) *Podman {
	if config.Binary == "" {
		config.Binary = findPodmanBinary(runtime.GOOS, []string{"/opt/podman/bin/podman", "/opt/homebrew/bin/podman", "/usr/local/bin/podman"})
	}
	if config.MachineImage == "" {
		config.MachineImage = defaultPodmanMachineImage
	}
	if config.MachineCPUs == 0 {
		config.MachineCPUs = 4
	}
	if config.MachineMemoryGiB == 0 {
		config.MachineMemoryGiB = 8
	}
	if config.MachineDiskGiB == 0 {
		config.MachineDiskGiB = 50
	}
	return &Podman{config: config, hostOS: runtime.GOOS, instances: map[string]sandbox.Instance{}}
}

// Finder-launched applications do not inherit an interactive shell's PATH.
// Discover an already installed engine without changing the host installation.
func findPodmanBinary(goos string, candidates []string) string {
	if path, err := exec.LookPath("podman"); err == nil {
		return path
	}
	if goos == "darwin" {
		for _, path := range candidates {
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
				return path
			}
		}
	}
	return "podman"
}
func (p *Podman) Descriptor() sandbox.Descriptor {
	return sandbox.Descriptor{ID: PodmanID, OS: "linux", HostOS: p.hostOS, Engine: "podman", Capabilities: sandbox.Capabilities{Shell: true, Files: true, Desktop: true, SharedExecution: true, HostMappings: true}}
}
func (p *Podman) DefaultResources() sandbox.Resources {
	return sandbox.Resources{CPUs: 4, MemoryGiB: 8, DiskGiB: 0}
}
func (p *Podman) ValidateResources(instance sandbox.Instance, value sandbox.Resources) error {
	if value.CPUs < 1 || value.CPUs > 32 || value.MemoryGiB < 1 || value.MemoryGiB > 128 || value.DiskGiB != 0 {
		return errors.New("Podman supports 1–32 CPUs, 1–128 GiB RAM; host filesystem owns disk capacity")
	}
	return nil
}
func (p *Podman) Workspace(instance sandbox.Instance) string {
	return filepath.Join(p.dataDirectory(instance), "workspace")
}
func (p *Podman) GuestWorkspace() string               { return "/data/workspace" }
func (p *Podman) CleanupPolicy() sandbox.CleanupPolicy { return sandbox.ProcessContainment }
func (p *Podman) BindResources(ctx context.Context, instance sandbox.Instance, prepared sandbox.PreparedResources) error {
	resources, err := decodeImageResources(prepared)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if resources.Architecture != runtime.GOARCH {
		return errors.New("prepared OCI architecture differs from this Runtime")
	}
	verified, err := verifyPodmanImage(resources)
	if err != nil {
		return err
	}
	saved, err := p.load(instance)
	if err != nil {
		return err
	}
	if saved != nil && saved.Image != strings.TrimPrefix(verified.ConfigDigest, "sha256:") {
		return errors.New("Sandbox image is pinned to its accepted OCI configuration; rebuild before binding another image")
	}
	return immutableResources(instance, resources)
}

type commandOutput struct {
	data  []byte
	limit int
}

func (w *commandOutput) Write(data []byte) (int, error) {
	if len(w.data)+len(data) > w.limit {
		return 0, errors.New("Podman output limit exceeded")
	}
	w.data = append(w.data, data...)
	return len(data), nil
}
func (p *Podman) command(ctx context.Context, args ...string) ([]byte, int, error) {
	prefix := []string{"--remote=false"}
	if p.hostOS == "darwin" {
		name, _, err := p.machineIdentity()
		if err != nil {
			return nil, -1, err
		}
		prefix = []string{"--remote=true", "--connection=" + name}
	} else if p.hostOS != "linux" {
		return nil, -1, errors.New("Podman requires macOS or Linux")
	}
	return p.runCommand(ctx, nil, append(prefix, args...)...)
}
func (p *Podman) runCommand(ctx context.Context, input io.Reader, args ...string) ([]byte, int, error) {
	timeout := 60 * time.Second
	for _, arg := range args {
		if arg == "load" {
			timeout = 10 * time.Minute
		}
	}
	if len(args) > 1 && args[0] == "machine" {
		if args[1] == "init" {
			timeout = 15 * time.Minute
		}
		if args[1] == "start" {
			timeout = 2 * time.Minute
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, p.config.Binary, args...)
	command.Stdin = input
	command.Env = p.commandEnvironment()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	var stdout commandOutput
	stdout.limit = 4 * 1024 * 1024
	command.Stdout = &stdout
	command.Stderr = io.Discard
	err := command.Run()
	code := -1
	if command.ProcessState != nil {
		code = command.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return nil, code, errors.Join(core.ErrCleanupUnconfirmed, errors.New("Podman command timed out; inspect before retrying"))
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return nil, code, errors.New("Podman operation unavailable or output invalid")
	}
	return stdout.data, code, nil
}

func (p *Podman) checked(ctx context.Context, args ...string) (json.RawMessage, error) {
	data, code, err := p.command(ctx, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("Podman %s failed; inspect locally without exposing private diagnostics", args[0])
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(data) {
		return nil, errors.New("invalid Podman JSON output")
	}
	return data, nil
}
func (p *Podman) load(instance sandbox.Instance) (*podmanInstance, error) {
	var value podmanInstance
	err := loadJSON(filepath.Join(instance.Directory(), "podman.json"), &value)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if value.Name == "" || value.Image == "" {
		return nil, errors.New("invalid persisted Podman identity")
	}
	return &value, nil
}
func (p *Podman) inspect(ctx context.Context, instance podmanInstance) (map[string]any, error) {
	_, code, err := p.command(ctx, "container", "exists", instance.Name)
	if err != nil {
		return nil, err
	}
	if code == 1 {
		return nil, nil
	}
	if code != 0 {
		return nil, errors.New("cannot inspect Podman container existence")
	}
	data, err := p.checked(ctx, "container", "inspect", instance.Name)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if json.Unmarshal(data, &rows) != nil || len(rows) != 1 {
		return nil, errors.New("invalid Podman container identity response")
	}
	row := rows[0]
	config, _ := row["Config"].(map[string]any)
	labels, _ := config["Labels"].(map[string]any)
	if labels["dev.pot.instance"] != instance.Name || row["Image"] != instance.Image {
		return nil, errors.New("Podman container identity mismatch")
	}
	return row, nil
}
func nested(row map[string]any, keys ...string) any {
	var value any = row
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}
func bootGeneration(row map[string]any) uint64 {
	started, _ := nested(row, "State", "StartedAt").(string)
	hash := sha256.Sum256([]byte(started))
	return binary.BigEndian.Uint64(hash[:8])
}

// A stopped, positively identified dedicated VM proves that its containers are
// stopped. It does not provide an execution receipt or authorize replacement of
// a missing machine. Status never boots the engine just to inspect a container.
func (p *Podman) dedicatedMachineStopped(ctx context.Context) (bool, error) {
	if p.hostOS != "darwin" {
		return false, nil
	}
	p.machineMu.Lock()
	defer p.machineMu.Unlock()
	if _, err := p.loadMachine(); err != nil {
		return false, errors.Join(core.ErrCleanupUnconfirmed, err, errors.New("accepted container's dedicated machine ownership unavailable"))
	}
	state, err := p.machineInspect(ctx)
	if err != nil {
		return false, errors.Join(core.ErrCleanupUnconfirmed, err)
	}
	if state == nil {
		return false, errors.Join(core.ErrCleanupUnconfirmed, errors.New("accepted container's dedicated machine is missing; recover explicitly"))
	}
	switch state["State"] {
	case "stopped":
		return true, nil
	case "running":
		return false, nil
	default:
		return false, errors.Join(core.ErrCleanupUnconfirmed, errors.New("dedicated machine state is not confirmed"))
	}
}
func (p *Podman) state(ctx context.Context, instance sandbox.Instance) (sandbox.Status, error) {
	saved, err := p.load(instance)
	if err != nil {
		return sandbox.Status{}, err
	}
	if saved == nil {
		return sandbox.Status{Phase: "stopped"}, nil
	}
	if stopped, err := p.dedicatedMachineStopped(ctx); err != nil {
		return sandbox.Status{}, err
	} else if stopped {
		return sandbox.Status{Phase: "stopped", Generation: saved.Generation}, nil
	}
	row, err := p.inspect(ctx, *saved)
	if err != nil {
		return sandbox.Status{}, err
	}
	if row != nil {
		generation := bootGeneration(row)
		if nested(row, "State", "Running") == true {
			return sandbox.Status{Phase: "running", Generation: generation}, nil
		}
		return sandbox.Status{Phase: "stopped", Generation: generation}, nil
	}
	return sandbox.Status{Phase: "stopped", Generation: saved.Generation}, nil
}
func (p *Podman) prepare(ctx context.Context, instance sandbox.Instance) (*podmanInstance, error) {
	saved, err := p.load(instance)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		if _, err := p.dedicatedMachineStopped(ctx); err != nil {
			return nil, err
		}
	}
	if err := p.ensureMachine(ctx, instance); err != nil {
		return nil, err
	}
	info, err := p.checked(ctx, "info", "--format=json")
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(info, &value) != nil || nested(value, "host", "security", "rootless") != true || nested(value, "host", "cgroupVersion") != "v2" || nested(value, "host", "cgroupManager") != "systemd" {
		return nil, errors.New("rootless Podman with cgroup v2/systemd is required")
	}
	if saved != nil {
		return saved, nil
	}
	image, err := p.preparedImage(ctx, instance)
	if err != nil {
		return nil, err
	}

	saved = &podmanInstance{Name: "pot-" + rand.Text(), Image: image}
	if err := privateJSON(filepath.Join(instance.Directory(), "podman.json"), saved); err != nil {
		return nil, err
	}
	return saved, nil
}

// The pinned macOS machine labels virtiofs shares nfs_t. Its root broker
// needs the engine domain to create private read-only skill bind mounts.
// Keep SELinux labels/MCS, seccomp and no-new-privileges enabled; Linux retains
// its installed host policy without any label override.
func podmanSecurityOptions(hostOS string) []string {
	options := []string{"--security-opt=no-new-privileges"}
	if hostOS == "darwin" {
		options = append(options, "--security-opt=label=type:container_engine_t")
	}
	return options
}

func (p *Podman) start(ctx context.Context, instance sandbox.Instance) (sandbox.Status, error) {
	saved, err := p.prepare(ctx, instance)
	if err != nil {
		return sandbox.Status{}, err
	}
	row, err := p.inspect(ctx, *saved)
	if err != nil {
		return sandbox.Status{}, err
	}
	if row != nil {
		if nested(row, "State", "Running") == true {
			return p.state(ctx, instance)
		}
		if _, code, err := p.command(ctx, "update", fmt.Sprintf("--cpus=%d", instance.Resources.CPUs), fmt.Sprintf("--memory=%dg", instance.Resources.MemoryGiB), saved.Name); err != nil || code != 0 {
			return sandbox.Status{}, errors.Join(errors.New("Podman resource update failed"), err)
		}
		if _, code, err := p.command(ctx, "start", saved.Name); err != nil || code != 0 {
			return sandbox.Status{}, errors.Join(errors.New("Podman restart failed"), err)
		}
		return p.state(ctx, instance)
	}
	if err := p.ValidateResources(instance, instance.Resources); err != nil {
		return sandbox.Status{}, err
	}
	work := p.dataDirectory(instance)
	if err := os.MkdirAll(filepath.Join(work, "workspace"), 0700); err != nil {
		return sandbox.Status{}, err
	}
	user, builtin := p.config.UserSkills, p.config.BuiltinSkills
	if user == "" || builtin == "" {
		return sandbox.Status{}, errors.New("trusted user/builtin Skill roots must be configured before Podman start")
	}
	for _, root := range []string{user, builtin} {
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil || canonical != root {
			return sandbox.Status{}, errors.New("Skill mount paths must be canonical directories")
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return sandbox.Status{}, errors.New("Skill mount path unavailable")
		}
	}
	token := rand.Text() + rand.Text()
	if err := privateBytes(filepath.Join(instance.Directory(), "executor-token"), []byte(token)); err != nil {
		return sandbox.Status{}, err
	}
	envPath := filepath.Join(instance.Directory(), "podman.env")
	if err := privateBytes(envPath, []byte("POT_EXECUTOR_TOKEN="+token+"\nPOT_CONTAINER=podman\n")); err != nil {
		return sandbox.Status{}, err
	}
	defer os.Remove(envPath)
	saved.Generation++
	if err := privateJSON(filepath.Join(instance.Directory(), "podman.json"), saved); err != nil {
		return sandbox.Status{}, err
	}
	args := []string{"run", "--detach", "--pull=never", "--name", saved.Name, "--label", "dev.pot.instance=" + saved.Name, "--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--cgroupns=private", "--systemd=always", "--stop-signal=SIGTERM", "--cap-add=SYS_ADMIN", "--network=pasta:--no-map-gw", "--no-hosts", "--publish=127.0.0.1::17701", "--publish=127.0.0.1::17681", fmt.Sprintf("--cpus=%d", instance.Resources.CPUs), fmt.Sprintf("--memory=%dg", instance.Resources.MemoryGiB), "--pids-limit=2048", "--env-file", envPath}
	args = append(args, podmanSecurityOptions(p.hostOS)...)
	for _, mount := range []struct{ source, destination, mode string }{{work, "/data", "rw"}, {user, "/run/pot-skill-sources/user", "rw"}, {builtin, "/run/pot-skill-sources/builtin", "ro"}} {
		canonical, err := filepath.EvalSymlinks(mount.source)
		if err != nil {
			return sandbox.Status{}, err
		}
		if strings.ContainsAny(canonical, ":,") {
			return sandbox.Status{}, errors.New("unsupported mount path")
		}
		source, err := p.mountSource(canonical)
		if err != nil {
			return sandbox.Status{}, err
		}
		args = append(args, "--volume", source+":"+mount.destination+":"+mount.mode)
	}
	if err := sandbox.ValidateMappings(instance.Mappings); err != nil {
		return sandbox.Status{}, err
	}
	for _, mapping := range instance.Mappings {
		mode := "rw"
		if mapping.ReadOnly {
			mode = "ro"
		}
		source, err := p.mountSource(mapping.Source)
		if err != nil {
			return sandbox.Status{}, err
		}
		args = append(args, "--volume", source+":"+mapping.Destination+":"+mode)
	}
	args = append(args, saved.Image, "tini", "--", "/bin/sh", "/opt/pot/sandbox.sh")
	if _, code, err := p.command(ctx, args...); err != nil || code != 0 {
		return sandbox.Status{}, errors.Join(errors.New("Podman run failed; inspect before retrying"), err)
	}
	return p.state(ctx, instance)
}
func (p *Podman) Lifecycle(ctx context.Context, instance sandbox.Instance, method string) (sandbox.Status, error) {
	p.mu.Lock()
	p.instances[instance.Directory()] = instance
	p.mu.Unlock()
	switch method {
	case "rebuild":
		state, err := p.Lifecycle(ctx, instance, "delete")
		if err != nil {
			return state, err
		}
		if state.Phase != "stopped" {
			return state, core.ErrCleanupUnconfirmed
		}
		workspace, err := os.OpenRoot(filepath.Dir(p.Workspace(instance)))
		if err != nil {
			return state, err
		}
		err = workspace.RemoveAll("development")
		return state, errors.Join(err, workspace.Close())
	case "status":
		return p.state(ctx, instance)
	case "prepare":
		if _, err := p.prepare(ctx, instance); err != nil {
			return sandbox.Status{}, err
		}
		return p.state(ctx, instance)
	case "start":
		return p.start(ctx, instance)
	case "stop", "delete":
		saved, err := p.load(instance)
		if err != nil {
			return sandbox.Status{}, err
		}
		if saved != nil {
			stopped, err := p.dedicatedMachineStopped(ctx)
			if err != nil {
				return sandbox.Status{}, err
			}
			if stopped && method == "stop" {
				return sandbox.Status{Phase: "stopped", Generation: saved.Generation}, nil
			}
			if stopped {
				// Engine startup does not start retained sibling containers.
				if err := p.ensureMachine(ctx, instance); err != nil {
					return sandbox.Status{}, errors.Join(core.ErrCleanupUnconfirmed, err)
				}
			}
			row, err := p.inspect(ctx, *saved)
			if err != nil {
				return sandbox.Status{}, err
			}
			if row != nil {
				args := []string{"stop", "--time=15", saved.Name}
				if method == "delete" {
					args = []string{"rm", "--force", saved.Name}
				}
				if _, code, err := p.command(ctx, args...); err != nil || code != 0 {
					return sandbox.Status{}, errors.Join(core.ErrCleanupUnconfirmed, err, errors.New("Podman stop/removal unconfirmed"))
				}
			}
		}
		if method == "delete" {
			if saved != nil {
				row, err := p.inspect(ctx, *saved)
				if err != nil || row != nil {
					return sandbox.Status{}, errors.Join(core.ErrCleanupUnconfirmed, err)
				}
			}
			if err := os.Remove(filepath.Join(instance.Directory(), "podman.json")); err != nil && !os.IsNotExist(err) {
				return sandbox.Status{}, err
			}
			return sandbox.Status{Phase: "stopped"}, nil
		}
		state, err := p.state(ctx, instance)
		if err == nil && state.Phase != "stopped" {
			err = core.ErrCleanupUnconfirmed
		}
		return state, err
	default:
		return sandbox.Status{}, errors.New("unsupported Podman lifecycle operation")
	}
}
func (p *Podman) guest(ctx context.Context, instance sandbox.Instance) (platformhelper.GuestClient, error) {
	saved, err := p.load(instance)
	if err != nil || saved == nil {
		return platformhelper.GuestClient{}, errors.New("Podman Sandbox not prepared")
	}
	if stopped, err := p.dedicatedMachineStopped(ctx); err != nil {
		return platformhelper.GuestClient{}, err
	} else if stopped {
		return platformhelper.GuestClient{}, errors.Join(core.ErrCleanupUnconfirmed, errors.New("dedicated machine stopped before original guest cleanup was acknowledged"))
	}
	row, err := p.inspect(ctx, *saved)
	if err != nil {
		return platformhelper.GuestClient{}, err
	}
	if row == nil || nested(row, "State", "Running") != true {
		return platformhelper.GuestClient{}, errors.New("Podman Sandbox stopped")
	}
	ports, _ := nested(row, "NetworkSettings", "Ports", "17701/tcp").([]any)
	if len(ports) != 1 {
		return platformhelper.GuestClient{}, errors.New("invalid guest port mapping")
	}
	port, _ := ports[0].(map[string]any)
	if port["HostIp"] != "127.0.0.1" {
		return platformhelper.GuestClient{}, errors.New("guest executor must bind to loopback")
	}
	text, _ := port["HostPort"].(string)
	number, err := strconv.ParseUint(text, 10, 16)
	if err != nil || number == 0 {
		return platformhelper.GuestClient{}, errors.New("invalid guest executor port")
	}
	token, err := privateFile(filepath.Join(instance.Directory(), "executor-token"), 4096)
	if err != nil {
		return platformhelper.GuestClient{}, err
	}
	return platformhelper.GuestClient{BaseURL: "http://127.0.0.1:" + text, Token: strings.TrimSpace(string(token)), Transport: &http.Transport{Proxy: nil}}, nil
}
func (p *Podman) Health(ctx context.Context, instance sandbox.Instance) (bool, error) {
	client, err := p.guest(ctx, instance)
	if err != nil {
		return false, err
	}
	client.Timeout = 2 * time.Second
	reply, err := client.Call(ctx, "health", nil)
	if err != nil {
		return false, err
	}
	var response struct {
		Ready bool `json:"ready"`
	}
	err = json.Unmarshal(reply, &response)
	return response.Ready, err
}
func (p *Podman) Request(ctx context.Context, instance sandbox.Instance, operation string, body json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	client, err := p.guest(ctx, instance)
	if err != nil {
		return nil, err
	}
	client.Timeout = timeout
	return client.Call(ctx, operation, body)
}
func (p *Podman) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	instances := make([]sandbox.Instance, 0, len(p.instances))
	for _, instance := range p.instances {
		instances = append(instances, instance)
	}
	p.mu.Unlock()
	var result error
	for _, instance := range instances {
		if _, err := p.Lifecycle(ctx, instance, "stop"); err != nil {
			result = errors.Join(result, core.ErrCleanupUnconfirmed, err)
		}
	}
	if result == nil {
		result = p.stopMachine(ctx)
	}
	return result
}
