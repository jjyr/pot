// Package sandboxprovider implements trusted platform lifecycle adapters. It
// depends on sandbox's contracts; the Manager never imports these providers.
package sandboxprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/sandbox"
)

type helperChild struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
}
type helperProcesses struct {
	mu       sync.Mutex
	children map[string]*helperChild
}

func (p *helperProcesses) owned(root string) (*helperChild, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	child := p.children[root]
	if child == nil {
		return nil, nil
	}
	select {
	case <-child.done:
		return nil, errors.Join(core.ErrCleanupUnconfirmed, errors.New("owned Swift Helper exited; reconcile before restarting"))
	default:
		return child, nil
	}
}
func (p *helperProcesses) launch(ctx context.Context, root, socket, binary string, args ...string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if child := p.children[root]; child != nil {
		select {
		case <-child.done:
			return errors.Join(core.ErrCleanupUnconfirmed, errors.New("owned Swift Helper exited; explicit recovery required"))
		default:
			return nil
		}
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		return errors.Join(core.ErrCleanupUnconfirmed, errors.New("unowned Swift Helper socket; prove old Helper exit before recovery"))
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return errors.New("Swift Helper is not installed")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(root, "helper.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	command := exec.Command(binary, args...)
	command.Stdout = log
	command.Stderr = log
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return err
	}
	child := &helperChild{command: command, done: make(chan struct{})}
	if p.children == nil {
		p.children = map[string]*helperChild{}
	}
	p.children[root] = child
	go func() { child.err = command.Wait(); close(child.done) }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Lstat(socket); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(core.ErrCleanupUnconfirmed, ctx.Err())
		case <-child.done:
			return errors.Join(core.ErrCleanupUnconfirmed, errors.New("Swift Helper exited during startup"))
		case <-timer.C:
			return errors.Join(core.ErrCleanupUnconfirmed, errors.New("Swift Helper did not publish its private socket"))
		case <-ticker.C:
		}
	}
}
func (p *helperProcesses) shutdown(ctx context.Context) error {
	p.mu.Lock()
	children := make(map[string]*helperChild, len(p.children))
	for root, child := range p.children {
		children[root] = child
	}
	p.mu.Unlock()
	var result error
	for root, child := range children {
		select {
		case <-child.done:
			if child.err != nil {
				result = errors.Join(result, core.ErrCleanupUnconfirmed, errors.New("Swift Helper exited unsuccessfully"))
			}
			continue
		default:
		}
		if err := child.command.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			result = errors.Join(result, core.ErrCleanupUnconfirmed, err)
			continue
		}
		select {
		case <-child.done:
			if child.err != nil {
				result = errors.Join(result, core.ErrCleanupUnconfirmed, errors.New("Swift Helper shutdown exited unsuccessfully"))
			}
		case <-ctx.Done():
			result = errors.Join(result, core.ErrCleanupUnconfirmed, ctx.Err())
			continue
		}
		p.mu.Lock()
		delete(p.children, root)
		p.mu.Unlock()
	}
	return result
}
func nativeStatus(value map[string]any) (sandbox.Status, error) {
	phase, ok := value["phase"].(string)
	if !ok {
		return sandbox.Status{}, errors.New("Helper omitted lifecycle phase")
	}
	var generation uint64
	var err error
	switch number := value["generation"].(type) {
	case json.Number:
		generation, err = strconv.ParseUint(string(number), 10, 64)
	case uint64:
		generation = number
	default:
		err = errors.New("Helper omitted exact generation")
	}
	if err != nil {
		return sandbox.Status{}, err
	}
	return sandbox.Status{Phase: phase, Generation: generation}, nil
}
func privateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return privateBytes(path, data)
}
func privateBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pot-config-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func loadJSON(path string, out any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return err
	}
	if len(data) > 65536 {
		return errors.New("private manifest exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing private manifest data")
	}
	return nil
}

func privateFile(path string, limit int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || owner.Uid != uint32(os.Getuid()) || info.Size() > limit {
		return nil, errors.New("guest credential must be a bounded private owner file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("guest credential too large")
	}
	return data, nil
}
func validatePaths(resources sandbox.PreparedResources) error {
	if resources.BundleID == "" || resources.ImageReference == "" || resources.ManifestDigest == "" || resources.Architecture == "" {
		return errors.New("missing prepared image identity")
	}
	if !filepath.IsAbs(resources.Image) {
		return errors.New("prepared image requires an absolute verified path")
	}
	canonical, err := filepath.EvalSymlinks(resources.Image)
	if err != nil || canonical != resources.Image {
		return errors.New("prepared image must not use symlinks")
	}
	info, err := os.Stat(resources.Image)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("prepared OCI image must be a directory")
	}
	return nil
}

func immutableResources(instance sandbox.Instance, resources sandbox.PreparedResources) error {
	if err := validatePaths(resources); err != nil {
		return err
	}
	path := filepath.Join(instance.Directory(), "prepared-resources.json")
	var old sandbox.PreparedResources
	if err := loadJSON(path, &old); err == nil {
		if old != resources {
			return errors.New("Sandbox resource bundle is pinned")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return privateJSON(path, resources)
}
