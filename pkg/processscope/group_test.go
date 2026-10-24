package processscope

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

func TestKillGroupRequiresDisappearance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		errors []error
		want   error
	}{
		{name: "already gone", errors: []error{syscall.ESRCH}},
		{name: "live then reaped", errors: []error{nil, syscall.EPERM, syscall.ESRCH}},
		{name: "permission restored", errors: []error{syscall.EPERM, nil, syscall.ESRCH}},
		{name: "persistent permission denial", errors: []error{syscall.EPERM}, want: syscall.EPERM},
		{name: "unexpected failure", errors: []error{syscall.EINVAL}, want: syscall.EINVAL},
		{name: "group remains", errors: []error{nil}, want: syscall.ETIMEDOUT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			timeout := time.Second
			if tc.want != nil {
				timeout = 30 * time.Millisecond
			}
			err := killGroup(123, time.Now().Add(timeout), func(pid int, signal syscall.Signal) error {
				if pid != -123 || signal != syscall.SIGKILL {
					t.Fatalf("signal target = %d/%v", pid, signal)
				}
				result := tc.errors[min(calls, len(tc.errors)-1)]
				calls++
				return result
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("cleanup = %v, want %v", err, tc.want)
			}
			if tc.want == nil && calls != len(tc.errors) {
				t.Fatalf("cleanup accepted before ESRCH: %d calls", calls)
			}
			if tc.want == syscall.EPERM && calls < 2 {
				t.Fatal("permission denial was not retried")
			}
		})
	}
}

func TestKillGroupRejectsInvalidTargets(t *testing.T) {
	for _, pid := range []int{0, -1, -123} {
		if err := killGroup(pid, time.Now().Add(time.Second), func(int, syscall.Signal) error {
			t.Fatal("invalid group signaled")
			return nil
		}); err == nil {
			t.Fatalf("accepted process group %d", pid)
		}
	}
}
