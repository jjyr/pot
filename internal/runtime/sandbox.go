package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/executor"
	"github.com/jjyr/pot/internal/resource"
	"github.com/jjyr/pot/internal/resourcecontrol"
	"github.com/jjyr/pot/internal/sandbox"
	"github.com/jjyr/pot/internal/sandboxprovider"
	"github.com/jjyr/pot/internal/sandboxresources"
)

func (r *Runtime) openSandboxes(ctx context.Context, options Options, config *control.Options) error {
	resources := options.ResourceDirectory
	helpers := options.HelperDirectory
	if resources == "" {
		executable, _ := os.Executable()
		candidate := filepath.Join(filepath.Dir(executable), "../Resources")
		if info, err := os.Stat(filepath.Join(candidate, "skills")); err == nil && info.IsDir() {
			resources = candidate
			helpers = filepath.Dir(executable)
		} else {
			resources = ".local/go/resources"
			helpers = ".local/go/helper-build/release"
		}
	}
	resources, _ = filepath.Abs(resources)
	helpers, _ = filepath.Abs(helpers)
	r.AssetDirectory = resources
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
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			config.Node = candidate
		}
	}
	if err := os.MkdirAll(filepath.Join(config.AgentHome, "skills"), 0700); err != nil {
		return err
	}
	providers := options.SandboxProviders
	if providers == nil {
		switch runtime.GOOS {
		case "darwin":
			apple := sandboxprovider.NewAppleContainer(filepath.Join(helpers, "PotHelper"), resources)
			apple.UserSkills = filepath.Join(config.AgentHome, "skills")
			providers = []sandbox.Provider{apple, sandboxprovider.NewAppleVM(sandboxprovider.AppleVMConfig{Helper: filepath.Join(helpers, "PotMacOSHelper"), UserSkills: filepath.Join(config.AgentHome, "skills"), BuiltinSkills: config.BuiltinSkills})}
		case "linux":
			providers = []sandbox.Provider{sandboxprovider.NewPodman(sandboxprovider.PodmanConfig{Image: options.SandboxImage, UserSkills: filepath.Join(config.AgentHome, "skills"), BuiltinSkills: config.BuiltinSkills})}
		}
	}
	var err error
	r.Sandboxes, err = sandbox.Open(ctx, sandbox.Config{Root: filepath.Join(r.Root.Path, "execution"), RuntimeID: r.Root.Identity.RuntimeID, Providers: providers})
	if err != nil {
		return err
	}
	r.Resources, err = resource.New(filepath.Join(r.Root.Path, "resource-cache"), resource.Options{Context: ctx})
	if err != nil {
		return err
	}
	catalog, err := sandboxresources.Embedded()
	if err != nil {
		return err
	}
	r.Provisioning, err = resourcecontrol.Open(resourcecontrol.Options{Root: r.Root.Path, Catalog: catalog, Store: r.Resources, Bind: func(ctx context.Context, id string, prepared sandbox.PreparedResources) error {
		if id == "shared" {
			definition, err := r.Sandboxes.EnsureShared(ctx)
			if err != nil {
				return err
			}
			if definition.Provider != sandbox.AppleContainer {
				return errors.New("this Sandbox uses an owner-installed image or prepared macOS VM")
			}
		}
		return r.Sandboxes.BindResources(ctx, id, prepared)
	}, Notify: func() {
		if r.Control != nil {
			r.Control.Changes.Notify("sandbox_resources")
		}
	}})
	if err != nil {
		return err
	}
	config.Sandboxes = r.Sandboxes
	config.ResolveSandbox = func(ctx context.Context) (core.ExecutionTarget, error) {
		definition, err := r.Sandboxes.ResolveSelected(ctx)
		if err != nil {
			return core.ExecutionTarget{}, err
		}
		return core.SandboxTarget(r.Root.Identity.RuntimeID, definition.ID), nil
	}
	previous := config.Target
	config.Target = func(ctx context.Context, op control.Operation, request executor.Request, emit func(core.Update) error) (json.RawMessage, error) {
		if op.Target.IsLocalTo(r.Root.Identity.RuntimeID) && op.Target.Target.Kind == "sandbox" {
			definitions, err := r.Sandboxes.Inventory(ctx)
			if err != nil {
				return nil, err
			}
			for _, definition := range definitions {
				if definition.ID == op.Target.Target.SandboxID && definition.Provider == sandbox.AppleContainer {
					if err = r.Provisioning.Wait(ctx, definition.ID, ""); err != nil {
						return nil, err
					}
					break
				}
			}
			grant := sandbox.AccessGrant{Target: op.Target, Owner: op.Owner, Turn: op.Turn, ThreadGeneration: op.Generation, Policy: request.Policy, Worker: op.Worker, WorkspaceOwner: op.Owner}

			args, err := json.Marshal(request.Args)
			if err != nil {
				return nil, err
			}
			prepared, err := r.Sandboxes.PrepareOperation(ctx, grant, request.Operation, args)
			if err != nil {
				return nil, err
			}
			return r.Sandboxes.Execute(ctx, grant, request.ID, request.Operation, prepared, emit)
		}
		if previous != nil {
			return previous(ctx, op, request, emit)
		}
		return nil, errors.New("execution destination unavailable; no fallback")
	}
	previousCleanup := config.CleanupTarget
	config.CleanupTarget = func(ctx context.Context, owner string, abort bool) error {
		var err error
		if abort {
			err = r.Sandboxes.Cancel(ctx, owner)
		} else {
			err = r.Sandboxes.Finish(ctx, owner)
		}
		if previousCleanup != nil {
			err = errors.Join(err, previousCleanup(ctx, owner, abort))
		}
		return err
	}
	return nil
}
func (r *Runtime) ImportSandboxResources(ctx context.Context, directory string) error {
	definition, err := r.Sandboxes.EnsureShared(ctx)
	if err != nil {
		return err
	}
	if definition.Provider != sandbox.AppleContainer {
		return errors.New("this Provider uses an owner-installed OCI image instead of Apple resource bundles")
	}
	request, err := r.Provisioning.Request(ctx, definition.ID, "")
	if err != nil {
		return err
	}
	if request.State == "ready" {
		return nil
	}
	if _, err = r.Provisioning.Import(request.ID, directory); err != nil {
		return err
	}
	return r.Provisioning.Wait(ctx, definition.ID, request.BundleID)
}
