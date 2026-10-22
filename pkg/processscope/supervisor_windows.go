package processscope

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const WorkerArgument = "--internal-process-supervisor"

type Markers struct{ Allow, Deny string }

// Scope owns a non-inheritable kill-on-close Job Object. The initial thread is
// suspended until assignment, so descendants cannot race job membership.
type Scope struct {
	mu      sync.Mutex
	command *exec.Cmd
	job     windows.Handle
	once    sync.Once
	result  error
}

func Prepare(cmd *exec.Cmd, _ []string, _ Markers) (*Scope, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP
	return &Scope{command: cmd, job: job}, nil
}
func (s *Scope) Started() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME, false, uint32(s.command.Process.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(p)
	if err = windows.AssignProcessToJobObject(s.job, p); err != nil {
		return err
	}
	status, _, _ := windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess").Call(uintptr(p))
	if int32(status) < 0 {
		return windows.NTStatus(status)
	}
	return nil
}
func (s *Scope) Stop(int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.job == 0 {
		return os.ErrProcessDone
	}
	return windows.TerminateJobObject(s.job, 1)
}
func (s *Scope) Abort() { s.mu.Lock(); defer s.mu.Unlock(); s.closeJob() }
func (s *Scope) closeJob() {
	if s.job != 0 {
		windows.CloseHandle(s.job)
		s.job = 0
	}
}
func (s *Scope) Finish() error {
	s.once.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.job == 0 {
			s.result = errors.New("Windows job closed before cleanup confirmation")
			return
		}
		defer s.closeJob()
		if err := windows.TerminateJobObject(s.job, 1); err != nil {
			s.result = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for {
			var info jobAccounting
			err := windows.QueryInformationJobObject(s.job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
			if err != nil {
				s.result = err
				return
			}
			if info.ActiveProcesses == 0 {
				return
			}
			select {
			case <-ctx.Done():
				s.result = errors.New("Windows job cleanup deadline exceeded")
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	return s.result
}

// Windows uses kernel Job Object accounting instead of a Unix supervisor child.
func HandleWorker() bool { return false }

type jobAccounting struct {
	TotalUserTime, TotalKernelTime, ThisPeriodTotalUserTime, ThisPeriodTotalKernelTime int64
	TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses     uint32
}
