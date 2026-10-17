//go:build darwin && cgo

package processscope

/*
#include <stdlib.h>
#include <stdint.h>
#include <dlfcn.h>
#include <libproc.h>
#include <sys/types.h>
#include <errno.h>

// sandbox_check is variadic; call through C so Darwin ARM64's variadic ABI is
// respected. No entitlement or privileged helper is requested.
static int scope_member(int pid, const char *allow, const char *deny) {
 typedef int (*check_fn)(pid_t,const char*,int,...);
 static check_fn check;
 static int quiet;
 if (!check) {void *lib=dlopen("/usr/lib/libsandbox.dylib",RTLD_LAZY|RTLD_LOCAL);if(lib){check=(check_fn)dlsym(lib,"sandbox_check");int *flag=(int*)dlsym(lib,"SANDBOX_CHECK_NO_REPORT");if(flag)quiet=*flag;}}
 if (!check) return -1;
 int a=check(pid,"file-read-data",(1|quiet),allow);
 int b=check(pid,"file-read-data",(1|quiet),deny);
 if (a<0 || b<0) return -1;
 return a==0 && b!=0;
}
struct scope_identity { unsigned char uuid[16]; uint64_t unique; uint64_t parent; int version; int parent_version; uint64_t reserved[2]; };
static int scope_identity(int pid, uint64_t *unique, uint64_t *parent) {
 struct scope_identity info;
 if (proc_pidinfo(pid,17,0,&info,sizeof(info))!=(int)sizeof(info)) return -1;
 *unique=info.unique; *parent=info.parent; return 0;
}
*/
import "C"

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"sync"
	"time"
	"unsafe"
)

func prepareNative() error { return nil }

type nativeObserver struct {
	mu          sync.Mutex
	known       map[uint64]bool
	markers     Markers
	allow, deny *C.char
	stop        chan struct{}
	done        chan struct{}
	err         error
}

func identity(pid int) (uint64, uint64, bool) {
	var id, parent C.uint64_t
	if C.scope_identity(C.int(pid), &id, &parent) != 0 {
		return 0, 0, false
	}
	return uint64(id), uint64(parent), true
}
func observeNative(pid int, markers Markers) (*nativeObserver, error) {
	id, _, ok := identity(pid)
	if !ok {
		return nil, errors.New("process identity unavailable")
	}
	o := &nativeObserver{known: map[uint64]bool{id: true}, markers: markers, stop: make(chan struct{}), done: make(chan struct{})}
	if markers.Allow != "" {
		o.allow = C.CString(markers.Allow)
		o.deny = C.CString(markers.Deny)
	}
	go func() {
		defer close(o.done)
		for {
			select {
			case <-o.stop:
				return
			case <-time.After(5 * time.Millisecond):
				o.mu.Lock()
				_, err := o.members()
				if err != nil {
					o.err = err
				}
				o.mu.Unlock()
			}
		}
	}()
	return o, nil
}
func (o *nativeObserver) members() ([]int, error) {
	rows, err := unix.SysctlKinfoProcSlice("kern.proc.uid", unix.Getuid())
	if err != nil {
		return nil, err
	}
	type entry struct {
		pid        int
		id, parent uint64
	}
	entries := []entry{}
	for _, row := range rows {
		pid := int(row.Proc.P_pid)
		id, parent, ok := identity(pid)
		if ok {
			entries = append(entries, entry{pid, id, parent})
		}
	}
	// Stable original-parent identities continue identifying detached children
	// after reparenting, unlike PPID/process-group snapshots.
	for changed := true; changed; {
		changed = false
		for _, item := range entries {
			if o.known[item.parent] && !o.known[item.id] {
				o.known[item.id] = true
				changed = true
			}
		}
	}
	found := []int{}
	for _, item := range entries {
		member := o.known[item.id]
		if o.allow != nil {
			result := C.scope_member(C.int(item.pid), o.allow, o.deny)
			if result < 0 {
				if _, e := unix.SysctlKinfoProc("kern.proc.pid", item.pid); e == nil {
					return nil, errors.New("cannot inspect sandbox membership")
				}
				continue
			}
			member = member || result == 1
		}
		if member {
			found = append(found, item.pid)
		}
	}
	return found, nil
}
func (o *nativeObserver) Drain(ctx context.Context) error {
	stable := 0
	for {
		o.mu.Lock()
		members, err := o.members()
		err = errors.Join(err, o.err)
		o.mu.Unlock()
		if err != nil {
			return err
		}
		if len(members) == 0 {
			stable++
			if stable >= 2 {
				return nil
			}
		} else {
			stable = 0
		}
		for _, pid := range members {
			if err := unix.Kill(pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("descendant cleanup deadline exceeded")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (o *nativeObserver) Close() {
	close(o.stop)
	<-o.done
	if o.allow != nil {
		C.free(unsafe.Pointer(o.allow))
		C.free(unsafe.Pointer(o.deny))
	}
}
