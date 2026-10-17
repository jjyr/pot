package processscope

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func prepareNative() error { return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }

type nativeObserver struct{}

func observeNative(int, Markers) (*nativeObserver, error) { return &nativeObserver{}, nil }
func (*nativeObserver) Close()                            {}
func (*nativeObserver) Drain(ctx context.Context) error {
	for {
		for {
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if errors.Is(err, unix.ECHILD) {
				return nil
			}
			if err != nil {
				return err
			}
			if pid == 0 {
				break
			}
		}
		files, err := filepath.Glob("/proc/self/task/*/children")
		if err != nil {
			return err
		}
		for _, path := range files {
			body, err := os.ReadFile(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
			for _, text := range strings.Fields(string(body)) {
				pid, err := strconv.Atoi(text)
				if err != nil {
					return err
				}
				if err = unix.Kill(pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
					return err
				}
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("descendant cleanup deadline exceeded")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
