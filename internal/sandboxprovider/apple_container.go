package sandboxprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/platformhelper"
	"github.com/jjyr/pot/internal/sandbox"
)

type AppleContainer struct {
	Helper           string
	RuntimeResources string
	UserSkills       string
	processes        helperProcesses
}

func NewAppleContainer(helper, resources string) *AppleContainer {
	return &AppleContainer{Helper: helper, RuntimeResources: resources}
}
func (p *AppleContainer) Descriptor() sandbox.Descriptor {
	return sandbox.Descriptor{ID: sandbox.AppleContainer, OS: "linux", HostOS: "macos", Engine: "apple-containerization", Capabilities: sandbox.Capabilities{Shell: true, Files: true, Desktop: true, SharedExecution: true}}
}
func (p *AppleContainer) DefaultResources() sandbox.Resources {
	return sandbox.Resources{CPUs: 4, MemoryGiB: 8, DiskGiB: 32}
}
func (p *AppleContainer) ValidateResources(instance sandbox.Instance, value sandbox.Resources) error {
	if value.CPUs < 1 || value.CPUs > 32 || value.MemoryGiB < 1 || value.MemoryGiB > 128 || value.DiskGiB < 4 || value.DiskGiB > 512 {
		return errors.New("invalid Apple container resource configuration")
	}
	disk := filepath.Join(instance.Directory(), "store/containers/pot-"+instance.ID, "rootfs.ext4")
	if _, err := os.Stat(disk); err == nil {
		var previous struct {
			sandbox.Resources
			Mappings []sandbox.HostMapping `json:"mappings,omitempty"`
		}
		if err := loadJSON(filepath.Join(instance.Directory(), "sandbox.json"), &previous); err != nil {
			return err
		}
		if previous.DiskGiB != value.DiskGiB {
			return errors.New("rebuild Sandbox before changing disk size")
		}
	}
	return nil
}
func (p *AppleContainer) Workspace(instance sandbox.Instance) string {
	return filepath.Join(instance.Directory(), "workspace/workspace")
}
func (p *AppleContainer) GuestWorkspace() string               { return "/data/workspace" }
func (p *AppleContainer) CleanupPolicy() sandbox.CleanupPolicy { return sandbox.ProcessContainment }
func (p *AppleContainer) BindResources(ctx context.Context, instance sandbox.Instance, resources sandbox.PreparedResources) error {
	return immutableResources(instance, resources)
}
func (p *AppleContainer) state(ctx context.Context, instance sandbox.Instance, method string) (map[string]any, error) {
	if runtime.GOOS != "darwin" {
		return nil, errors.New("Apple Containerization requires macOS")
	}
	switch method {
	case "status", "prepare", "start", "stop", "delete":
	default:
		return nil, errors.New("unsupported Apple container lifecycle operation")
	}
	socket := filepath.Join(instance.Root, "helper.sock")
	child, err := p.processes.owned(instance.Root)
	if err != nil {
		return nil, err
	}
	if child == nil {
		if _, err := os.Lstat(socket); !os.IsNotExist(err) {
			return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("unowned Swift Helper socket; recovery required"))
		}
		if method == "status" || method == "stop" {
			phase := "stopped"
			if _, err := os.Stat(p.Helper); err != nil {
				phase = "unavailable"
			}
			return map[string]any{"phase": phase, "generation": uint64(0)}, nil
		}
		if p.Helper == "" || p.RuntimeResources == "" {
			return nil, errors.New("Apple Containerization Helper/resources are not installed")
		}
		if method == "prepare" || method == "start" {
			var resources sandbox.PreparedResources
			if err := loadJSON(filepath.Join(instance.Directory(), "prepared-resources.json"), &resources); err != nil {
				return nil, errors.New("verified Linux resources must be prepared before starting Sandbox")
			}
			if err := validatePaths(resources); err != nil {
				return nil, err
			}
		}
		if !filepath.IsAbs(p.UserSkills) {
			return nil, errors.New("explicit owning Control Skill directory is required")
		}
		if err := p.processes.launchWithEnv(ctx, instance.Root, socket, p.Helper, map[string]string{"POT_AGENT_SKILLS_DIR": p.UserSkills}, instance.Root, p.RuntimeResources, "--sandbox-only"); err != nil {
			return nil, err
		}
	}
	if method == "prepare" || method == "start" {
		if err := sandbox.ValidateMappings(instance.Mappings); err != nil {
			return nil, err
		}
		if err := p.ValidateResources(instance, instance.Resources); err != nil {
			return nil, err
		}
		if err := privateJSON(filepath.Join(instance.Directory(), "sandbox.json"), struct {
			sandbox.Resources
			Mappings []sandbox.HostMapping `json:"mappings,omitempty"`
		}{instance.Resources, instance.Mappings}); err != nil {
			return nil, err
		}
	}
	return (platformhelper.Client{SocketPath: socket, Protocol: platformhelper.Containerization, Timeout: 60 * time.Second}).Call(ctx, platformhelper.Request{Method: method, SandboxID: instance.ID})
}
func (p *AppleContainer) Lifecycle(ctx context.Context, instance sandbox.Instance, method string) (sandbox.Status, error) {
	value, err := p.state(ctx, instance, method)
	if err != nil {
		return sandbox.Status{}, err
	}
	return nativeStatus(value)
}
func (p *AppleContainer) guest(ctx context.Context, instance sandbox.Instance) (platformhelper.GuestClient, error) {
	value, err := p.state(ctx, instance, "status")
	if err != nil {
		return platformhelper.GuestClient{}, err
	}
	if value["phase"] != "running" {
		return platformhelper.GuestClient{}, errors.New("Sandbox is not running")
	}
	address, _ := value["ip"].(string)
	ip := net.ParseIP(address)
	if ip == nil {
		return platformhelper.GuestClient{}, errors.New("Sandbox address unavailable")
	}
	token, err := privateFile(filepath.Join(instance.Directory(), "executor-token"), 4096)
	if err != nil {
		return platformhelper.GuestClient{}, err
	}
	return platformhelper.GuestClient{BaseURL: "http://" + net.JoinHostPort(ip.String(), "17701"), Token: strings.TrimSpace(string(token)), Transport: &http.Transport{Proxy: nil}}, nil
}
func (p *AppleContainer) Health(ctx context.Context, instance sandbox.Instance) (bool, error) {
	client, err := p.guest(ctx, instance)
	if err != nil {
		return false, err
	}
	client.Timeout = time.Second
	reply, err := client.Call(ctx, "health", nil)
	if err != nil {
		return false, err
	}
	var value struct {
		Ready bool `json:"ready"`
	}
	err = json.Unmarshal(reply, &value)
	return value.Ready, err
}
func (p *AppleContainer) Request(ctx context.Context, instance sandbox.Instance, operation string, request json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	client, err := p.guest(ctx, instance)
	if err != nil {
		return nil, err
	}
	client.Timeout = timeout
	return client.Call(ctx, operation, request)
}
func (p *AppleContainer) Shutdown(ctx context.Context) error { return p.processes.shutdown(ctx) }
