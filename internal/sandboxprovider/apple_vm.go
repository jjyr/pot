package sandboxprovider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/platformhelper"
	"github.com/jjyr/pot/internal/sandbox"
)

type MacOSManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Provider      string `json:"provider"`
	GuestBuild    string `json:"guest_build"`
	XcodeVersion  string `json:"xcode_version"`
	Bootstrap     string `json:"bootstrap"`
	GuestIP       string `json:"guest_ip"`
	MACAddress    string `json:"mac_address"`
}

var macOSFiles = []string{"manifest.json", "hardware-model", "machine-identifier", "auxiliary-storage", "disk.img", "guest-ca.pem", "executor-token"}

func LoadMacOSManifest(bundle string) (MacOSManifest, error) {
	var manifest MacOSManifest
	info, err := os.Lstat(bundle)
	if err != nil {
		return manifest, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Getuid()) {
		return manifest, errors.New("macOS bundle must be a private current-user directory")
	}
	for _, name := range macOSFiles {
		info, err := os.Lstat(filepath.Join(bundle, name))
		if err != nil {
			return manifest, err
		}
		entry, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || entry.Uid != stat.Uid {
			return manifest, errors.New("invalid or non-private macOS enrollment file")
		}
	}
	if err := loadJSON(filepath.Join(bundle, "manifest.json"), &manifest); err != nil {
		return manifest, err
	}
	if manifest.SchemaVersion != 1 || manifest.Provider != sandbox.AppleVM || manifest.GuestBuild == "" || manifest.XcodeVersion == "" || manifest.Bootstrap != "pot-macos-executor-v3" {
		return manifest, errors.New("incompatible macOS bundle; install pot-macos-executor-v3 and enroll complete stopped instance")
	}
	ip := net.ParseIP(manifest.GuestIP)
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return manifest, errors.New("macOS guest must use a private IPv4 address")
	}
	mac, err := net.ParseMAC(manifest.MACAddress)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || mac[0]&2 == 0 {
		return manifest, errors.New("macOS VM MAC must be locally administered unicast")
	}
	return manifest, nil
}

type AppleVMConfig struct {
	Helper        string
	UserSkills    string
	BuiltinSkills string
}
type AppleVM struct {
	config    AppleVMConfig
	processes helperProcesses
}

