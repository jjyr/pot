package pluginhost

import (
	"github.com/jjyr/pot/pkg/processscope"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if processscope.HandleWorker() {
		return
	}
	os.Exit(m.Run())
}

func testProcessWorker(t *testing.T) []string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{binary}
}
