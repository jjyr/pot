package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/protocol"
)

type State struct {
	Version   string `json:"version"`
	Phase     string `json:"phase"`
	RequestID string `json:"request_id,omitempty"`
	Error     string `json:"error,omitempty"`
}
type Status struct {
	Current string `json:"current"`
	State
	Work        Work   `json:"work"`
	Supported   bool   `json:"supported"`
	CanRetry    bool   `json:"can_retry"`
	AutoInstall bool   `json:"auto_install"`
	ReleaseURL  string `json:"release_url"`
	Reason      string `json:"reason,omitempty"`
	Discovery   string `json:"discovery"`
}
type Command struct {
	Action    string `json:"action"`
	Version   string `json:"version"`
	RequestID string `json:"request_id"`
	Interrupt bool   `json:"interrupt"`
}
type Manager struct {
	Host        *Host
	Notify      func()
	mu          sync.Mutex
	release     *Release
	busy        bool
	ctx         context.Context
	client      *http.Client
	discoveryMu sync.RWMutex
	detected    string
	reason      string
	discovery   string
	envelope    []byte
	checkMu     sync.Mutex
	checking    chan struct{}
}

func New(ctx context.Context, h *Host, notify func()) *Manager {
	return &Manager{Host: h, Notify: notify, ctx: ctx, client: client()}
}
func (m *Manager) state() State {
	var s State
	if m.Host == nil {
		return s
	}
	data, err := os.ReadFile(filepath.Join(m.Host.Directory, "state.json"))
	if err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}