func NewAppleVM(config AppleVMConfig) *AppleVM { return &AppleVM{config: config} }
func (p *AppleVM) Descriptor() sandbox.Descriptor {
	return sandbox.Descriptor{ID: sandbox.AppleVM, OS: "macos", HostOS: "macos", Engine: "apple-virtualization", Capabilities: sandbox.Capabilities{Shell: true, Files: true, Desktop: true, SharedExecution: false}}
}
func (p *AppleVM) DefaultResources() sandbox.Resources {
	return sandbox.Resources{CPUs: 4, MemoryGiB: 8, DiskGiB: 64}
}
func (p *AppleVM) ValidateResources(instance sandbox.Instance, value sandbox.Resources) error {
	if value.CPUs < 2 || value.CPUs > 32 || value.MemoryGiB < 4 || value.MemoryGiB > 128 || value.DiskGiB < 64 || value.DiskGiB > 512 {
		return errors.New("macOS needs 2+ CPUs, 4+ GiB RAM and a prepared 64+ GiB disk")
	}
	info, err := os.Stat(filepath.Join(instance.Directory(), "macos/disk.img"))
	if err != nil {
		return err
	}
	if uint64(info.Size()) != value.DiskGiB*1024*1024*1024 {
		return errors.New("macOS disk resizing is unsupported; retain enrolled disk size")
	}
	return nil
}
func (p *AppleVM) Workspace(instance sandbox.Instance) string {
	return filepath.Join(instance.Directory(), "workspace/workspace")
}
func (p *AppleVM) GuestWorkspace() string               { return "/Volumes/PotWorkspace/workspace" }
func (p *AppleVM) CleanupPolicy() sandbox.CleanupPolicy { return sandbox.SessionClosure }
func (p *AppleVM) BindResources(context.Context, sandbox.Instance, sandbox.PreparedResources) error {
	return errors.New("macOS uses owner-prepared enrollment; Linux image resources are unsupported")
}
func (p *AppleVM) Lifecycle(ctx context.Context, instance sandbox.Instance, method string) (sandbox.Status, error) {
	switch method {
	case "status", "prepare", "start", "stop":
	default:
		return sandbox.Status{}, errors.New("macOS enrolled VM supports prepare/start/status/stop; automatic destruction is unsupported")
	}
	directory := instance.Directory()
	socket := filepath.Join(directory, "macos-helper.sock")
	child, err := p.processes.owned(directory)
	if err != nil {
		return sandbox.Status{}, err
	}
	if child == nil {
		if _, err := os.Lstat(socket); !os.IsNotExist(err) {
			return sandbox.Status{}, errors.Join(core.ErrCleanupUnconfirmed, errors.New("unowned macOS Helper socket; prove old Helper/VM exit before recovery"))
		}
		if method == "status" || method == "stop" {
			_, manifestErr := LoadMacOSManifest(filepath.Join(directory, "macos"))
			_, helperErr := os.Stat(p.config.Helper)
			phase := "stopped"
			if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" || manifestErr != nil || helperErr != nil {
				phase = "unavailable"
			}
			generation := uint64(0)
			if data, err := os.ReadFile(filepath.Join(directory, "macos-generation")); err == nil {
				generation, err = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
				if err != nil {
					return sandbox.Status{}, err
				}
			}
			return sandbox.Status{Phase: phase, Generation: generation}, nil
		}
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			return sandbox.Status{}, errors.New("macOS VM requires Apple silicon macOS")
		}
		if _, err := LoadMacOSManifest(filepath.Join(directory, "macos")); err != nil {
			return sandbox.Status{}, err
		}
		if err := p.ValidateResources(instance, instance.Resources); err != nil {
			return sandbox.Status{}, err
		}
		if err := privateJSON(filepath.Join(directory, "sandbox.json"), instance.Resources); err != nil {
			return sandbox.Status{}, err
		}
		if err := os.MkdirAll(p.Workspace(instance), 0700); err != nil {
			return sandbox.Status{}, err
		}
		if err := snapshotSkills(filepath.Join(directory, "macos-skills"), p.config.BuiltinSkills, p.config.UserSkills); err != nil {
			return sandbox.Status{}, err
		}
		if err := p.processes.launch(ctx, directory, socket, p.config.Helper, directory); err != nil {
			return sandbox.Status{}, err
		}
	}
	value, err := (platformhelper.Client{SocketPath: socket, Protocol: platformhelper.MacOSVirtualization, Timeout: 60 * time.Second}).Call(ctx, platformhelper.Request{Method: method})
	if err != nil {
		return sandbox.Status{}, err
	}
	state, err := nativeStatus(value)
	if err != nil {
		return state, err
	}
	if method == "stop" {
		if state.Phase != "stopped" {
			return state, core.ErrCleanupUnconfirmed
		}
		if err := p.processes.stopAfterVMStopped(ctx, directory, socket); err != nil {
			return state, err
		}
	}
	return state, nil
}
func (p *AppleVM) guest(ctx context.Context, instance sandbox.Instance) (platformhelper.GuestClient, error) {
	manifest, err := LoadMacOSManifest(filepath.Join(instance.Directory(), "macos"))
	if err != nil {
		return platformhelper.GuestClient{}, err
	}
	state, err := p.Lifecycle(ctx, instance, "status")
	if err != nil || state.Phase != "running" {
		return platformhelper.GuestClient{}, errors.Join(errors.New("macOS VM is not running"), err)
	}
	ca, err := privateFile(filepath.Join(instance.Directory(), "macos/guest-ca.pem"), 65536)
	if err != nil {
		return platformhelper.GuestClient{}, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return platformhelper.GuestClient{}, errors.New("invalid dedicated macOS guest CA")
	}
	token, err := privateFile(filepath.Join(instance.Directory(), "macos/executor-token"), 4096)
	if err != nil {
		return platformhelper.GuestClient{}, err
	}
	credential := strings.TrimSpace(string(token))
	if len(credential) < 32 || len(credential) > 256 || strings.Trim(credential, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return platformhelper.GuestClient{}, errors.New("invalid enrolled guest credential")
	}
	return platformhelper.GuestClient{BaseURL: "https://" + net.JoinHostPort(manifest.GuestIP, "17701"), Token: credential, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}}, nil
}
func (p *AppleVM) Health(ctx context.Context, instance sandbox.Instance) (bool, error) {
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
		Ready    bool   `json:"ready"`
		Protocol string `json:"protocol"`
	}
	err = json.Unmarshal(reply, &response)
	return response.Ready && response.Protocol == "pot-macos-executor-v3", err
}
func (p *AppleVM) Request(ctx context.Context, instance sandbox.Instance, operation string, body json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	switch operation {
	case "execute", "finish", "cancel", "inspect", "prepare":
	default:
		return nil, errors.New("unsupported macOS guest capability")
	}
	client, err := p.guest(ctx, instance)
	if err != nil {
		return nil, err
	}
	client.Timeout = timeout
	return client.Call(ctx, operation, body)
}
func (p *AppleVM) Shutdown(ctx context.Context) error {
	p.processes.mu.Lock()
	directories := []string{}
	for directory := range p.processes.children {
		directories = append(directories, directory)
	}
	p.processes.mu.Unlock()
	var result error
	for _, directory := range directories {
		instance := sandbox.Instance{Root: filepath.Dir(filepath.Dir(directory)), ID: filepath.Base(directory)}
		if _, err := p.Lifecycle(ctx, instance, "stop"); err != nil {
			result = errors.Join(result, core.ErrCleanupUnconfirmed, err)
		}
	}
	return result
}

func (p *helperProcesses) stopAfterVMStopped(ctx context.Context, root, socket string) error {
	p.mu.Lock()
	child := p.children[root]
	p.mu.Unlock()
	if child == nil {
		return errors.New("cannot stop an unowned Helper")
	}
	if err := child.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return errors.Join(core.ErrCleanupUnconfirmed, err)
	}
	select {
	case <-child.done:
	case <-ctx.Done():
		return errors.Join(core.ErrCleanupUnconfirmed, ctx.Err())
	}
	p.mu.Lock()
	delete(p.children, root)
	p.mu.Unlock()
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
func snapshotSkills(destination string, roots ...string) error {
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".pot-skills-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	var copyTree func(string, string, int) error
	copyTree = func(source, target string, depth int) error {
		if depth >= 32 {
			return errors.New("Skill tree too deep")
		}
		info, err := os.Lstat(source)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return err
			}
			entries, err := os.ReadDir(source)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := copyTree(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name()), depth+1); err != nil {
					return err
				}
			}
		} else if info.Mode().IsRegular() {
			if info.Size() > 16*1024*1024 {
				return errors.New("Skill file exceeds 16 MiB")
			}
			input, err := os.Open(source)
			if err != nil {
				return err
			}
			defer input.Close()
			output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, input)
			closeErr := output.Close()
			return errors.Join(copyErr, closeErr)
		}
		return nil
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			target := filepath.Join(staging, entry.Name())
			if _, err := os.Lstat(target); err == nil {
				continue
			}
			if err := copyTree(filepath.Join(root, entry.Name()), target, 0); err != nil {
				return err
			}
		}
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return os.Rename(staging, destination)
}
