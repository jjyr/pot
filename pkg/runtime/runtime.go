// Package runtime embeds a complete Pot host in a Go application. For an agent
// loop without the host's databases and product services, use pkg/agent instead.
//
// Open requires a private data root and an explicit resource directory. It does
// not discover resources relative to the embedding executable or its checkout.
// The caller owns HTTP listening and must stop serving requests before Close.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/productinfo"
	impl "github.com/jjyr/pot/internal/runtime"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/resource"
	"github.com/jjyr/pot/pkg/resourcecontrol"
	"github.com/jjyr/pot/pkg/sandbox"
)

// KeySource supplies the encryption key for this host's provider credentials.
// Key must return a caller-owned 32-byte copy, which the host clears after use.
// When allowCreate is false, a missing key must cause an error rather than
// creating a replacement key. Implementations must retain keys across restarts
// when persisted credentials are used. A nil KeySource uses the system key store.
type KeySource interface {
	Key(ctx context.Context, allowCreate bool) ([]byte, error)
}

// Options configures a complete host. Asset directories and executable paths
// belong to the embedding application; Open does not install them.
type Options struct {
	// Root is an explicit private data directory, created with mode 0700 when
	// absent. Existing roots must have the host's recognized identity.
	Root string
	// ResourceDirectory contains packaged skills, plugins and optional Node.
	// It is required even when the embedding application supplies no assets.
	ResourceDirectory string
	// HelperDirectory contains platform helpers. An empty value uses
	// ResourceDirectory; no checkout-relative default is used.
	HelperDirectory string
	// SandboxProviders nil selects platform defaults. An empty, nonnil slice
	// disables providers. Providers are not started by Open.
	SandboxProviders []sandbox.Provider
	// Limits nil uses persisted limits or execution.DefaultLimits.
	Limits *execution.Limits
	// Workspace and AgentHome default to directories beneath Root. BuiltinSkills
	// and PluginRoot default to directories beneath ResourceDirectory.
	Workspace, AgentHome, BuiltinSkills, PluginRoot string
	// Node is the Node executable used by plugins. If empty, the host checks
	// ResourceDirectory/node/bin/node, then uses its existing environment lookup.
	Node string
	Keys KeySource
	// Composer executes composed tool calls. A process-based composer requires
	// an explicit worker command. If nil, composition fails until configured;
	// the host never executes the embedding application's executable as a worker.
	Composer agent.Composer
	// Embedded fixes this host's control role and disables joining another host.
	Embedded bool
	// DisableMetadataRefresh prevents background provider metadata refresh.
	DisableMetadataRefresh bool
	// ControlHTTPClient customizes calls to other hosts.
	ControlHTTPClient *http.Client
	// Name is the host name returned by the operator API; it defaults to Pot.
	Name string
}

// Info is an immutable copy of the host's location and running identity.
type Info struct {
	Root, RuntimeID, BootID, AssetDirectory string
}

// Runtime owns a host and its managers. Close releases the exclusive root lock.
// Managers returned by its accessors are borrowed: callers must not close them
// separately and must stop using them before closing the Runtime.
type Runtime struct {
	inner *impl.Runtime
	app   *transport.Application
}

// Open opens the host under ctx. It never starts an HTTP listener. Canceling ctx
// cancels background work; callers must also call Close to release resources.
func Open(ctx context.Context, options Options) (*Runtime, error) {
	if ctx == nil {
		return nil, errors.New("runtime context is required")
	}
	if options.Root == "" || options.ResourceDirectory == "" {
		return nil, errors.New("runtime requires explicit root and resource directories")
	}
	resources, err := filepath.Abs(options.ResourceDirectory)
	if err != nil {
		return nil, err
	}
	helpers := options.HelperDirectory
	if helpers == "" {
		helpers = resources
	}
	helpers, err = filepath.Abs(helpers)
	if err != nil {
		return nil, err
	}
	composer := options.Composer
	if composer == nil {
		composer = composition.ProcessRunner{}
	}
	inner, err := impl.Open(ctx, impl.Options{
		Root: options.Root, ResourceDirectory: resources, HelperDirectory: helpers,
		SandboxProviders: options.SandboxProviders, Limits: options.Limits,
		DisableMetadataRefresh: options.DisableMetadataRefresh,
		Control: control.Options{
			Workspace: options.Workspace, AgentHome: options.AgentHome,
			BuiltinSkills: options.BuiltinSkills, PluginRoot: options.PluginRoot,
			Node: options.Node, Keys: options.Keys, Embedded: options.Embedded,
			Composer:          composer,
			ControlHTTPClient: options.ControlHTTPClient,
		},
	})
	if err != nil {
		return nil, err
	}
	name := options.Name
	if name == "" {
		name = "Pot"
	}
	app := &transport.Application{
		RuntimeResources: inner.ResourceCall, Resources: inner.Provisioning,
		Control: inner.Control, RuntimeID: inner.Root.Identity.RuntimeID,
		BootID: inner.BootID, Name: name,
		About: func(context.Context) (any, error) { return productinfo.Read(resources) },
	}
	return &Runtime{inner: inner, app: app}, nil
}

// Info returns a copy of the host identity without transferring root ownership.
func (r *Runtime) Info() Info {
	return Info{Root: r.inner.Root.Path, RuntimeID: r.inner.Root.Identity.RuntimeID,
		BootID: r.inner.BootID, AssetDirectory: r.inner.AssetDirectory}
}

// Executions returns the shared execution capacity and lifecycle manager.
func (r *Runtime) Executions() *execution.Manager { return r.inner.Executions }

// Sandboxes returns the host's sandbox manager.
func (r *Runtime) Sandboxes() *sandbox.Manager { return r.inner.Sandboxes }

// Resources returns the host's verified resource cache.
func (r *Runtime) Resources() *resource.Store { return r.inner.Resources }

// Provisioning returns the sandbox resource preparation manager.
func (r *Runtime) Provisioning() *resourcecontrol.Manager { return r.inner.Provisioning }

// Handler returns the authenticated operator API, including event streams and
// runtime resource commands. Every request requires Authorization: Bearer token.
// An empty token rejects every request. This API gives the token holder control
// over the host; the caller owns token distribution and transport security.
// It does not publish a browser UI or a desktop capture gateway.
func (r *Runtime) Handler(token string) http.Handler { return transport.Handler(r.app, token) }

// ResourceSnapshot returns the operator resource snapshot as JSON, avoiding
// dependencies on the host's private control and assistant representations.
func (r *Runtime) ResourceSnapshot(ctx context.Context) (json.RawMessage, error) {
	value, err := r.inner.ResourceSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// ImportSandboxResources imports an offline resource bundle for the shared
// Podman sandbox and waits until preparation completes. This is an explicit
// operation and may start the provider; Open does not call it automatically.
func (r *Runtime) ImportSandboxResources(ctx context.Context, directory string) error {
	return r.inner.ImportSandboxResources(ctx, directory)
}

// Close cancels background work and closes the host's services, providers and
// exclusive data-root lock. It is safe to call more than once.
func (r *Runtime) Close() error { return r.inner.Close() }
