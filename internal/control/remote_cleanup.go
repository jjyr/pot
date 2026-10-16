package control

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/runner"
	"sync"
	"time"
)

// During cancellation a command already sent receives a bounded chance to return
// its original cleanup receipt. No replacement command is issued.
func settlementContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	var mu sync.Mutex
	var timer *time.Timer
	stop := context.AfterFunc(parent, func() { mu.Lock(); timer = time.AfterFunc(5*time.Second, cancel); mu.Unlock() })
	return ctx, func() {
		stop()
		mu.Lock()
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
		cancel()
	}
}

type remoteRunnerPort struct{ s *Service }

func (p remoteRunnerPort) Launch(ctx context.Context, l runner.Launch) (runner.Run, error) {
	return p.s.Runner.Launch(ctx, l)
}
func (p remoteRunnerPort) Inspect(ctx context.Context, c, t, r string) (*runner.Run, error) {
	return p.s.Runner.Inspect(ctx, c, t, r)
}
func (p remoteRunnerPort) Resume(ctx context.Context, c, t, r string) error {
	return p.s.Runner.Resume(ctx, c, t, r)
}
func (p remoteRunnerPort) Stop(ctx context.Context, c, t, r, m string) error {
	err := p.s.Runner.Stop(ctx, c, t, r, m)
	state := p.s.remote()
	state.mu.Lock()
	prepared, ok := state.prepared[r]
	state.mu.Unlock()
	if ok {
		if engine, yes := prepared.engine.(interface{ Drain() error }); yes {
			err = errors.Join(err, engine.Drain())
		}
		if p.s.options.CleanupTarget != nil {
			err = errors.Join(err, p.s.options.CleanupTarget(ctx, t, true))
		}
	}
	return err
}
func (s *Service) closeRemoteRun(run string) {
	state := s.remote()
	state.mu.Lock()
	a := state.runs[run]
	state.mu.Unlock()
	if a == nil {
		return
	}
	a.mu.Lock()
	a.closed = true
	a.cancel()
	streams := []*modelStream{}
	for _, stream := range a.streams {
		streams = append(streams, stream)
	}
	a.mu.Unlock()
	for _, stream := range streams {
		if stream.body != nil {
			stream.body.Close()
		}
	}
}
func (s *Service) reconcileRemoteResources(ctx context.Context) {
	state := s.remote()
	state.mu.Lock()
	runs := map[string]*remoteAuthority{}
	for id, a := range state.runs {
		runs[id] = a
	}
	state.mu.Unlock()
	for id := range runs {
		var phase, stop string
		err := s.Tasks.DB().QueryRowContext(ctx, "SELECT phase,json_extract(body,'$.stop_mode') FROM tasks WHERE json_extract(body,'$.run_id')=?", id).Scan(&phase, &stop)
		if err != nil {
			continue
		}
		if stop != "" || (phase != "queued" && phase != "running" && phase != "waiting") {
			s.closeRemoteRun(id)
		}
	}
}

var _ = core.ErrCleanupUnconfirmed
