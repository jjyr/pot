package selfupdate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// restartCommand is exchanged only over the private live update socket. Arguments
// and environment are never persisted in host registration or installation jobs.
type restartCommand struct {
	App         bool     `json:"app"`
	PID         int      `json:"pid,omitempty"`
	Executable  string   `json:"executable,omitempty"`
	Args        []string `json:"args,omitempty"`
	Environment []string `json:"environment,omitempty"`
	Directory   string   `json:"directory,omitempty"`
}

type restartHost struct {
	Participant
	command          restartCommand
	prepared, exited bool
	childDone        chan struct{}
}

func restartHosts(install string, peers []Participant) ([]*restartHost, error) {
	result := make([]*restartHost, 0, len(peers))
	for _, peer := range peers {
		host := &restartHost{Participant: peer}
		if err := localCall(context.Background(), peer.Socket, "GET", "/restart", &host.command); err != nil {
			return nil, err
		}
		if host.command.App && (host.command.PID <= 0 || len(host.command.Args) == 0 || host.command.Executable != filepath.Join(install, "Contents/MacOS/pot-app") || peer.Root == "") {
			return nil, errors.New("invalid native App restart command")
		}
		result = append(result, host)
	}
	return result, nil
}

func (h *restartHost) prepare(directory string) error {
	if h.command.App && h.exited {
		return nil
	}
	if h.command.App && h.childDone != nil {
		select {
		case <-h.childDone:
			h.exited = true
			h.prepared = true
			return nil // An owned failed launch is proven stopped by Wait, not a missing socket.
		default:
		}
		if err := h.discover(directory); err != nil {
			return err
		}
	}
	// A CLI may have stopped even when its callback reports uncertain cleanup;
	// its resident coordinator can still acknowledge an in-place recovery.
	if !h.command.App {
		h.prepared = true
	}
	if err := localCall(context.Background(), h.Socket, "POST", "/prepare", nil); err != nil {
		return err
	}
	h.prepared = true
	if !h.command.App {
		return nil
	}
	watch, err := watchProcess(h.command.PID)
	if err != nil {
		return err
	}
	defer watch.Close()
	if err = localCall(context.Background(), h.Socket, "POST", "/exit", nil); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = watch.Wait(ctx); err != nil {
		return err
	}
	h.exited = true
	return nil
}

func (h *restartHost) resume() error {
	if !h.prepared {
		return nil
	}
	if !h.command.App {
		if err := localCall(context.Background(), h.Socket, "POST", "/resume", nil); err != nil {
			return err
		}
		h.prepared = false
		return nil
	}
	if !h.exited {
		return errors.New("native App exit was not confirmed")
	}
	command := exec.Command(h.command.Executable, h.command.Args[1:]...)
	command.Args[0] = h.command.Args[0]
	command.Env = h.command.Environment
	command.Dir = h.command.Directory
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	command.Stdout, command.Stderr = os.Stderr, os.Stderr
	if err := command.Start(); err != nil {
		return err
	}
	h.childDone = make(chan struct{})
	h.command.PID = command.Process.Pid
	h.exited, h.prepared = false, false
	done := h.childDone
	go func() { _ = command.Wait(); close(done) }()
	return nil
}

func (h *restartHost) discover(directory string) error {
	peers, err := participants(directory)
	if err != nil {
		return err
	}
	for _, peer := range peers {
		if peer.Root == h.Root {
			var command restartCommand
			if err := localCall(context.Background(), peer.Socket, "GET", "/restart", &command); err != nil {
				return err
			}
			if !command.App || command.PID != h.command.PID {
				return errors.New("unexpected process owns native App root")
			}
			h.Participant = peer
			return nil
		}
	}
	return errors.New("native App has not registered its data root")
}
