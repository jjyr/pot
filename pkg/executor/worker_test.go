package executor

import (
	"github.com/jjyr/pot/pkg/processscope"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if processscope.HandleWorker() {
		return
	}
	if len(os.Args) == 2 && os.Args[1] == PatchWorkerArgument {
		if ServePatchWorker(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}
