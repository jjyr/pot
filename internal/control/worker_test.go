package control

import (
	"flag"
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/processscope"
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
	// Each parallel test owns its root, listeners and execution manager. Bound
	// the number of active cases; explicit go test -parallel still wins.
	flag.Parse()
	parallelSet := false
	flag.Visit(func(f *flag.Flag) { parallelSet = parallelSet || f.Name == "test.parallel" })
	if !parallelSet {
		if err := flag.Set("test.parallel", strconv.Itoa(min(8, runtime.GOMAXPROCS(0)))); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}
