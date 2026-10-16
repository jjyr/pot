// Package runtime composes components and owns their lifecycle. Databases,
// provider policy and HTTP adapters remain in their owning packages.
package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/execution"
	"github.com/jjyr/pot/internal/instance"
	"github.com/jjyr/pot/internal/resource"
	"github.com/jjyr/pot/internal/resourcecontrol"
	"github.com/jjyr/pot/internal/sandbox"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Options struct {
	Root                               string
	ResourceDirectory, HelperDirectory string
	SandboxProviders                   []sandbox.Provider
	Control                            control.Options
	Limits                             *execution.Limits
	DisableMetadataRefresh             bool
}
type Runtime struct {
	Root           *instance.Root
	AssetDirectory string
	BootID         string
	Executions     *execution.Manager
	Control        *control.Service
	Sandboxes      *sandbox.Manager
	Resources      *resource.Store
	Provisioning   *resourcecontrol.Manager
	cancel         context.CancelFunc
	once           sync.Once
	closeErr       error
	background     sync.WaitGroup
}

func Open(ctx context.Context, options Options) (*Runtime, error) {
	path := options.Root
	var err error
	if path == "" {
		path, err = instance.DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	root, err := instance.Open(path)
	if err != nil {
		return nil, err
	}
	runctx, cancel := context.WithCancel(ctx)
	r := &Runtime{Root: root, BootID: rand.Text(), cancel: cancel}
	success := false
	defer func() {
		if !success {
			r.Close()
		}
	}()
	limits := execution.DefaultLimits()
	if options.Limits != nil {
		limits = *options.Limits
	} else if body, err := os.ReadFile(filepath.Join(root.Path, "runtime-execution.json")); err == nil {
		if err = core.DecodeJSON(body, &limits); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	r.Executions, err = execution.New(root.Identity.RuntimeID, r.BootID, limits)
	if err != nil {
		return nil, err
	}
	config := options.Control
	config.Context = runctx
	config.Root = root.Path
	config.RuntimeID = root.Identity.RuntimeID
	config.BootID = r.BootID
	config.Manager = r.Executions
	if err = r.openSandboxes(runctx, options, &config); err != nil {
		return nil, err
	}
	r.Control, err = control.Open(config)
	if err != nil {
		return nil, err
	}
	if !options.DisableMetadataRefresh {
		r.background.Go(func() { r.Control.Profiles.RunMetadata(runctx) })
	}
	success = true
	return r, nil
}
func (r *Runtime) Close() error {
	r.once.Do(func() {
		if r.Executions != nil {
			r.Executions.Stop()
		}
		r.cancel()
		r.background.Wait()
		if r.Control != nil {
			r.closeErr = errors.Join(r.closeErr, r.Control.Close())
		}
		if r.Provisioning != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			r.closeErr = errors.Join(r.closeErr, r.Provisioning.Close(ctx))
			cancel()
		}
		if r.Executions != nil {
			r.closeErr = errors.Join(r.closeErr, r.Executions.Drain(context.Background()))
		}
		if r.Sandboxes != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			r.closeErr = errors.Join(r.closeErr, r.Sandboxes.Shutdown(ctx), r.Sandboxes.Close())
			cancel()
		}
		if r.Resources != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			r.closeErr = errors.Join(r.closeErr, r.Resources.Close(ctx))
			cancel()
		}
		if r.Root != nil {
			r.closeErr = errors.Join(r.closeErr, r.Root.Close())
		}
	})
	return r.closeErr
}
