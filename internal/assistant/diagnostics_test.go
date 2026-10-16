package assistant

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/core"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDiagnosticsAreBoundedMetadataAndExcludeErrorPayloads(t *testing.T) {
	var cleanup atomic.Int64
	c := conversationFixture(t, filepath.Join(t.TempDir(), "conversation.sqlite"), "boot", func(context.Context, core.ModelRequest) (io.ReadCloser, error) {
		t.Fatal("diagnostics sampled model")
		return nil, nil
	}, &cleanup)
	ctx := context.Background()
	for i := 0; i < 205; i++ {
		if _, err := c.store.DB().ExecContext(ctx, "INSERT INTO assistant_observations(turn_id,generation,body)VALUES('turn',1,?)", `{"kind":"model","id":"request","error":"private prompt content","usage":{"input":123}}`); err != nil {
			t.Fatal(err)
		}
	}
	result, err := c.Diagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries := result["entries"].([]map[string]any)
	if len(entries) != 200 || entries[0]["sequence"].(uint64) != 6 {
		t.Fatal(entries)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private prompt") || strings.Contains(string(raw), "usage") || strings.Contains(string(raw), "error") {
		t.Fatal("diagnostics exposed payload", string(raw))
	}
}
