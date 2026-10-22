package selfupdate

import (
	"errors"
	"github.com/jjyr/pot/pkg/privatefs"
	"os"
	"syscall"
)

const (
	lockShared    = 1
	lockExclusive = 2
	lockNonblock  = 4
)

var connectionRefused = syscall.ECONNREFUSED

// Portable Windows installations are upgraded as a whole while stopped.
func updatesSupported() bool                { return false }
func restartHost(string) error              { return errors.New("Windows portable packages require manual restart") }
func detachedProcess() *syscall.SysProcAttr { return &syscall.SysProcAttr{CreationFlags: 0x200} }
func privateDirectory(path string) error    { return privatefs.Mkdir(path) }
func openLock(path string, how int) (*os.File, error) {
	return privatefs.Lock(path, how&lockShared != 0, how&lockNonblock != 0)
}
