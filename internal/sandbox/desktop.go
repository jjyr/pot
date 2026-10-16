package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/jjyr/pot/internal/core"
)

type DesktopCapabilities struct {
	Viewing   bool   `json:"viewing"`
	Input     bool   `json:"input"`
	Clipboard bool   `json:"clipboard"`
	Browser   bool   `json:"browser"`
	Protocol  string `json:"protocol"`
}
type DesktopProvider interface {
	DesktopCapabilities() DesktopCapabilities
	PrepareDesktop(context.Context, Instance) error
	OpenDesktop(context.Context, Instance) (io.ReadWriteCloser, error)
	DesktopMetrics(context.Context, Instance) (json.RawMessage, error)
	DesktopBrowser(context.Context, Instance) error
}
type DesktopBinding struct {
	SandboxID    string              `json:"sandbox_id"`
	ManagerEpoch uint64              `json:"-"`
	Generation   uint64              `json:"-"`
	Capabilities DesktopCapabilities `json:"capabilities"`
}

func (m *Manager) PrepareDesktop(ctx context.Context, id string) (DesktopBinding, error) {
	ctx, done, err := m.beginCall(ctx, "desktop:"+id)
	if err != nil {
		return DesktopBinding{}, err
	}
	defer done()
	lease, err := m.Acquire(ctx, id, "desktop:"+id)
	if err != nil {
		return DesktopBinding{}, err
	}
	defer lease.Release(context.WithoutCancel(ctx))
	definition, provider, instance, state, err := m.ready(ctx, id)
	if err != nil {
		return DesktopBinding{}, err
	}
	desktop, ok := provider.(DesktopProvider)
	if !ok || !desktop.DesktopCapabilities().Viewing {
		return DesktopBinding{}, errors.New("desktop capability unavailable")
	}
	if err := desktop.PrepareDesktop(ctx, instance); err != nil {
		return DesktopBinding{}, err
	}
	return DesktopBinding{SandboxID: id, ManagerEpoch: definition.ManagerEpoch, Generation: state.Generation, Capabilities: desktop.DesktopCapabilities()}, nil
}
func (m *Manager) desktop(ctx context.Context, binding DesktopBinding) (DesktopProvider, Instance, error) {
	definition, provider, instance, err := m.provider(ctx, binding.SandboxID)
	if err != nil {
		return nil, instance, err
	}
	if definition.Quarantined {
		return nil, instance, core.ErrCleanupUnconfirmed
	}
	if definition.ManagerEpoch != binding.ManagerEpoch {
		return nil, instance, errors.New("desktop Runtime epoch changed")
	}
	state, err := provider.Lifecycle(ctx, instance, "status")
	if err != nil {
		return nil, instance, err
	}
	if state.Phase != "running" || state.Generation != binding.Generation {
		return nil, instance, errors.New("desktop VM generation changed")
	}
	desktop, ok := provider.(DesktopProvider)
	if !ok {
		return nil, instance, errors.New("desktop capability unavailable")
	}
	return desktop, instance, nil
}
func (m *Manager) ValidateDesktop(ctx context.Context, binding DesktopBinding) error {
	_, _, err := m.desktop(ctx, binding)
	return err
}
func (m *Manager) DesktopMetrics(ctx context.Context, binding DesktopBinding) (json.RawMessage, error) {
	p, i, err := m.desktop(ctx, binding)
	if err != nil {
		return nil, err
	}
	return p.DesktopMetrics(ctx, i)
}
func (m *Manager) DesktopBrowser(ctx context.Context, binding DesktopBinding) error {
	p, i, err := m.desktop(ctx, binding)
	if err != nil {
		return err
	}
	if !binding.Capabilities.Browser {
		return errors.New("desktop browser unavailable")
	}
	return p.DesktopBrowser(ctx, i)
}
func (m *Manager) OpenDesktop(ctx context.Context, binding DesktopBinding) (io.ReadWriteCloser, error) {
	ctx, done, err := m.beginCall(ctx, "desktop:"+binding.SandboxID)
	if err != nil {
		return nil, err
	}
	lease, err := m.Acquire(ctx, binding.SandboxID, "desktop:"+binding.SandboxID)
	if err != nil {
		done()
		return nil, err
	}
	p, i, err := m.desktop(ctx, binding)
	if err != nil {
		lease.Release(context.WithoutCancel(ctx))
		done()
		return nil, err
	}
	stream, err := p.OpenDesktop(ctx, i)
	releaseErr := lease.Release(context.WithoutCancel(ctx))
	if err != nil || releaseErr != nil {
		if stream != nil {
			stream.Close()
		}
		done()
		return nil, errors.Join(err, releaseErr)
	}
	value := &desktopStream{ReadWriteCloser: stream, done: done}
	go func() { <-ctx.Done(); value.Close() }()
	return value, nil
}

type desktopStream struct {
	io.ReadWriteCloser
	once sync.Once
	done func()
	err  error
}

func (s *desktopStream) Close() error {
	s.once.Do(func() { s.err = s.ReadWriteCloser.Close(); s.done() })
	return s.err
}