func (m *Manager) save(s State) error {
	err := instance.WriteJSON(filepath.Join(m.Host.Directory, "state.json"), s)
	if err == nil && m.Notify != nil {
		m.Notify()
	}
	return err
}
func (m *Manager) Status() (Status, error) {
	s := Status{Current: buildinfo.Version, Supported: m.Host != nil, Discovery: "checking"}
	m.discoveryMu.RLock()
	s.Version, s.Reason, s.Discovery = m.detected, m.reason, m.discovery
	if s.Discovery == "" {
		s.Discovery = "checking"
	}
	m.discoveryMu.RUnlock()
	if m.Host == nil {
		s.Reason = "installation"
		if buildinfo.UpdatePublicKey == "" {
			s.Reason = "no_key"
		}
		s.ReleaseURL = releaseURL(s.Version)
		return s, nil
	}
	saved := m.state()
	if saved.Version != "" && (newer(saved.Version, buildinfo.Version) || saved.Phase == "installing" || saved.Phase == "restarting" || saved.Phase == "blocked") {
		s.State = saved
	}
	if s.Version == "" && saved.Version != "" && !newer(saved.Version, buildinfo.Version) {
		s.State = State{Phase: "current"}
	}
	s.ReleaseURL = releaseURL(s.Version)
	s.AutoInstall = s.Discovery == "verified" && s.Reason == "" && s.Version != "" && newer(s.Version, buildinfo.Version)
	peers, err := participants(m.Host.Directory)
	if err != nil {
		return s, err
	}
	for _, p := range peers {
		if p.Protocol != UpdateProtocol {
			s.AutoInstall = false
			s.Reason = "protocol"
		}
		if p.Unsupported {
			s.Supported = false
			s.AutoInstall = false
			s.Reason = "protected_state"
		}
		s.Work.Tasks += p.Work.Tasks
		s.Work.Replies += p.Work.Replies
		s.Work.Sessions += p.Work.Sessions
	}
	if s.Phase == "blocked" {
		s.AutoInstall = false
		s.Reason = "recovery"
		s.CanRetry = s.Version == buildinfo.Version
	}
	if s.AutoInstall && m.Host.Install != "" {
		if unix.Access(filepath.Dir(m.Host.Install), unix.W_OK) != nil || unix.Access(m.Host.Install, unix.W_OK) != nil {
			s.AutoInstall = false
			s.Reason = "permissions"
		}
	}
	return s, nil
}
func (m *Manager) Run() {
	go m.check()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	next := time.Now().Add(4 * time.Hour)
	previous := m.state()
	phaseSince := time.Now()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			s := m.state()
			if s != previous {
				previous = s
				phaseSince = time.Now()
				if m.Notify != nil {
					m.Notify()
				}
			}
			if m.Host != nil && (s.Phase == "installing" || s.Phase == "restarting") && time.Since(phaseSince) > 15*time.Second {
				m.checkWorker(s)
			}
			if time.Now().After(next) {
				next = time.Now().Add(4 * time.Hour)
				go m.check()
			}
		}
	}
}
func (m *Manager) checkWorker(s State) {
	lock, err := openLock(filepath.Join(m.Host.Directory, "update.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return
	}
	defer lock.Close()
	if m.state() != s {
		return
	}
	s.Phase = "blocked"
	s.Error = "installer absent; inspect migration-journal.json and worker.log before retrying the installed target"
	_ = m.save(s)
}
func (m *Manager) checkFlight() <-chan struct{} {
	m.checkMu.Lock()
	defer m.checkMu.Unlock()
	if m.checking != nil {
		return m.checking
	}
	flight := make(chan struct{})
	m.checking = flight
	go func() { m.doCheck(); m.checkMu.Lock(); close(flight); m.checking = nil; m.checkMu.Unlock() }()
	return flight
}
func (m *Manager) check() {
	select {
	case <-m.checkFlight():
	case <-m.ctx.Done():
	}
}
func (m *Manager) doCheck() {
	m.mu.Lock()
	defer m.mu.Unlock()
	phase := m.state().Phase
	if m.Host != nil && !m.busy && phase == "downloading" {
		download, err := openLock(filepath.Join(m.Host.Directory, "download.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != nil {
			return
		}
		download.Close()
		s := m.state()
		s.Phase = "failed"
		s.Error = "download interrupted by process restart"
		_ = m.save(s)
	}
	installing := m.busy || m.Host != nil && m.Host.Maintaining()
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	data, err := fetch(ctx, m.client, "https://github.com/jjyr/pot/releases/latest/download/update.json", 1<<20)
	feedUnavailable := err != nil
	var release Release
	reason, discovery := "", "verified"
	if err == nil {
		release, err = Verify(data, buildinfo.UpdatePublicKey)
	}
	if err != nil {
		reason, discovery = "verification", "manual"
		if buildinfo.UpdatePublicKey == "" {
			reason = "no_key"
		}
		body, fallbackErr := fetch(ctx, m.client, "https://api.github.com/repos/jjyr/pot/releases/latest", 1<<20)
		var public struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if fallbackErr != nil || json.Unmarshal(body, &public) != nil || public.Draft || public.Prerelease || !strings.HasPrefix(public.Tag, "v") || !versionPattern.MatchString(strings.TrimPrefix(public.Tag, "v")) {
			m.discoveryMu.Lock()
			if feedUnavailable {
				m.discovery = "unreachable"
			} else {
				m.discovery = "invalid_signature"
			}
			m.discoveryMu.Unlock()
			if m.Notify != nil {
				m.Notify()
			}
			return
		}
		release = Release{Version: strings.TrimPrefix(public.Tag, "v")}
	} else {
		reason = eligibility(release)
		if reason == "" && m.Host != nil {
			if _, e := m.artifact(release); e != nil {
				reason = "artifact"
			}
		}
	}
	if !newer(release.Version, buildinfo.Version) && buildinfo.Version != "development" {
		release.Version = ""
		discovery = "current"
	}
	m.discoveryMu.Lock()
	m.detected, m.reason, m.discovery = release.Version, reason, discovery
	m.discoveryMu.Unlock()
	if !installing {
		m.release, m.envelope = nil, nil
		if reason == "" && release.Version != "" {
			m.release = &release
			m.envelope = data
		}
	}
	if !installing && m.Host != nil && release.Version != "" {
		old := m.state()
		if old.Version != release.Version || old.Phase != "ready" && old.Phase != "failed" && old.Phase != "blocked" {
			_ = m.save(State{Version: release.Version, Phase: "available"})
		}
	}
	if !installing && m.Host != nil && release.Version == "" {
		_ = m.save(State{Version: buildinfo.Version, Phase: "current"})
	}
	if m.Notify != nil {
		m.Notify()
	}
}

func (m *Manager) artifact(r Release) (Artifact, error) {
	for _, a := range r.Artifacts {
		if a.Kind == m.Host.Package.Kind && a.Platform == m.Host.Package.Platform {
			return a, nil
		}
	}
	return Artifact{}, errors.New("release has no artifact for this installation")
}
func (m *Manager) Call(ctx context.Context, method string, body json.RawMessage) (any, error) {
	if method == "GET" {
		return m.Status()
	}
	if method != "POST" {
		return nil, protocol.APIError{Code: "method_not_allowed"}
	}
	var command Command
	if err := protocol.DecodeStrict(body, &command); err != nil {
		return nil, err
	}
	if command.Action == "check" {
		if command.Version != "" || command.Interrupt || !protocolID(command.RequestID) {
			return nil, protocol.APIError{Code: "invalid_update"}
		}
		m.check()
		return m.Status()
	}
	if m.Host == nil {
		return nil, protocol.APIError{Code: "update_unavailable"}
	}
	if command.Action == "retry" {
		status, err := m.Status()
		if err != nil {
			return nil, err
		}
		if !status.CanRetry || command.Version != status.Version || !protocolID(command.RequestID) || command.Interrupt {
			return nil, protocol.APIError{Code: "update_unavailable"}
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		operation, err := openLock(filepath.Join(m.Host.Directory, "operation.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != nil {
			return nil, protocol.APIError{Code: "update_busy"}
		}
		defer operation.Close()
		if err := m.install(command, m.state()); err != nil {
			return nil, err
		}
		return m.Status()
	}
	if !newer(command.Version, buildinfo.Version) {
		return nil, protocol.APIError{Code: "update_stale"}
	}
	status, statusErr := m.Status()
	if statusErr != nil {
		return nil, statusErr
	}
	if !status.Supported || !status.AutoInstall {
		return nil, protocol.APIError{Code: "update_unavailable"}
	}
	if !versionPattern.MatchString(command.Version) || !protocolID(command.RequestID) || (command.Action != "download" && command.Action != "install") {
		return nil, protocol.APIError{Code: "invalid_update"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	operation, err := openLock(filepath.Join(m.Host.Directory, "operation.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		return nil, protocol.APIError{Code: "update_busy"}
	}
	defer operation.Close()
	s := m.state()
	if s.RequestID == command.RequestID && s.Version == command.Version {
		return m.Status()
	}
	if !newer(command.Version, buildinfo.Version) {
		return nil, protocol.APIError{Code: "update_stale"}
	}
	if m.busy || s.Phase == "downloading" || s.Phase == "installing" || s.Phase == "restarting" || s.Phase == "blocked" {
		return nil, protocol.APIError{Code: "update_busy"}
	}
	if command.Action == "download" {
		if s.Phase == "ready" && s.Version == command.Version {
			return m.Status()
		}
		if m.release == nil || m.release.Version != command.Version {
			return nil, protocol.APIError{Code: "update_stale"}
		}
		artifact, err := m.artifact(*m.release)
		if err != nil {
			return nil, err
		}
		downloadLock, err := openLock(filepath.Join(m.Host.Directory, "download.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != nil {
			return nil, protocol.APIError{Code: "update_busy"}
		}
		s = State{Version: command.Version, Phase: "downloading", RequestID: command.RequestID}
		if err = m.save(s); err != nil {
			downloadLock.Close()
			return nil, err
		}
		m.busy = true
		go func() {
			defer downloadLock.Close()
			err := m.download(m.ctx, artifact, command.Version)
			m.mu.Lock()
			defer m.mu.Unlock()
			m.busy = false
			s.Phase = "ready"
			if err != nil {
				s.Phase = "failed"
				s.Error = err.Error()
			}
			_ = m.save(s)
			if err == nil {
				operation, e := openLock(filepath.Join(m.Host.Directory, "operation.lock"), syscall.LOCK_EX)
				if e != nil {
					return
				}
				defer operation.Close()
				if current := m.state(); current.Phase != "ready" || current.Version != s.Version {
					return
				}
				// A click continues without a browser connection. If work began
				// during download, retain ready state for explicit confirmation.
				_ = m.install(Command{Action: "install", Version: s.Version, RequestID: s.RequestID + "-install", Interrupt: command.Interrupt}, s)
			}
		}()
	} else {
		if s.Version != command.Version || s.Phase != "ready" {
			return nil, protocol.APIError{Code: "update_stale"}
		}
		if err := m.install(command, s); err != nil {
			return nil, err
		}
	}
	return m.Status()
}
func protocolID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func (m *Manager) download(ctx context.Context, a Artifact, version string) error {
	stage := filepath.Join(m.Host.Directory, "stage-"+version)
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.Mkdir(stage, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(m.Host.Directory, "download-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	req, err := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if err != nil {
		return err
	}
	response, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("update HTTP %d", response.StatusCode)
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, a.Size+1))
	if err != nil {
		return err
	}
	if n != a.Size || hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return errors.New("update artifact checksum mismatch")
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = extract(file.Name(), stage, a.Kind == "app"); err != nil {
		return err
	}
	entries, err := os.ReadDir(stage)
	if a.Kind == "app" {
		filtered := entries[:0]
		for _, entry := range entries {
			if entry.Name() != "__MACOSX" {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	if err != nil || len(entries) != 1 {
		return errors.New("invalid update package layout")
	}
	target := filepath.Join(stage, entries[0].Name())
	metadata := filepath.Join(target, "Resources/update.json")
	if a.Kind == "app" {
		metadata = filepath.Join(target, "Contents/Resources/update.json")
	}
	data, err := os.ReadFile(metadata)
	if err != nil {
		return err
	}
	var installed Installation
	if err = json.Unmarshal(data, &installed); err != nil {
		return err
	}
	if installed.Version != version || m.release == nil || installed.StorageFormat != m.release.StorageFormat || installed.Protocol != UpdateProtocol || installed.Kind != a.Kind || installed.Platform != a.Platform {
		return errors.New("update package metadata mismatch")
	}
	// Prepared paths are private installation state; package bytes were verified above.
	if m.release == nil {
		return errors.New("release unavailable")
	}
	if err := os.WriteFile(filepath.Join(m.Host.Directory, "verified-feed.json"), m.envelope, 0600); err != nil {
		return err
	}
	return instance.WriteJSON(filepath.Join(m.Host.Directory, "prepared.json"), struct{ Version, Path string }{version, target})
}

func (m *Manager) install(command Command, s State) error {
	status, err := m.Status()
	if err != nil {
		return err
	}
	if status.Work.Active() && !command.Interrupt {
		return protocol.APIError{Code: "update_confirmation_required"}
	}
	if err := checkInstallation(m.Host.Install, m.Host.Package); err != nil {
		return protocol.APIError{Code: "update_unavailable"}
	}
	job := Job{Install: m.Host.Install, Directory: m.Host.Directory, Version: command.Version, Interrupt: command.Interrupt, Package: m.Host.Package, Retry: command.Action == "retry"}
	path := filepath.Join(m.Host.Directory, "job.json")
	if err = instance.WriteJSON(path, job); err != nil {
		return err
	}
	s.Phase = "installing"
	s.RequestID = command.RequestID
	if err = m.save(s); err != nil {
		return err
	}
	executable := filepath.Join(m.Host.Install, "bin/pot")
	if m.Host.Package.Kind == "app" {
		executable = filepath.Join(m.Host.Install, "Contents/MacOS/pot-app")
	}
	worker := exec.Command(executable, "_update-worker", path)
	worker.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	log, err := os.OpenFile(filepath.Join(m.Host.Directory, "worker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		defer log.Close()
		worker.Stdout = log
		worker.Stderr = log
		err = worker.Start()
	}
	if err != nil {
		s.Phase = "failed"
		s.Error = err.Error()
		_ = m.save(s)
		return err
	}
	go func() {
		if err := worker.Wait(); err != nil {
			current := m.state()
			if current.Phase == "installing" || current.Phase == "restarting" {
				current.Phase = "blocked"
				current.Error = "installer process exited unexpectedly"
				_ = m.save(current)
			}
		}
	}()
	return nil
}
