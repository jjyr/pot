package runtime

import (
	"github.com/jjyr/pot/internal/control"
	"os"
	"path/filepath"
	"runtime"
)

// prepareAssets locates trusted tool and Skill packages without starting processes.
func (r *Runtime) prepareAssets(options Options, config *control.Options) error {
	resources := options.ResourceDirectory
	if resources == "" {
		executable, _ := os.Executable()
		candidate := filepath.Join(filepath.Dir(executable), "../Resources")
		if info, err := os.Stat(filepath.Join(candidate, "skills")); err == nil && info.IsDir() {
			resources = candidate
		} else {
			resources = ".local/go/runtime-resources"
		}
	}
	resources, _ = filepath.Abs(resources)
	r.AssetDirectory = resources
	config.SandboxDirectory = filepath.Join(resources, "sandbox")
	if config.AgentHome == "" {
		config.AgentHome = filepath.Join(r.Root.Path, "agent")
	}
	if config.BuiltinSkills == "" {
		config.BuiltinSkills = filepath.Join(resources, "skills")
	}
	if config.PluginRoot == "" {
		config.PluginRoot = filepath.Join(resources, "plugins")
	}
	if config.Node == "" {
		candidate := filepath.Join(resources, "node/bin/node")
		if runtime.GOOS == "windows" {
			candidate = filepath.Join(resources, "node/node.exe")
		}
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			config.Node = candidate
		}
	}
	if err := os.MkdirAll(filepath.Join(config.AgentHome, "skills"), 0700); err != nil {
		return err
	}
	return nil
}
