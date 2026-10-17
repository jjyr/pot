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
