package resource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Context context.Context
	Client  *http.Client
}
type job struct {
	resource Resource
	ctx      context.Context
	cancel   context.CancelFunc
	waiters  int
	progress Progress
	changed  chan struct{}
	done     bool
	value    Verified
	err      error
}
type Store struct {
	root    string
	client  *http.Client
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	jobs    map[string]*job
	locks   map[string]chan struct{}
	workers sync.WaitGroup
	closed  bool
}

func New(root string, options Options) (*Store, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("resource root must be a private directory")
	}
	parent := options.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	client := http.Client{Timeout: time.Hour}
	if options.Client != nil {
		client = *options.Client
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > 5 || request.URL.Scheme != "https" {
			return errors.New("unsafe resource redirect")
		}
		return nil
	}
	return &Store{root: root, client: &client, ctx: ctx, cancel: cancel, jobs: map[string]*job{}, locks: map[string]chan struct{}{}}, nil
}
func (s *Store) Root() string { return s.root }
func (s *Store) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.workers.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
func (s *Store) lockLocked(digest string) chan struct{} {
	lock := s.locks[digest]
	if lock == nil {
		lock = make(chan struct{}, 1)
		lock <- struct{}{}
		s.locks[digest] = lock
	}
	return lock
}
func (s *Store) changedLocked(j *job) { close(j.changed); j.changed = make(chan struct{}) }
func (s *Store) progress(j *job, p Progress) {
	s.mu.Lock()
	j.progress = p
	s.changedLocked(j)
	s.mu.Unlock()
}

