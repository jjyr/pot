package terminal

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWindowsConPTYLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	h := New()
	defer h.Close(ctx)
	info, err := h.Create("owner", t.TempDir(), Create{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	s, err := h.Get("owner", info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Get("other", info.ID); err == nil {
		t.Fatal("terminal crossed owner")
	}
	_, out, detach, err := s.Attach()
	if err != nil {
		t.Fatal(err)
	}
	defer detach()
	if err = s.Resize(Size{100, 30}); err != nil {
		t.Fatal(err)
	}
	if err = s.Write([]byte("Write-Output ('conpty-' + 'ready')\r\n")); err != nil {
		t.Fatal(err)
	}
	text := ""
	for !strings.Contains(text, "conpty-ready") {
		select {
		case <-out:
			data, ok := s.Drain()
			if !ok {
				t.Fatal("terminal lagged")
			}
			text += string(data)
		case <-ctx.Done():
			t.Fatalf("ConPTY bridge timed out: %s", text)
		}
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = h.CloseSession(ctx, "owner", info.ID); err != nil {
		t.Fatal(err)
	}
}
