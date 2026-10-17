package sandboxprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/sandbox"
)

const defaultPodmanMachineImage = "docker://quay.io/podman/machine-os@sha256:c536c6cf694704cb60fbf26703ed5ca6f31ca7743d55d0ff6acec71cea79ea51"

type machineShare struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}
type podmanMachine struct {
	SchemaVersion int            `json:"schema_version"`
	Initialized   bool           `json:"initialized"`
	Name          string         `json:"name"`
	RuntimeRoot   string         `json:"runtime_root"`
	Image         string         `json:"image"`
	CPUs          uint64         `json:"cpus"`
	MemoryGiB     uint64         `json:"memory_gib"`
	DiskGiB       uint64         `json:"disk_gib"`
	Shares        []machineShare `json:"shares"`
}

func (p *Podman) machineIdentity() (string, string, error) {
	root := p.config.RuntimeRoot
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return "", "", errors.New("Podman machine requires a canonical private Runtime execution root")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return "", "", errors.New("Podman Runtime root must not use symlinks")
	}
	name := p.config.MachineName
	if name == "" {
		hash := sha256.Sum256([]byte(root))
		name = "pot-" + hex.EncodeToString(hash[:8])
	}
	if len(name) > 40 || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" || !strings.HasPrefix(name, "pot-") {
		return "", "", errors.New("dedicated Podman machine name must start with pot- and use lowercase letters, digits or hyphens")
	}
	return name, filepath.Join(root, "podman-machine"), nil
}
func (p *Podman) commandEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "CONTAINER_HOST" || key == "CONTAINER_CONNECTION" {
			continue
		}
		if p.hostOS == "darwin" && (strings.HasPrefix(key, "CONTAINERS_") || strings.HasPrefix(key, "CONTAINER_") || strings.HasPrefix(key, "PODMAN_") || key == "XDG_CONFIG_HOME" || key == "XDG_DATA_HOME" || key == "XDG_CACHE_HOME") {
			continue
		}
		env = append(env, entry)
	}
	if p.hostOS == "darwin" {
		root := filepath.Join(p.config.RuntimeRoot, "podman-machine")
		env = append(env, "XDG_CONFIG_HOME="+filepath.Join(root, "config"), "XDG_DATA_HOME="+filepath.Join(root, "data"), "XDG_CACHE_HOME="+filepath.Join(root, "cache"), "CONTAINERS_CONF="+filepath.Join(root, "containers.conf"), "PODMAN_CONNECTIONS_CONF="+filepath.Join(root, "connections.json"), "CONTAINERS_MACHINE_PROVIDER=applehv")
	}
	return env
}
func (p *Podman) dataDirectory(i sandbox.Instance) string {
	if p.hostOS == "darwin" {
		return filepath.Join(p.config.RuntimeRoot, "podman-shared", "sandboxes", i.ID)
	}
	return filepath.Join(i.Directory(), "workspace")
}
func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
func canonicalShare(source, target string, readOnly bool) (machineShare, error) {
	home, err := os.UserHomeDir()
	if err == nil {
		if canonical, e := filepath.EvalSymlinks(home); e == nil {
			home = canonical
		}
		if pathWithin(home, source) {
			return machineShare{}, errors.New("Podman machine cannot share HOME or its parent; choose an explicit project directory")
		}
	}
	if !filepath.IsAbs(source) || source == "/" || strings.ContainsAny(source, ":,\n\x00") {
		return machineShare{}, errors.New("unsupported Podman machine share")
	}
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil || canonical != source {
		return machineShare{}, errors.New("Podman machine shares must be canonical existing directories")
	}
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return machineShare{}, errors.New("Podman machine share must be a directory")
	}
	return machineShare{source, target, readOnly}, nil
}
func (p *Podman) machinePlan(i sandbox.Instance) (podmanMachine, error) {
	name, _, err := p.machineIdentity()
	if err != nil {
		return podmanMachine{}, err
	}
	if !strings.Contains(p.config.MachineImage, "@sha256:") {
		return podmanMachine{}, errors.New("configure a digest-pinned official Podman machine OS image before preparing the dedicated machine")
	}
	plan := podmanMachine{SchemaVersion: 1, Name: name, RuntimeRoot: p.config.RuntimeRoot, Image: p.config.MachineImage, CPUs: p.config.MachineCPUs, MemoryGiB: p.config.MachineMemoryGiB, DiskGiB: p.config.MachineDiskGiB}
	if plan.CPUs < 1 || plan.CPUs > 32 || plan.MemoryGiB < 1 || plan.MemoryGiB > 128 || plan.DiskGiB < 10 || plan.DiskGiB > 1024 {
		return plan, errors.New("invalid Podman machine resources")
	}
	for _, item := range []machineShare{{filepath.Join(p.config.RuntimeRoot, "podman-shared"), "/mnt/pot/shared", false}, {p.config.UserSkills, "/mnt/pot/user-skills", false}, {p.config.BuiltinSkills, "/mnt/pot/builtin-skills", true}} {
		share, err := canonicalShare(item.Source, item.Target, item.ReadOnly)
		if err != nil {
			return plan, err
		}
		plan.Shares = append(plan.Shares, share)
	}
	if err = sandbox.ValidateMappings(i.Mappings); err != nil {
		return plan, err
	}
	mappings := append([]sandbox.HostMapping{}, i.Mappings...)
	sort.Slice(mappings, func(a, b int) bool { return mappings[a].Source < mappings[b].Source })
	for _, m := range mappings {
		covered := false
		for _, share := range plan.Shares {
			if pathWithin(m.Source, share.Source) && (!share.ReadOnly || m.ReadOnly) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		if pathWithin(p.config.RuntimeRoot, m.Source) || pathWithin(m.Source, p.config.RuntimeRoot) {
			return plan, errors.New("Podman machine cannot share Runtime databases, credentials or their parent directory")
		}
		digest := sha256.Sum256([]byte(m.Source))
		share, err := canonicalShare(m.Source, "/mnt/pot/host-"+hex.EncodeToString(digest[:6]), m.ReadOnly)
		if err != nil {
			return plan, err
		}
		plan.Shares = append(plan.Shares, share)
	}
	return plan, nil
}
func (p *Podman) loadMachine() (podmanMachine, error) {
	_, root, err := p.machineIdentity()
	if err != nil {
		return podmanMachine{}, err
	}
	var plan podmanMachine
	err = loadJSON(filepath.Join(root, "owner.json"), &plan)
	if err != nil {
		return plan, err
	}
	name, _, _ := p.machineIdentity()
	if plan.SchemaVersion != 1 || plan.Name != name || plan.RuntimeRoot != p.config.RuntimeRoot {
		return plan, errors.New("Podman machine ownership differs from this Runtime")
	}
	return plan, nil
}
func (p *Podman) mountSource(source string) (string, error) {
	if p.hostOS != "darwin" {
		return source, nil
	}
	plan, err := p.loadMachine()
	if err != nil {
		return "", err
	}
	// Prefer the most specific share so a separately readonly root cannot be
	// reached through a broader writable mapping by accident.
	sort.Slice(plan.Shares, func(a, b int) bool { return len(plan.Shares[a].Source) > len(plan.Shares[b].Source) })
	for _, share := range plan.Shares {
		if pathWithin(source, share.Source) {
			relative, _ := filepath.Rel(share.Source, source)
			return filepath.ToSlash(filepath.Join(share.Target, relative)), nil
		}
	}
	return "", errors.New("Host mapping is outside this Podman machine's pinned shares; configure its approved roots before creating a new dedicated machine")
}
func (p *Podman) machineInspect(ctx context.Context) (map[string]any, error) {
	name, root, err := p.machineIdentity()
	if err != nil {
		return nil, err
	}
	raw, code, err := p.runCommand(ctx, nil, "machine", "list", "--format=json")
	if err != nil {
		return nil, errors.Join(core.ErrCleanupUnconfirmed, err)
	}
	if code != 0 {
		return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("cannot establish dedicated Podman machine existence"))
	}
	var machines []struct{ Name string }
	if json.Unmarshal(raw, &machines) != nil || machines == nil {
		return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("invalid Podman machine inventory"))
	}
	found := false
	for _, machine := range machines {
		if machine.Name == "" {
			return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("invalid Podman machine inventory"))
		}
		if machine.Name == name {
			if found {
				return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("ambiguous dedicated Podman machine identity"))
			}
			found = true
		}
	}
	if !found {
		return nil, nil
	}
	raw, code, err = p.runCommand(ctx, nil, "machine", "inspect", name)
	if err != nil || code != 0 {
		return nil, errors.Join(core.ErrCleanupUnconfirmed, err, errors.New("known dedicated Podman machine inspection failed"))
	}
	var rows []map[string]any
	if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 || rows[0]["Name"] != name || rows[0]["Rootful"] != false {
		return nil, errors.New("dedicated Podman machine identity invalid")
	}
	configDir, _ := nested(rows[0], "ConfigDir", "Path").(string)
	if !pathWithin(configDir, filepath.Join(root, "config")) {
		return nil, errors.New("Podman machine configuration escaped its private Runtime root")
	}
	return rows[0], nil
}
func (p *Podman) ensureMachine(ctx context.Context, i sandbox.Instance) error {
	if p.hostOS != "darwin" {
		return nil
	}
	p.machineMu.Lock()
	defer p.machineMu.Unlock()
	_, root, err := p.machineIdentity()
	if err != nil {
		return err
	}
	for _, dir := range []string{root, filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "cache"), filepath.Join(p.config.RuntimeRoot, "podman-shared")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		info, e := os.Lstat(dir)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Podman machine storage must not use symlinks")
		}
	}
	if _, err = os.Stat(filepath.Join(root, "containers.conf")); os.IsNotExist(err) {
		if err = privateBytes(filepath.Join(root, "containers.conf"), []byte("[machine]\nvolumes = []\n")); err != nil {
			return err
		}
	}
	plan, err := p.loadMachine()
	if os.IsNotExist(err) {
		existing, e := p.machineInspect(ctx)
		if e != nil {
			return e
		}
		if existing != nil {
			return errors.New("refusing to adopt an unowned Podman machine")
		}
		plan, err = p.machinePlan(i)
		if err != nil {
			return err
		}
		if err = privateJSON(filepath.Join(root, "owner.json"), plan); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if plan.Image != p.config.MachineImage || plan.CPUs != p.config.MachineCPUs || plan.MemoryGiB != p.config.MachineMemoryGiB || plan.DiskGiB != p.config.MachineDiskGiB {
		return errors.New("Podman machine configuration is pinned; stop all owned sandboxes before explicit machine replacement")
	}
	for _, source := range []string{p.dataDirectory(i), p.config.UserSkills, p.config.BuiltinSkills} {
		if _, err = p.mountSource(source); err != nil {
			return err
		}
	}
	for _, mapping := range i.Mappings {
		if _, err = p.mountSource(mapping.Source); err != nil {
			return err
		}
	}
	state, err := p.machineInspect(ctx)
	if err != nil {
		return err
	}
	if state == nil {
		if plan.Initialized {
			return errors.Join(core.ErrCleanupUnconfirmed, errors.New("initialized dedicated Podman machine is missing; recover all original containers before explicit engine replacement"))
		}
		prepareImage := p.prepareMachineImage
		if prepareImage == nil {
			prepareImage = preparePodmanMachineImage
		}
		imagePath, e := prepareImage(ctx, filepath.Join(root, "cache"), plan.Image)
		if e != nil {
			return e
		}
		args := []string{"machine", "init", "--provider=applehv", "--rootful=false", "--update-connection=false", fmt.Sprintf("--cpus=%d", plan.CPUs), fmt.Sprintf("--memory=%d", plan.MemoryGiB*1024), fmt.Sprintf("--disk-size=%d", plan.DiskGiB), "--image=" + imagePath}
		for _, share := range plan.Shares {
			mode := "rw"
			if share.ReadOnly {
				mode = "ro"
			}
			args = append(args, "--volume="+share.Source+":"+share.Target+":"+mode)
		}
		args = append(args, plan.Name)
		if _, code, e := p.runCommand(ctx, nil, args...); e != nil || code != 0 {
			return errors.Join(e, errors.New("dedicated Podman machine initialization failed; inspect the owned machine before retrying"))
		}
		state, err = p.machineInspect(ctx)
		if err != nil || state == nil {
			return errors.Join(err, errors.New("new dedicated Podman machine not found"))
		}
	}
	// The on-disk machine configuration is private and generated by the pinned
	// CLI. Verify mounts rather than trusting a stale owner marker alone.
	configDir, _ := nested(state, "ConfigDir", "Path").(string)
	raw, err := os.ReadFile(filepath.Join(configDir, plan.Name+".json"))
	if err != nil {
		return err
	}
	var actual struct {
		Mounts []struct {
			Source   string
			Target   string
			ReadOnly bool
		}
	}
	if json.Unmarshal(raw, &actual) != nil || len(actual.Mounts) != len(plan.Shares) {
		return errors.New("dedicated Podman machine shares changed")
	}
	for _, share := range plan.Shares {
		found := false
		for _, mount := range actual.Mounts {
			if mount.Source == share.Source && mount.Target == share.Target && mount.ReadOnly == share.ReadOnly {
				found = true
			}
		}
		if !found {
			return errors.New("dedicated Podman machine shares changed")
		}
	}
	if !plan.Initialized {
		plan.Initialized = true
		if err := privateJSON(filepath.Join(root, "owner.json"), plan); err != nil {
			return err
		}
	}
	if state["State"] == "stopped" {
		if _, code, e := p.runCommand(ctx, nil, "machine", "start", plan.Name); e != nil || code != 0 {
			return errors.Join(core.ErrCleanupUnconfirmed, e, errors.New("dedicated Podman machine start unconfirmed"))
		}
		state, err = p.machineInspect(ctx)
		if err != nil {
			return err
		}
	}
	if state == nil || state["State"] != "running" {
		return errors.Join(core.ErrCleanupUnconfirmed, errors.New("dedicated Podman machine is not running"))
	}
	return nil
}
func (p *Podman) stopMachine(ctx context.Context) error {
	if p.hostOS != "darwin" {
		return nil
	}
	p.machineMu.Lock()
	defer p.machineMu.Unlock()
	plan, err := p.loadMachine()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	state, err := p.machineInspect(ctx)
	if err != nil {
		return err
	}
	if state == nil || state["State"] == "stopped" {
		return nil
	}
	raw, code, err := p.command(ctx, "ps", "--format=json")
	if err != nil || code != 0 {
		return errors.Join(core.ErrCleanupUnconfirmed, err, errors.New("cannot prove dedicated machine containers stopped"))
	}
	var running []json.RawMessage
	if json.Unmarshal(raw, &running) != nil || len(running) != 0 {
		return errors.Join(core.ErrCleanupUnconfirmed, errors.New("dedicated machine still has running containers; leaving it running"))
	}
	if _, code, err = p.runCommand(ctx, nil, "machine", "stop", plan.Name); err != nil || code != 0 {
		return errors.Join(core.ErrCleanupUnconfirmed, err, errors.New("dedicated Podman machine shutdown unconfirmed"))
	}
	state, err = p.machineInspect(ctx)
	if err != nil || state == nil || state["State"] != "stopped" {
		return errors.Join(core.ErrCleanupUnconfirmed, err, errors.New("dedicated Podman machine stop not confirmed"))
	}
	return nil
}
