package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jjyr/pot/internal/buildinfo"
	"github.com/jjyr/pot/pkg/instance"
)

type Installation struct {
	Version       string `json:"version"`
	Kind          string `json:"kind"`
	Platform      string `json:"platform"`
	StorageFormat string `json:"storage_format"`
}
type Work struct {
	Tasks    int `json:"tasks"`
	Replies  int `json:"replies"`
	Sessions int `json:"sessions"`
}

func (w Work) Active() bool { return w.Tasks+w.Replies+w.Sessions > 0 }

type Participant struct {
	Socket      string `json:"socket"`
	Root        string `json:"root"`
	Version     string `json:"version"`
	Work        Work   `json:"work"`
	Ready       bool   `json:"ready"`
	Unsupported bool   `json:"unsupported"`
}
type Host struct {
	Install, Directory, Executable string
	Package                        Installation
	mu                             sync.Mutex
	root                           string
	unsupported                    bool
	work                           func() (Work, error)
	cancel                         context.CancelFunc
	done                           chan struct{}
	result                         error
	stopped                        atomic.Bool
	ready                          atomic.Bool
	server                         *http.Server
	listener                       net.Listener
	registration                   string
	lock                           *os.File
	resume                         chan struct{}
}
type hostKey struct{}

func FromContext(ctx context.Context) *Host { h, _ := ctx.Value(hostKey{}).(*Host); return h }
func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("update directory must be private and owned")
	}
	return nil
}
func openLock(path string, how int) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if err = syscall.Flock(fd, how); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// OpenHost recognizes only complete, signed product installations. It registers
// long-lived hosts, never transient CLI commands or arbitrary executables.
func OpenHost() (*Host, error) {
	if buildinfo.UpdatePublicKey == "" {
		return nil, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(executable)
	install := filepath.Dir(base)
	resources := filepath.Join(install, "Resources")
	if filepath.Base(base) == "MacOS" {
		install = filepath.Dir(filepath.Dir(base))
		resources = filepath.Join(install, "Contents/Resources")
	}
	var info Installation
	data, err := os.ReadFile(filepath.Join(resources, "update.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	if info.Version != buildinfo.Version || info.StorageFormat != storageFormat() || (info.Kind != "cli" && info.Kind != "app") {
		return nil, errors.New("installed update metadata does not match executable")
	}
	directory := install + ".updates"
	if err = privateDirectory(directory); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, nil
		}
		return nil, err
	}
	lock, err := openLock(filepath.Join(directory, "installation.lock"), syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(install))
	socketDirectory := filepath.Join(os.TempDir(), "pot-update-"+strconv.Itoa(os.Geteuid())+"-"+hex.EncodeToString(digest[:8]))
	if err = privateDirectory(socketDirectory); err != nil {
		lock.Close()
		return nil, err
	}
	socket := filepath.Join(socketDirectory, strconv.Itoa(os.Getpid())+".sock")
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		lock.Close()
		return nil, err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		lock.Close()
		return nil, err
	}
	h := &Host{Install: install, Directory: directory, Executable: executable, Package: info, listener: listener, lock: lock, done: make(chan struct{}), resume: make(chan struct{}, 1), registration: filepath.Join(directory, fmt.Sprintf("host-%d.json", os.Getpid()))}
	h.server = &http.Server{Handler: http.HandlerFunc(h.handle), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Second}
	go h.server.Serve(listener)
	return h, nil
}
func (h *Host) Bind(root string, work func() (Work, error)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.root = root
	h.protectLocked(root)
	h.work = work
	return instance.WriteJSON(h.registration, Participant{Socket: h.listener.Addr().String(), Root: root, Version: buildinfo.Version, Ready: h.ready.Load(), Unsupported: h.unsupported})
}
func (h *Host) status() (Participant, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := Participant{Socket: h.listener.Addr().String(), Root: h.root, Version: buildinfo.Version, Ready: h.ready.Load(), Unsupported: h.unsupported}
	if h.work != nil && !h.stopped.Load() {
		var err error
		p.Work, err = h.work()
		if err != nil {
			return p, err
		}
	}
	return p, nil
}
func (h *Host) Maintaining() bool {
	if h.stopped.Load() {
		return true
	}
	var state State
	data, err := os.ReadFile(filepath.Join(h.Directory, "state.json"))
	if err != nil {
		return !os.IsNotExist(err)
	}
	if json.Unmarshal(data, &state) != nil {
		return true
	}
	return state.Phase == "installing" || state.Phase == "restarting" || state.Phase == "blocked"
}
func (h *Host) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "GET" && r.URL.Path == "/status":
		p, err := h.status()
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		_ = json.NewEncoder(w).Encode(p)
	case r.Method == "POST" && r.URL.Path == "/prepare":
		h.mu.Lock()
		if h.cancel == nil {
			h.mu.Unlock()
			http.Error(w, "host is not ready", 409)
			return
		}
		h.stopped.Store(true)
		h.cancel()
		h.mu.Unlock()
		select {
		case <-h.done:
			if h.result != nil {
				http.Error(w, h.result.Error(), 409)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"ready": true})
		case <-r.Context().Done():
			return
		}
	case r.Method == "POST" && r.URL.Path == "/resume":
		if !h.stopped.Load() {
			http.Error(w, "host is not stopped", 409)
			return
		}
		select {
		case h.resume <- struct{}{}:
		default:
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"accepted": true})
	default:
		http.NotFound(w, r)
	}
}
func (h *Host) Run(ctx context.Context, run func(context.Context) error) error {
	defer func() {
		h.server.Close()
		h.listener.Close()
		os.Remove(h.registration)
		if h.lock != nil {
			h.lock.Close()
		}
	}()
	runctx, cancel := context.WithCancel(context.WithValue(ctx, hostKey{}, h))
	defer cancel()
	h.mu.Lock()
	h.cancel = cancel
	h.mu.Unlock()
	h.result = run(runctx)
	if !h.stopped.Load() {
		return h.result
	}
	if h.lock != nil {
		h.lock.Close()
		h.lock = nil
	}
	close(h.done)
	select {
	case <-h.resume:
		// Keep the supervisor's original PID and original argv/environment. Services
		// restart in place, without duplicate daemons or privileged service commands.
		return syscall.Exec(h.Executable, os.Args, os.Environ())
	case <-ctx.Done():
		return h.result
	}
}
func participants(directory string) ([]Participant, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	result := []Participant{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "host-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var p Participant
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		err = localCall(context.Background(), p.Socket, "GET", "/status", &p)
		if err != nil { // Only a missing/refused socket proves a stale registration.
			var op *net.OpError
			if errors.As(err, &op) && (errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist)) {
				_ = os.Remove(filepath.Join(directory, entry.Name()))
				continue
			}
			return nil, err
		}
		result = append(result, p)
	}
	return result, nil
}
func localCall(ctx context.Context, socket, method, path string, out any) error {
	c := &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer c.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, method, "http://update"+path, nil)
	if err != nil {
		return err
	}
	response, err := c.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("local update participant HTTP %d", response.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(response.Body).Decode(out)
	}
	return nil
}

func (h *Host) Ready() { h.ready.Store(true) }

// Protect prevents installation replacement from moving user state/workspaces.
func (h *Host) Protect(paths ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, path := range paths {
		h.protectLocked(path)
	}
}
func (h *Host) protectLocked(path string) {
	if path == "" {
		return
	}
	path, err := filepath.Abs(path)
	if err != nil {
		h.unsupported = true
		return
	}
	if resolved, e := filepath.EvalSymlinks(path); e == nil {
		path = resolved
	}
	for _, base := range []string{h.Install, h.Directory} {
		rel, err := filepath.Rel(base, path)
		if err != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			h.unsupported = true
		}
	}
}
