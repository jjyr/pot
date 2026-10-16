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

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/platformhelper"
	"github.com/jjyr/pot/internal/sandbox"
)

type PodmanConfig struct {
	Binary        string
	Image         string
	UserSkills    string
	BuiltinSkills string
}
type Podman struct {
	config    PodmanConfig
	mu        sync.Mutex
	instances map[string]sandbox.Instance
}
type podmanInstance struct {
	Name       string `json:"name"`
	Image      string `json:"image"`
	Generation uint64 `json:"generation"`
}

func NewPodman(config PodmanConfig) *Podman {
	if config.Binary == "" {
		config.Binary = "podman"
	}
	if config.Image == "" {
		config.Image = "localhost/pot-sandbox:0.1.0"
	}
	return &Podman{config: config, instances: map[string]sandbox.Instance{}}
}
func (p *Podman) Descriptor() sandbox.Descriptor {
	return sandbox.Descriptor{ID: sandbox.Podman, OS: "linux", HostOS: "linux", Engine: "podman", Capabilities: sandbox.Capabilities{Shell: true, Files: true, Desktop: true, SharedExecution: true}}
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
	return filepath.Join(instance.Directory(), "workspace/workspace")
}
func (p *Podman) GuestWorkspace() string               { return "/data/workspace" }
func (p *Podman) CleanupPolicy() sandbox.CleanupPolicy { return sandbox.ProcessContainment }
func (p *Podman) BindResources(context.Context, sandbox.Instance, sandbox.PreparedResources) error {
	return errors.New("Podman uses an owner-installed local OCI image; it does not consume Apple kernel/boot resources")
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
	if runtime.GOOS != "linux" {
		return nil, -1, errors.New("Podman Provider requires a Linux host")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, p.config.Binary, append([]string{"--remote=false"}, args...)...)
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
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CONTAINER_HOST=") && !strings.HasPrefix(entry, "CONTAINER_CONNECTION=") {
			command.Env = append(command.Env, entry)
		}
	}
	var stdout commandOutput
	stdout.limit = 4 * 1024 * 1024
	command.Stdout = &stdout
	command.Stderr = io.Discard
	err := command.Run()
	code := 0
	if command.ProcessState != nil {
		code = command.ProcessState.ExitCode()
	} else {
		code = -1
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
func (p *Podman) state(ctx context.Context, instance sandbox.Instance) (sandbox.Status, error) {
	saved, err := p.load(instance)
	if err != nil {
		return sandbox.Status{}, err
	}
	if saved == nil {
		return sandbox.Status{Phase: "stopped"}, nil
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
	info, err := p.checked(ctx, "info", "--format=json")
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(info, &value) != nil || nested(value, "host", "security", "rootless") != true || nested(value, "host", "cgroupVersion") != "v2" || nested(value, "host", "cgroupManager") != "systemd" {
		return nil, errors.New("rootless Podman with cgroup v2/systemd is required")
	}
	saved, err := p.load(instance)
	if err != nil || saved != nil {
		return saved, err
	}
	data, err := p.checked(ctx, "image", "inspect", p.config.Image)
	if err != nil {
		return nil, err
	}
	var images []map[string]any
	if json.Unmarshal(data, &images) != nil || len(images) != 1 || nested(images[0], "Labels", "dev.pot.bootstrap") != "pot-executor-v2" {
		return nil, errors.New("install the pinned Pot OCI image with its guest executor before preparing Sandbox")
	}
	image, _ := images[0]["Id"].(string)
	if image == "" {
		return nil, errors.New("Podman image identity missing")
	}
	saved = &podmanInstance{Name: "pot-" + rand.Text(), Image: image}
	if err := privateJSON(filepath.Join(instance.Directory(), "podman.json"), saved); err != nil {
		return nil, err
	}
	return saved, nil
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
	work := filepath.Join(instance.Directory(), "workspace")
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
	args := []string{"run", "--detach", "--pull=never", "--name", saved.Name, "--label", "dev.pot.instance=" + saved.Name, "--userns=keep-id:uid=1000,gid=1000", "--user=0:0", "--cgroupns=private", "--systemd=always", "--stop-signal=SIGTERM", "--cap-add=SYS_ADMIN", "--security-opt=no-new-privileges", "--network=pasta:--no-map-gw", "--no-hosts", "--publish=127.0.0.1::17701", "--publish=127.0.0.1::17681", fmt.Sprintf("--cpus=%d", instance.Resources.CPUs), fmt.Sprintf("--memory=%dg", instance.Resources.MemoryGiB), "--pids-limit=2048", "--env-file", envPath}
	for _, mount := range []struct{ source, destination, mode string }{{work, "/data", "rw"}, {user, "/run/pot-skill-sources/user", "rw"}, {builtin, "/run/pot-skill-sources/builtin", "ro"}} {
		canonical, err := filepath.EvalSymlinks(mount.source)
		if err != nil {
			return sandbox.Status{}, err
		}
		if strings.ContainsAny(canonical, ":,") {
			return sandbox.Status{}, errors.New("unsupported mount path")
		}
		args = append(args, "--volume", canonical+":"+mount.destination+":"+mount.mode)
	}
	if err := sandbox.ValidateMappings(instance.Mappings); err != nil {
		return sandbox.Status{}, err
	}
	for _, mapping := range instance.Mappings {
		mode := "rw"
		if mapping.ReadOnly {
			mode = "ro"
		}
		args = append(args, "--volume", mapping.Source+":"+mapping.Destination+":"+mode)
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
	return result
}
