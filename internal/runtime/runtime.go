// Package runtime composes components and owns their lifecycle. Databases,
// provider policy and HTTP adapters remain in their owning packages.
package runtime

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/selfupdate"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/storage"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Options struct {
	Root              string
	ResourceDirectory string

	Control                control.Options
	Limits                 *execution.Limits
	DisableMetadataRefresh bool
}
type Runtime struct {
	Root           *instance.Root
	AssetDirectory string
	BootID         string
	Executions     *execution.Manager
	Control        *control.Service
	Updates        *selfupdate.Manager
	Maintaining    func() bool

	cancel     context.CancelFunc
	once       sync.Once
	closeErr   error
	background sync.WaitGroup
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
	if err := storage.MigrateRoot(ctx, root.Path); err != nil {
		root.Close()
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
	host := selfupdate.FromContext(ctx)
	if host != nil {
		r.Maintaining = host.Maintaining
		r.Executions.SetMaintenance(host.Maintaining)
	}
	config := options.Control
	if r.Maintaining != nil {
		config.Maintenance = r.Maintaining
	}
	if host != nil {
		host.Protect(r.Root.Path, config.Workspace, config.AgentHome)
	}
	if config.Composer == nil {
		// Product executables explicitly host this hidden worker entry point.
		executable, err := os.Executable()
		if err != nil {
			return nil, err
		}
		config.Composer = composition.ProcessRunner{Command: []string{executable, composition.WorkerArgument}}
	}
	config.Context = runctx
	config.Root = root.Path
	config.RuntimeID = root.Identity.RuntimeID
	config.BootID = r.BootID
	config.Manager = r.Executions
	if err = r.prepareAssets(options, &config); err != nil {
		return nil, err
	}
	r.Control, err = control.Open(config)
	if err != nil {
		return nil, err
	}
	if !options.DisableMetadataRefresh {
		r.background.Go(func() { r.Control.Profiles.RunMetadata(runctx) })
	}
	r.Updates = selfupdate.New(runctx, host, func() { r.Control.Changes.Notify("updates") })
	if host != nil {
		if err = host.Bind(r.Root.Path, r.updateWork); err != nil {
			return nil, err
		}
	}
	r.background.Go(r.Updates.Run)
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
		if r.Control != nil && r.Maintaining != nil && r.Maintaining() {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			r.closeErr = errors.Join(r.closeErr, r.Executions.Drain(cleanup), r.interruptUpdateTasks(cleanup))
			cancel()
		}
		if r.Control != nil {
			r.closeErr = errors.Join(r.closeErr, r.Control.Close())
		}

		if r.Executions != nil {
			r.closeErr = errors.Join(r.closeErr, r.Executions.Drain(context.Background()))
		}

		if r.Root != nil {
			r.closeErr = errors.Join(r.closeErr, r.Root.Close())
		}
	})
	return r.closeErr
}
