package runtime_test

import (
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/processscope"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if processscope.HandleWorker() {
		return
	}
	if len(os.Args) == 2 && os.Args[1] == executor.PatchWorkerArgument {
		if executor.ServePatchWorker(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}
