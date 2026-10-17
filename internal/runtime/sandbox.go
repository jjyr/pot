package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/resource"
	"github.com/jjyr/pot/pkg/resourcecontrol"
	"github.com/jjyr/pot/pkg/sandbox"
	"github.com/jjyr/pot/pkg/sandboxprovider"
	"github.com/jjyr/pot/pkg/sandboxresources"
)

func (r *Runtime) openSandboxes(ctx context.Context, options Options, config *control.Options) error {
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
	executionRoot := filepath.Join(r.Root.Path, "execution")
	if err := os.MkdirAll(executionRoot, 0700); err != nil {
		return err
	}
	providers := options.SandboxProviders
	catalog := sandboxresources.Empty()
	defaultProvider := options.DefaultSandboxProvider
	if providers == nil {
		packages, err := plugin.Discover(config.PluginRoot)
		if err != nil {
			return err
		}
		registry, err := sandboxprovider.Load(ctx, packages, sandboxprovider.Config{Root: executionRoot, UserSkills: filepath.Join(config.AgentHome, "skills"), BuiltinSkills: config.BuiltinSkills})
		if err != nil {
			return err
		}
		providers = registry.Providers
		catalog = registry.Catalog
		if defaultProvider == "" {
			defaultProvider = registry.DefaultProvider
		}
	}
	if options.SandboxCatalog != nil {
		catalog = *options.SandboxCatalog
	}
	var err error
	r.Sandboxes, err = sandbox.Open(ctx, sandbox.Config{Root: executionRoot, RuntimeID: r.Root.Identity.RuntimeID, Providers: providers, DefaultProvider: defaultProvider})
	if err != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, p := range providers {
			err = errors.Join(err, p.Shutdown(shutdown))
		}
		return err
	}
	r.Resources, err = resource.New(filepath.Join(r.Root.Path, "resource-cache"), resource.Options{Context: ctx})
	if err != nil {
		return err
	}
	providerFor := func(ctx context.Context, id string) (string, error) {
		if id == sandbox.SharedID {
			definition, err := r.Sandboxes.EnsureShared(ctx)
			return definition.Provider, err
		}
		definitions, err := r.Sandboxes.Inventory(ctx)
		if err != nil {
			return "", err
		}
		for _, d := range definitions {
			if d.ID == id && !d.Retired {
				return d.Provider, nil
			}
		}
		return "", errors.New("Sandbox definition not found")
	}
	r.Provisioning, err = resourcecontrol.Open(resourcecontrol.Options{Root: r.Root.Path, Catalog: catalog, Store: r.Resources, SandboxProvider: providerFor, Bind: r.Sandboxes.BindResources, Notify: func() {
		if r.Control != nil {
			r.Control.Changes.Notify("sandbox_resources")
		}
	}})
	if err != nil {
		return err
	}
	r.providerCatalog = catalog
	config.Sandboxes = r.Sandboxes
	config.PrepareSandboxResources = func(ctx context.Context, id string) error {
		provider, err := providerFor(ctx, id)
		if err != nil {
			return err
		}
		if !catalog.RequiresResources(provider) {
			return nil
		}
		return r.Provisioning.Wait(ctx, id, "")
	}
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
			provider, err := providerFor(ctx, op.Target.Target.SandboxID)
			if err != nil {
				return nil, err
			}
			if catalog.RequiresResources(provider) {
				if err = r.Provisioning.WaitFor(ctx, op.Target.Target.SandboxID, "", op.Owner, op.Turn); err != nil {
					return nil, err
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
			if err = r.Sandboxes.RememberPrepared(ctx, grant, request.ID, request.Operation, args, prepared); err != nil {
				return nil, err
			}
			return r.Sandboxes.ExecuteWithEnvironment(ctx, grant, request.ID, request.Operation, prepared, request.Environment, emit)
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
func (r *Runtime) ImportSandboxResources(ctx context.Context, sandboxID, bundleID, directory string) error {
	var definition sandbox.Definition
	var err error
	if sandboxID == sandbox.SharedID {
		definition, err = r.Sandboxes.EnsureShared(ctx)
	} else {
		var definitions []sandbox.Definition
		definitions, err = r.Sandboxes.Inventory(ctx)
		for _, d := range definitions {
			if d.ID == sandboxID && !d.Retired {
				definition = d
				break
			}
		}
		if err == nil && definition.ID == "" {
			err = errors.New("Sandbox definition not found")
		}
	}
	if err != nil {
		return err
	}
	if !r.providerCatalog.RequiresResources(definition.Provider) {
		return errors.New("this Sandbox Provider declares no resource bundles")
	}
	request, err := r.Provisioning.Request(ctx, definition.ID, bundleID)
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
