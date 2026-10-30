package rollout

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"golang.org/x/sys/unix"
)

var (
	ErrNotEligible = errors.New("rollout is not eligible for compression")
	ErrChanged     = errors.New("rollout changed during compression")
)

// Compression is a prepared archive. Its methods must be called serially.
// Callers must recheck the committed reference and owner inactivity before
// Commit, and serialize that check/publication with reference updates.
type Compression struct {
	path, temp string
	info       os.FileInfo
}

// PrepareCompression performs expensive work without holding a rollout lock.
// Only the committed prefix is compressed; publication rejects any source change.
func PrepareCompression(ctx context.Context, path string, ref Ref, bytesPerSecond int64) (_ *Compression, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = ref.validate(); err != nil {
		return nil, err
	}
	if ref.IsZero() {
		return nil, ErrNotEligible
	}
	f, err := openRegular(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotEligible
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	pace := func(r io.Reader) io.Reader { return &pacedReader{ctx: ctx, reader: r, bytesPerSecond: bytesPerSecond} }
	_, offset, err := replay(pace(f), ref)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".rollout-compress-*")
	if err != nil {
		return nil, err
	}
	c := &Compression{path: path, temp: temp.Name(), info: info}
	defer func() {
		temp.Close()
		if err != nil {
			_ = c.Abort()
		}
	}()
	z, err := newArchiveWriter(temp)
	if err != nil {
		return nil, err
	}
	expected := sha256.New()
	n, err := io.Copy(io.MultiWriter(z, expected), pace(io.LimitReader(f, offset)))
	if err != nil {
		_ = z.Close()
		return nil, err
	}
	if n != offset {
		_ = z.Close()
		return nil, ErrChanged
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	if err = temp.Sync(); err != nil {
		return nil, err
	}
	if _, err = temp.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	zr, err := newArchiveReader(pace(temp))
	if err != nil {
		return nil, err
	}
	actual := sha256.New()
	size, copyErr := io.Copy(actual, pace(zr))
	zr.Close()
	if copyErr != nil {
		return nil, copyErr
	}
	if size != n || !bytes.Equal(actual.Sum(nil), expected.Sum(nil)) {
		return nil, fmt.Errorf("%w: compressed verification mismatch", ErrCorrupt)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = temp.Close(); err != nil {
		return nil, err
	}
	return c, nil
}

// Abort removes an unpublished temporary archive. It is safe to call repeatedly.
func (c *Compression) Abort() error {
	if c.temp == "" {
		return nil
	}
	err := os.Remove(c.temp)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		c.temp = ""
		return nil
	}
	return err
}

// Commit publishes with a nonblocking, short-lived file lock. Failed commits
// discard the temporary archive; callers can retry preparation on a later check.
func (c *Compression) Commit(ctx context.Context) error {
	defer c.Abort()
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.temp == "" {
		return ErrNotEligible
	}
	guard, err := openRegular(c.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer guard.Close()
	if err = unix.Flock(int(guard.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return ErrChanged
		}
		return err
	}
	defer unix.Flock(int(guard.Fd()), unix.LOCK_UN)
	info, err := os.Lstat(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrChanged
	}
	if err != nil {
		return err
	}
	if !sameCompressionSource(c.info, info) {
		return ErrChanged
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(c.temp, c.path+".zst"); err != nil {
		return err
	}
	c.temp = ""
	if err = syncDirectory(filepath.Dir(c.path)); err != nil {
		return err
	}
	if err = os.Remove(c.path); err != nil {
		return err
	}
	forget(c.path)
	return syncDirectory(filepath.Dir(c.path))
}

func sameCompressionSource(a, b os.FileInfo) bool {
	if !b.Mode().IsRegular() || !os.SameFile(a, b) || a.Size() != b.Size() || a.ModTime() != b.ModTime() || a.Mode() != b.Mode() {
		return false
	}
	// Reading can update atime. Compare all other stat fields, including ctime,
	// without depending on platform-specific Stat_t field spellings.
	x, y := reflect.ValueOf(a.Sys()), reflect.ValueOf(b.Sys())
	if x.Kind() != reflect.Pointer || y.Kind() != reflect.Pointer {
		return reflect.DeepEqual(a.Sys(), b.Sys())
	}
	x, y = x.Elem(), y.Elem()
	if x.Type() != y.Type() || x.Kind() != reflect.Struct {
		return reflect.DeepEqual(a.Sys(), b.Sys())
	}
	for i := 0; i < x.NumField(); i++ {
		name := x.Type().Field(i).Name
		if name == "Atim" || name == "Atimespec" || name == "Atime" || name == "Atimensec" {
			continue
		}
		if !reflect.DeepEqual(x.Field(i).Interface(), y.Field(i).Interface()) {
			return false
		}
	}
	return true
}

type pacedReader struct {
	ctx            context.Context
	reader         io.Reader
	bytesPerSecond int64
}

func (p *pacedReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	if len(b) > 32<<10 {
		b = b[:32<<10]
	}
	n, err := p.reader.Read(b)
	if n > 0 && p.bytesPerSecond > 0 {
		timer := time.NewTimer(time.Duration(float64(n) / float64(p.bytesPerSecond) * float64(time.Second)))
		defer timer.Stop()
		select {
		case <-p.ctx.Done():
			return n, p.ctx.Err()
		case <-timer.C:
		}
	}
	if p.ctx.Err() != nil {
		return n, p.ctx.Err()
	}
	return n, err
}