// Prepare shares one transfer per digest while each subscriber retains its own
// cancellation. Only the last departing subscriber cancels the shared work.
func (s *Store) Prepare(ctx context.Context, r Resource, repo string, emit func(Progress)) (Verified, error) {
	if err := r.Validate(); err != nil {
		return Verified{}, err
	}
	if err := ctx.Err(); err != nil {
		return Verified{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Verified{}, errors.New("resource store closed")
	}
	j := s.jobs[r.SHA256]
	if j != nil && j.resource != r {
		s.mu.Unlock()
		return Verified{}, errors.New("resource digest metadata conflict")
	}
	if j == nil {
		runctx, cancel := context.WithCancel(s.ctx)
		j = &job{resource: r, ctx: runctx, cancel: cancel, progress: Progress{Phase: "downloading", TotalBytes: r.SizeBytes}, changed: make(chan struct{})}
		s.jobs[r.SHA256] = j
		lock := s.lockLocked(r.SHA256)
		s.workers.Add(1)
		go s.run(j, repo, lock)
	}
	j.waiters++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		j.waiters--
		if j.waiters == 0 && !j.done {
			if s.jobs[r.SHA256] == j {
				delete(s.jobs, r.SHA256)
			}
			j.cancel()
		}
		s.mu.Unlock()
	}()
	for {
		s.mu.Lock()
		p, changed, done, value, err := j.progress, j.changed, j.done, j.value, j.err
		s.mu.Unlock()
		if emit != nil {
			emit(p)
		}
		if done {
			return value, err
		}
		select {
		case <-ctx.Done():
			return Verified{}, ctx.Err()
		case <-changed:
		}
	}
}
func (s *Store) run(j *job, repo string, lock chan struct{}) {
	defer s.workers.Done()
	var value Verified
	var err error
	select {
	case <-j.ctx.Done():
		err = j.ctx.Err()
	case <-lock:
		value, err = s.transfer(j.ctx, j.resource, repo, func(p Progress) { s.progress(j, p) })
		lock <- struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j.value = value
	j.err = err
	j.done = true
	j.cancel()
	if s.jobs[j.resource.SHA256] == j {
		delete(s.jobs, j.resource.SHA256)
	}
	s.changedLocked(j)
}
func (s *Store) transfer(ctx context.Context, r Resource, repo string, emit func(Progress)) (Verified, error) {
	if cached, ok, err := s.Cached(ctx, r); err != nil || ok {
		return cached, err
	}
	base, err := url.Parse(repo)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return Verified{}, errors.New("HTTPS resource repository required")
	}
	if err = space(s.root, r.SizeBytes+r.UnpackedBytes); err != nil {
		return Verified{}, err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/releases/download/" + r.ReleaseTag + "/" + r.Filename
	temporary, err := os.MkdirTemp(s.root, ".download-")
	if err != nil {
		return Verified{}, err
	}
	defer os.RemoveAll(temporary)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return Verified{}, err
	}
	response, err := s.client.Do(request)
	if err != nil {
		return Verified{}, errors.New("resource download connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Verified{}, fmt.Errorf("resource download HTTP %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && uint64(response.ContentLength) != r.SizeBytes {
		return Verified{}, errors.New("resource download size mismatch")
	}
	file, err := os.OpenFile(filepath.Join(temporary, "blob"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Verified{}, err
	}
	defer file.Close()
	digest := sha256.New()
	progress := &progressWriter{writer: io.MultiWriter(file, digest), total: r.SizeBytes, start: time.Now(), emit: emit}
	count, err := io.Copy(progress, &contextReader{ctx, io.LimitReader(response.Body, int64(r.SizeBytes)+1)})
	if err != nil {
		return Verified{}, err
	}
	if uint64(count) != r.SizeBytes || hex.EncodeToString(digest.Sum(nil)) != r.SHA256 {
		return Verified{}, errors.New("resource download digest or size mismatch")
	}
	if err = file.Sync(); err != nil {
		return Verified{}, err
	}
	if err = file.Close(); err != nil {
		return Verified{}, err
	}
	return s.publish(ctx, temporary, r, emit)
}
func (s *Store) publish(ctx context.Context, temporary string, r Resource, emit func(Progress)) (Verified, error) {
	if err := verify(ctx, filepath.Join(temporary, "blob"), r); err != nil {
		return Verified{}, err
	}
	if emit != nil {
		emit(Progress{Phase: "extracting", DownloadedBytes: r.SizeBytes, TotalBytes: r.SizeBytes})
	}
	if err := unpack(ctx, temporary, r); err != nil {
		return Verified{}, err
	}
	if err := ctx.Err(); err != nil {
		return Verified{}, err
	}
	destination := filepath.Join(s.root, r.SHA256)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return Verified{}, errors.New("existing resource cache requires repair")
	}
	if err := os.Rename(temporary, destination); err != nil {
		return Verified{}, err
	}
	directory, err := os.Open(s.root)
	if err != nil {
		return Verified{}, err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return Verified{}, err
	}
	value, found, err := s.Cached(ctx, r)
	if err != nil {
		return Verified{}, err
	}
	if !found {
		return Verified{}, errors.New("resource publication incomplete")
	}
	if emit != nil {
		emit(Progress{Phase: "complete", DownloadedBytes: r.SizeBytes, TotalBytes: r.SizeBytes})
	}
	return value, nil
}
func (s *Store) Import(ctx context.Context, r Resource, source string) (Verified, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if err := r.Validate(); err != nil {
		return Verified{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Verified{}, errors.New("resource store closed")
	}
	lock := s.lockLocked(r.SHA256)
	s.workers.Add(1)
	s.mu.Unlock()
	defer s.workers.Done()
	select {
	case <-lock:
		defer func() { lock <- struct{}{} }()
	default:
		return Verified{}, errors.New("resource is being prepared")
	}
	if cached, ok, err := s.Cached(ctx, r); err != nil || ok {
		return cached, err
	}
	if err := space(s.root, r.SizeBytes+r.UnpackedBytes); err != nil {
		return Verified{}, err
	}
	if err := regular(source); err != nil {
		return Verified{}, err
	}
	temporary, err := os.MkdirTemp(s.root, ".import-")
	if err != nil {
		return Verified{}, err
	}
	defer os.RemoveAll(temporary)
	// Validate copied bytes, not an earlier read of a mutable source path.
	if err = copyFileBounded(ctx, source, filepath.Join(temporary, "blob"), r.SizeBytes); err != nil {
		return Verified{}, err
	}
	return s.publish(ctx, temporary, r, nil)
}

type progressWriter struct {
	writer       io.Writer
	count, total uint64
	start        time.Time
	emit         func(Progress)
}

func (p *progressWriter) Write(data []byte) (int, error) {
	if uint64(len(data)) > p.total-p.count {
		return 0, errors.New("resource exceeds expected size")
	}
	n, err := p.writer.Write(data)
	p.count += uint64(n)
	if p.emit != nil {
		speed := uint64(float64(p.count) / max(.001, time.Since(p.start).Seconds()))
		var remaining *uint64
		if speed > 0 {
			eta := (p.total - p.count) / speed
			remaining = &eta
		}
		p.emit(Progress{Phase: "downloading", DownloadedBytes: p.count, TotalBytes: p.total, BytesPerSecond: speed, RemainingSeconds: remaining})
	}
	return n, err
}
func space(root string, required uint64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(root, &stat); err != nil {
		return err
	}
	if stat.Bsize <= 0 || stat.Bavail > ^uint64(0)/uint64(stat.Bsize) {
		return errors.New("resource free-space overflow")
	}
	if stat.Bavail*uint64(stat.Bsize) < required {
		return errors.New("insufficient resource cache space")
	}
	return nil
}
