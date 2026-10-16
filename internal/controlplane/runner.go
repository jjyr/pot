package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/core"
)

// RemotePort is bound to one original Runtime. Parent routers must retain this
// target when recovering tasks; lookup failures never select a local fallback.
type RemotePort struct {
	Directory *Directory
	RuntimeID string
}

func (d *Directory) ControlPeer() (*Peer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Role != "runner" || d.state.Membership == nil {
		return nil, ErrUnauthorized
	}
	p := d.peers[d.state.Membership.Enrollment.Control.RuntimeID]
	if p == nil {
		return nil, ErrDisconnected
	}
	return p, nil
}

func (d *Directory) Peer(runtimeID string) (*Peer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m, ok := d.state.Members[runtimeID]
	p := d.peers[runtimeID]
	if d.state.Role != "control" || !ok || !m.Machine.Enabled || p == nil {
		return nil, ErrDisconnected
	}
	return p, nil
}
func (p RemotePort) call(ctx context.Context, method, id string, params any, out any) error {
	if p.Directory == nil {
		return fmt.Errorf("Directory required")
	}
	peer, err := p.Directory.Peer(p.RuntimeID)
	if err != nil {
		return err
	}
	raw, err := core.CanonicalJSON(params)
	if err != nil {
		return err
	}
	reply, err := peer.Call(ctx, Request{id, method, raw})
	if err != nil {
		return err
	}
	if out != nil {
		return core.DecodeJSON(reply, out)
	}
	return nil
}
func (p RemotePort) Launch(ctx context.Context, l runner.Launch) (runner.Run, error) {
	var run runner.Run
	if err := l.Validate(p.RuntimeID); err != nil {
		return run, err
	}
	if p.Directory == nil || l.ControlID != p.Directory.Identity().RuntimeID {
		return run, ErrUnauthorized
	}
	err := p.call(ctx, "runner.launch", "launch:"+l.RunID, l, &run)
	if err == nil {
		expected, e := l.Digest()
		actual, e2 := run.Launch.Digest()
		if e != nil || e2 != nil || expected != actual || run.LaunchDigest != expected {
			return runner.Run{}, fmt.Errorf("remote launch receipt differs from original")
		}
	}
	return run, err
}

type runRef struct {
	ControlID string `json:"control_id" required:"true"`
	TaskID    string `json:"task_id" required:"true"`
	RunID     string `json:"run_id" required:"true"`
	Mode      string `json:"mode" required:"true"`
}

func (p RemotePort) Inspect(ctx context.Context, control, task, run string) (*runner.Run, error) {
	var result *runner.Run
	err := p.call(ctx, "runner.inspect", "inspect:"+secret(), runRef{control, task, run, ""}, &result)
	if err == nil && result != nil {
		if result.Launch.Validate(p.RuntimeID) != nil || result.Launch.ControlID != control || result.Launch.TaskID != task || result.Launch.RunID != run {
			return nil, fmt.Errorf("remote inspection ownership differs")
		}
		digest, e := result.Launch.Digest()
		if e != nil || digest != result.LaunchDigest {
			return nil, fmt.Errorf("remote inspection manifest differs")
		}
	}
	return result, err
}
func (p RemotePort) Stop(ctx context.Context, control, task, run, mode string) error {
	return p.call(ctx, "runner.stop", "stop:"+run+":"+mode, runRef{control, task, run, mode}, nil)
}
func (p RemotePort) Resume(ctx context.Context, control, task, run string) error {
	return p.call(ctx, "runner.resume", "resume:"+run, runRef{control, task, run, ""}, nil)
}

// RunnerHandler enforces authenticated Control/Runtime ownership before invoking
// the durable Runner port. The optional next handler owns model/approval/resource
// callbacks and must enforce its own run-scoped authorization.
func RunnerHandler(d *Directory, port runner.Port, next Handler) Handler {
	return func(ctx context.Context, principal Principal, request Request) (json.RawMessage, error) {
		membership := d.Membership()
		if membership == nil || principal.Owner || principal.RuntimeID != membership.Enrollment.Control.RuntimeID || principal.Epoch != membership.Enrollment.Epoch {
			return nil, ErrUnauthorized
		}
		if request.Method == "runner.launch" {
			var launch runner.Launch
			if err := core.DecodeJSON(request.Params, &launch); err != nil {
				return nil, err
			}
			if launch.ControlID != principal.RuntimeID {
				return nil, ErrUnauthorized
			}
			if err := launch.Validate(d.Identity().RuntimeID); err != nil {
				return nil, err
			}
			run, err := port.Launch(ctx, launch)
			if err != nil {
				return nil, err
			}
			return json.Marshal(run)
		}
		switch request.Method {
		case "runner.inspect", "runner.stop", "runner.resume":
			var ref runRef
			if err := core.DecodeJSON(request.Params, &ref); err != nil {
				return nil, err
			}
			if ref.ControlID != principal.RuntimeID || !core.Identifier(ref.TaskID) || !core.Identifier(ref.RunID) {
				return nil, ErrUnauthorized
			}
			switch request.Method {
			case "runner.inspect":
				result, err := port.Inspect(ctx, ref.ControlID, ref.TaskID, ref.RunID)
				if err != nil {
					return nil, err
				}
				return json.Marshal(result)
			case "runner.stop":
				if ref.Mode != "interrupt" && ref.Mode != "cancel" {
					return nil, fmt.Errorf("invalid stop mode")
				}
				return json.RawMessage("null"), port.Stop(ctx, ref.ControlID, ref.TaskID, ref.RunID, ref.Mode)
			case "runner.resume":
				resume, ok := port.(interface {
					Resume(context.Context, string, string, string) error
				})
				if !ok {
					return nil, fmt.Errorf("Runner resume unavailable")
				}
				return json.RawMessage("null"), resume.Resume(ctx, ref.ControlID, ref.TaskID, ref.RunID)
			}
		}
		if next != nil {
			return next(ctx, principal, request)
		}
		return nil, fmt.Errorf("unknown Runner method")
	}
}
